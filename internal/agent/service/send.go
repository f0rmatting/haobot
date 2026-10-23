package service

import (
	"context"
	"errors"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// sendTimeout bounds one send, including waiting for the modem to be free.
const sendTimeout = 3 * time.Minute

// ErrNotReady is returned when the SIM cannot send yet.
var ErrNotReady = errors.New("SIM 卡未就绪，暂时无法发送短信")

// Send queues text to a number. The message is stored at once with status
// "sending" and updated when the modem answers, so the UI shows it
// immediately; the result arrives through the hub.
func (s *Service) Send(ctx context.Context, to, text string) (store.Message, error) {
	to, err := modem.CleanNumber(to)
	if err != nil {
		return store.Message{}, err
	}
	pdus, err := modem.EncodeSubmit(to, text)
	if err != nil {
		return store.Message{}, err
	}
	if err := s.canSend(); err != nil {
		return store.Message{}, err
	}
	msg, err := s.store.AddOutgoing(ctx, s.DeviceID(), to, text, len(pdus), time.Now())
	if err != nil {
		return msg, err
	}
	s.hub.Publish(TopicMessages)
	go s.deliver(msg)
	return msg, nil
}

// Resend retries a failed outgoing message.
func (s *Service) Resend(ctx context.Context, msg store.Message) (store.Message, error) {
	if msg.Direction != store.DirectionOut || msg.Status != store.StatusFailed {
		return msg, errors.New("只能重发发送失败的短信")
	}
	if err := s.canSend(); err != nil {
		return msg, err
	}
	if err := s.store.SetOutgoingStatus(ctx, msg.ID, store.StatusSending, ""); err != nil {
		return msg, err
	}
	msg.Status, msg.Error = store.StatusSending, ""
	s.hub.Publish(TopicMessages)
	go s.deliver(msg)
	return msg, nil
}

func (s *Service) canSend() error {
	snap := s.Snapshot()
	switch {
	case !snap.Connected:
		return ErrOffline
	case !snap.Device.Enabled:
		return errors.New("设备已停用，请先在设备页启用")
	case snap.Modem.SIM != modem.SIMReady || !snap.SMSReady:
		return ErrNotReady
	}
	return nil
}

func (s *Service) deliver(msg store.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		_, err := m.SendSMS(ctx, msg.Sender, msg.Body)
		return friendly(err)
	})
	status, text := store.StatusSent, ""
	if err != nil {
		status, text = store.StatusFailed, err.Error()
		s.note(ctx, "warn", "发送短信到 "+msg.Sender+" 失败："+text)
	}
	if err := s.store.SetOutgoingStatus(context.Background(), msg.ID, status, text); err != nil {
		s.log.Error("record send result", "id", msg.ID, "error", err)
	}
	s.hub.Publish(TopicMessages)
}
