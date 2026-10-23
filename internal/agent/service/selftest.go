package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

// selfTestTimeout is how long a test message may take to come back.
const selfTestTimeout = 3 * time.Minute

// ErrSelfTestRunning is returned when a device is already testing.
var ErrSelfTestRunning = errors.New("这台设备正在自检，请等待结果")

func selfTestToken() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// SelfTest sends a message with a random token from a device to a number
// (its own, or another device's) and waits for it to arrive on any device.
// It returns once the message is queued; the outcome lands in the device's
// health and the event log. Test messages are deleted afterwards.
func (m *Manager) SelfTest(ctx context.Context, deviceID, to string) error {
	svc := m.Device(deviceID)
	if svc == nil {
		return ErrOffline
	}
	if st := svc.Snapshot().Health.SelfTest; st != nil && st.State == "running" {
		return ErrSelfTestRunning
	}
	token := selfTestToken()
	got := make(chan store.Message, 1)
	m.mu.Lock()
	m.selfTests[token] = got
	m.mu.Unlock()
	started := time.Now()
	svc.update(func(snap *Snapshot) {
		snap.Health.SelfTest = &SelfTest{State: "running", To: to, StartedAt: started}
	})
	out, err := svc.Send(ctx, to, "号小助收发自检 "+token+"（自动发送，收到后自动删除）")
	if err != nil {
		m.finishSelfTest(svc, token, 0, false, 0, "发送失败："+err.Error())
		return err
	}
	go m.awaitSelfTest(svc, token, out.ID, got, started)
	return nil
}

func (m *Manager) awaitSelfTest(svc *Service, token string, outID int64, got <-chan store.Message, started time.Time) {
	deadline := time.NewTimer(selfTestTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var received *store.Message
	sent := false
	for {
		select {
		case msg := <-got:
			received = &msg
		case <-tick.C:
		case <-deadline.C:
			detail := "发送超时：模组一直没有确认发出"
			if sent {
				detail = "短信已发出，但 3 分钟内没有收到。可能是号码填错、运营商延迟，或这张卡收不到短信"
			}
			m.finishSelfTest(svc, token, outID, false, 0, detail)
			return
		}
		if !sent {
			out, err := m.store.GetMessage(context.Background(), outID)
			switch {
			case err != nil:
				m.finishSelfTest(svc, token, outID, false, 0, "自检短信记录丢失")
				return
			case out.Status == store.StatusFailed:
				m.finishSelfTest(svc, token, outID, false, 0, "短信没能发出："+out.Error)
				return
			case out.Status == store.StatusSent:
				sent = true
			}
		}
		if sent && received != nil {
			delay := received.ReceivedAt.Sub(started)
			where := ""
			if received.DeviceID != svc.DeviceID() {
				if other := m.Device(received.DeviceID); other != nil {
					where = "，由「" + other.Snapshot().Device.Name + "」收到"
				}
			}
			m.finishSelfTest(svc, token, outID, true, delay, fmt.Sprintf("发送和接收都正常，用时 %d 秒%s", int(delay.Seconds()+0.5), where))
			return
		}
	}
}

func (m *Manager) finishSelfTest(svc *Service, token string, outID int64, passed bool, delay time.Duration, detail string) {
	m.mu.Lock()
	delete(m.selfTests, token)
	m.mu.Unlock()
	if outID != 0 {
		m.store.DeleteMessages(context.Background(), []int64{outID})
	}
	state, level := "failed", "warn"
	if passed {
		state, level = "passed", "info"
	}
	svc.update(func(snap *Snapshot) {
		if st := snap.Health.SelfTest; st != nil {
			done := *st
			done.State, done.FinishedAt, done.Delay, done.Detail = state, time.Now(), delay, detail
			snap.Health.SelfTest = &done
		}
	})
	svc.note(context.Background(), level, "收发自检"+map[bool]string{true: "通过", false: "未通过"}[passed]+"："+detail)
	m.hub.Publish(TopicMessages)
}

// takeSelfTests removes incoming self-test messages before anything else
// sees them (no classification, no unread badge) and hands them to their
// waiting test. It returns the other messages.
func (m *Manager) takeSelfTests(msgs []store.Message) []store.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.selfTests) == 0 {
		return msgs
	}
	rest := msgs[:0:0]
	for _, msg := range msgs {
		matched := false
		for token, got := range m.selfTests {
			if msg.Direction == store.DirectionIn && strings.Contains(msg.Body, token) {
				matched = true
				m.store.DeleteMessages(context.Background(), []int64{msg.ID})
				select {
				case got <- msg:
				default:
				}
			}
		}
		if !matched {
			rest = append(rest, msg)
		}
	}
	return rest
}
