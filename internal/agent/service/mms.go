package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/mms"
	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

const (
	// mmsTimeout bounds a whole download, from opening the MMS APN on.
	mmsTimeout = 4 * time.Minute
	// mmsWait is how long the MMSC may take to answer one request.
	mmsWait = 60 * time.Second
	// mmsLimit is larger than any carrier lets an MMS be.
	mmsLimit = 2 << 20
	// mmsChunks is how many file reads one operation does, so a call
	// arriving meanwhile is noticed within a second or two.
	mmsChunks = 16
	mmsFile   = "UFS:haobot_mms.bin"
)

// DownloadMMS fetches an MMS's content from the carrier's MMSC through the
// module and its MMS APN. It returns once the download has started; the
// outcome is stored on the message and announced through the hub.
func (s *Service) DownloadMMS(ctx context.Context, msg store.Message) error {
	if msg.Kind != store.KindMMS || msg.MMSURL == "" {
		return errors.New("这条彩信通知没有可下载的地址")
	}
	snap := s.Snapshot()
	switch {
	case !snap.Connected:
		return ErrOffline
	case !snap.Device.Enabled:
		return errors.New("设备已停用，请先在设备页启用")
	case snap.Modem.SIM != modem.SIMReady:
		return ErrNotReady
	case len(snap.Calls) > 0:
		return errors.New("正在通话，挂断后再下载彩信")
	}
	carrier, err := mms.DetectCarrier(snap.Identity.IMSI, snap.Modem.Operator)
	if err != nil {
		return err
	}
	if !s.mmsBusy.CompareAndSwap(false, true) {
		return errors.New("这台设备正在下载另一条彩信，请稍候")
	}
	if err := s.store.StartMMSDownload(ctx, msg.ID); err != nil {
		s.mmsBusy.Store(false)
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("这条彩信正在下载")
		}
		return err
	}
	s.hub.Publish(TopicMessages)
	go func() {
		defer s.mmsBusy.Store(false)
		s.fetchMMS(msg, carrier)
	}()
	return nil
}

func (s *Service) fetchMMS(msg store.Message, carrier mms.Carrier) {
	ctx, cancel := context.WithTimeout(context.Background(), mmsTimeout)
	defer cancel()
	start := time.Now()
	retrieved, err := s.retrieveMMS(ctx, msg.MMSURL, carrier)
	if err == nil {
		err = s.store.SaveMMSContent(ctx, msg.ID, retrieved.Subject, storedParts(retrieved.Parts))
	}
	if err != nil {
		reason := err.Error()
		if !msg.MMSExpiry.IsZero() && time.Now().After(msg.MMSExpiry) {
			reason += "（彩信已过有效期，运营商可能已经删除）"
		}
		s.note(ctx, "warn", fmt.Sprintf("下载来自 %s 的彩信失败（%s）：%s", msg.Sender, carrier.Name, reason))
		if err := s.store.FailMMSDownload(context.WithoutCancel(ctx), msg.ID, reason); err != nil {
			s.log.Error("record MMS download", "id", msg.ID, "error", err)
		}
	} else {
		s.note(ctx, "info", fmt.Sprintf("已下载来自 %s 的彩信（%d 个部分，用时 %s）", msg.Sender, len(retrieved.Parts), time.Since(start).Round(time.Second)))
	}
	s.hub.Publish(TopicMessages)
}

// retrieveMMS runs the download as a series of short operations, so calls
// and SMS are still handled between them.
func (s *Service) retrieveMMS(ctx context.Context, target string, carrier mms.Carrier) (mms.Retrieved, error) {
	connect, raw, err := carrier.GetRequest(target)
	if err != nil {
		return mms.Retrieved{}, err
	}
	var cid int
	var opened bool
	if err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		cid, opened, err = m.OpenContext(ctx, carrier.APN)
		return err
	}); err != nil {
		return mms.Retrieved{}, fmt.Errorf("打开彩信数据连接（APN %s）失败：%w", carrier.APN, friendly(err))
	}
	if opened {
		defer s.Do(context.WithoutCancel(ctx), func(ctx context.Context, m *modem.Modem) error {
			if err := m.CloseContext(ctx, cid); err != nil {
				s.log.Warn("close MMS context", "cid", cid, "error", err)
			}
			return nil
		})
	}

	status, err := s.httpOnModem(ctx, modem.HTTPRequest{Context: cid, Connect: connect, Raw: raw, Wait: mmsWait})
	if err != nil {
		return mms.Retrieved{}, fmt.Errorf("请求彩信中心失败：%w", err)
	}
	if status != 200 {
		return mms.Retrieved{}, fmt.Errorf("彩信中心返回 HTTP %d", status)
	}
	data, err := s.readResponse(ctx)
	if err != nil {
		return mms.Retrieved{}, fmt.Errorf("读取彩信内容失败：%w", err)
	}
	retrieved, err := mms.ParseRetrieve(data)
	if errors.Is(err, mms.ErrRetrieveFailed) {
		return retrieved, fmt.Errorf("彩信中心拒绝了下载：%w", err)
	}
	if err != nil {
		return retrieved, fmt.Errorf("无法解析彩信内容（%d 字节）：%w", len(data), err)
	}
	if retrieved.TransactionID != "" {
		s.acknowledgeMMS(ctx, cid, carrier, retrieved.TransactionID)
	}
	return retrieved, nil
}

// httpOnModem sends one request and waits for its status outside the
// modem's operation loop.
func (s *Service) httpOnModem(ctx context.Context, req modem.HTTPRequest) (int, error) {
	var pending *modem.Pending
	if err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		var err error
		pending, err = m.StartHTTP(ctx, req)
		return err
	}); err != nil {
		return 0, friendly(err)
	}
	value, err := pending.Wait(ctx, req.Wait+15*time.Second)
	if err != nil {
		return 0, err
	}
	return modem.ParseHTTPResult(value)
}

// readResponse copies the last response body out of the module.
func (s *Service) readResponse(ctx context.Context) ([]byte, error) {
	var pending *modem.Pending
	if err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if err := m.DeleteFile(ctx, mmsFile); err != nil {
			return err
		}
		var err error
		pending, err = m.SaveHTTPResponse(ctx, mmsFile, mmsWait)
		return err
	}); err != nil {
		return nil, friendly(err)
	}
	value, err := pending.Wait(ctx, mmsWait+15*time.Second)
	if err != nil {
		return nil, err
	}
	if value != "0" {
		return nil, fmt.Errorf("模组保存响应失败（%s）", value)
	}
	defer s.Do(context.WithoutCancel(ctx), func(ctx context.Context, m *modem.Modem) error {
		return m.DeleteFile(ctx, mmsFile)
	})

	var handle int
	if err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		var err error
		handle, err = m.OpenFile(ctx, mmsFile)
		return err
	}); err != nil {
		return nil, friendly(err)
	}
	defer s.Do(context.WithoutCancel(ctx), func(ctx context.Context, m *modem.Modem) error {
		return m.CloseFile(ctx, handle)
	})
	var data []byte
	for done := false; !done; {
		if err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
			for range mmsChunks {
				block, err := m.ReadChunk(ctx, handle)
				if err != nil {
					return err
				}
				data = append(data, block...)
				if len(block) < modem.FileChunk {
					done = true
					return nil
				}
			}
			return nil
		}); err != nil {
			return nil, friendly(err)
		}
		if len(data) > mmsLimit {
			return nil, fmt.Errorf("彩信超过 %d MB", mmsLimit>>20)
		}
	}
	return data, nil
}

// acknowledgeMMS tells the MMSC the message arrived. Failure only means the
// sender gets no delivery report, so it is just logged.
func (s *Service) acknowledgeMMS(ctx context.Context, cid int, carrier mms.Carrier, transactionID string) {
	connect, raw, err := carrier.PostRequest(mms.Acknowledge(transactionID))
	if err == nil {
		var status int
		status, err = s.httpOnModem(ctx, modem.HTTPRequest{Context: cid, Connect: connect, Post: true, Raw: raw, Wait: 30 * time.Second})
		if err == nil && status != 200 && status != 204 {
			err = fmt.Errorf("HTTP %d", status)
		}
	}
	if err != nil {
		s.log.Warn("acknowledge MMS", "error", err)
	}
}

func storedParts(parts []mms.Part) []store.MMSPart {
	out := make([]store.MMSPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, store.MMSPart{ContentType: p.ContentType, Name: p.Name, Data: p.Data})
	}
	return out
}
