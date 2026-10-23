package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizePeer(t *testing.T) {
	for in, want := range map[string]string{
		"+8613812345678":  "13812345678",
		"008613812345678": "13812345678",
		"8613812345678":   "13812345678",
		"138 1234 5678":   "13812345678",
		"95588":           "95588",
		"+12025550123":    "+12025550123",
		"1069012345":      "1069012345",
		"Apple":           "APPLE",
		"86955880000000":  "86955880000000",
	} {
		if got := NormalizePeer(in); got != want {
			t.Errorf("NormalizePeer(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConversationsGroupByDeviceAndPeer(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	add := func(hash, device, sender, body string, at time.Time) {
		t.Helper()
		seg := segment(hash, sender, 0, 1, 1, body, at)
		seg.DeviceID = device
		if _, err := s.SaveSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "dev1", "+8613800000001", "你好", now.Add(-3*time.Minute))
	add("b", "dev1", "13800000001", "在吗", now.Add(-2*time.Minute))
	add("c", "dev2", "13800000001", "另一张卡", now.Add(-time.Minute))
	add("d", "dev1", "95588", "账单", now.Add(-10*time.Minute))
	if _, err := s.AssemblePending(ctx, now, time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	out, err := s.AddOutgoing(ctx, "dev1", "+86 138 0000 0001", "回复", 1, now)
	if err != nil {
		t.Fatal(err)
	}

	convs, total, err := s.ListConversations(ctx, Filter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(convs) != 3 {
		t.Fatalf("conversations = %d (%+v)", total, convs)
	}
	first := convs[0]
	if first.DeviceID != "dev1" || first.Peer != "13800000001" || first.Total != 3 || first.Unread != 2 || first.Last.ID != out.ID {
		t.Fatalf("newest conversation = %+v", first)
	}
	if convs[1].DeviceID != "dev2" || convs[1].Peer != "13800000001" {
		t.Fatalf("same number on another device must be its own conversation: %+v", convs[1])
	}

	// Filtering matches inside a conversation but the badge counts it all.
	convs, total, _ = s.ListConversations(ctx, Filter{Query: "在吗"}, 10, 0)
	if total != 1 || convs[0].Unread != 2 {
		t.Fatalf("filtered = %d %+v", total, convs)
	}
	if n, _ := s.MarkConversationRead(ctx, "dev1", "13800000001"); n != 2 {
		t.Fatalf("marked %d read", n)
	}
	if _, total, _ = s.ListConversations(ctx, Filter{Unread: true}, 10, 0); total != 2 {
		t.Fatalf("unread conversations = %d, want 2 (dev2 and 95588)", total)
	}
	// Outgoing messages are never classified or counted as unread.
	pending, _ := s.Unclassified(ctx, 10)
	for _, m := range pending {
		if m.Direction != DirectionIn {
			t.Fatalf("outgoing message queued for classification: %+v", m)
		}
	}
	if err := s.SetOutgoingStatus(ctx, out.ID, StatusFailed, "网络错误"); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMessage(ctx, out.ID); m.Status != StatusFailed || m.Error != "网络错误" || m.Direction != DirectionOut {
		t.Fatalf("outgoing = %+v", m)
	}
	if n, err := s.DeleteConversation(ctx, "dev1", "13800000001"); err != nil || n != 3 {
		t.Fatalf("deleted %d, %v", n, err)
	}
}

func TestLegacyMessagesAdoptedOnRequest(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	if _, err := s.SaveSegment(ctx, segment("x", "10086", 0, 1, 1, "旧短信", now)); err != nil {
		t.Fatal(err)
	}
	s.AssemblePending(ctx, now, time.Hour, nil)
	d, err := s.SeeDevice(ctx, Device{ID: "866", Name: "EC20", IMEI: "866"}, now)
	if err != nil || !d.Enabled || d.Name != "EC20" {
		t.Fatalf("SeeDevice = %+v, %v", d, err)
	}
	if n, _ := s.Orphans(ctx); n != 1 {
		t.Fatalf("orphans = %d, want 1 (seeing a device must not adopt them)", n)
	}
	if n, err := s.AdoptOrphans(ctx, "866"); err != nil || n != 1 {
		t.Fatalf("adopted %d, %v", n, err)
	}
	if _, total, _ := s.ListMessages(ctx, Filter{DeviceID: "866"}, 10, 0); total != 1 {
		t.Fatal("legacy message not adopted")
	}
	name := "家里的卡"
	if err := s.UpdateDevice(ctx, "866", &name, nil); err != nil {
		t.Fatal(err)
	}
	// Seeing it again keeps the user's name.
	d, _ = s.SeeDevice(ctx, Device{ID: "866", Name: "EC20", IMEI: "866", Endpoint: "usb 1-2"}, now)
	if d.Name != name || d.Endpoint != "usb 1-2" {
		t.Fatalf("device after reconnect = %+v", d)
	}
	if _, err := s.SaveSegment(ctx, Segment{PDUHash: "new", LegacyHash: "x", PDU: "p", Sender: "1", SentAt: now, ReceivedAt: now, Total: 1, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM segments`).Scan(&n)
	if n != 1 {
		t.Fatalf("segment stored again under its new hash: %d segments", n)
	}
}

func TestCallLog(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	in, err := s.StartCall(ctx, CallRecord{DeviceID: "d1", Number: "+8613800000001", Direction: DirectionIn, Status: CallRinging, StartedAt: now})
	if err != nil || in.Peer != "13800000001" {
		t.Fatalf("StartCall = %+v, %v", in, err)
	}
	s.EndCall(ctx, in.ID, CallMissed, now.Add(10*time.Second))
	out, _ := s.StartCall(ctx, CallRecord{DeviceID: "d1", Number: "10086", Direction: DirectionOut, Status: CallDialing, StartedAt: now.Add(time.Minute)})
	s.AnswerCall(ctx, out.ID, now.Add(70*time.Second))
	if n, _ := s.UnseenMissedCalls(ctx); n != 1 {
		t.Fatalf("unseen missed = %d", n)
	}
	// A restart in the middle of a call closes it as completed.
	if err := s.EndOpenCalls(ctx, "d1", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.GetCall(ctx, out.ID); c.Status != CallCompleted || c.AnsweredAt.IsZero() || c.EndedAt.IsZero() {
		t.Fatalf("outgoing after restart = %+v", c)
	}
	calls, total, _ := s.ListCalls(ctx, "", CallsMissed, 10, 0)
	if total != 1 || calls[0].ID != in.ID {
		t.Fatalf("missed = %+v", calls)
	}
	s.MarkCallsSeen(ctx)
	if n, _ := s.UnseenMissedCalls(ctx); n != 0 {
		t.Fatalf("unseen after mark = %d", n)
	}
}

func TestCallbackCandidates(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()
	in := func(number, status, by string, ago time.Duration) CallRecord {
		c, err := s.StartCall(ctx, CallRecord{DeviceID: "d1", Peer: NormalizePeer(number), Number: number, Direction: DirectionIn, Status: CallRinging, StartedAt: now.Add(-ago)})
		if err != nil {
			t.Fatal(err)
		}
		s.EndCall(ctx, c.ID, status, now.Add(-ago+time.Minute))
		if by != "" {
			s.SetCallHandler(ctx, c.ID, by)
		}
		return c
	}
	in("13800000001", CallMissed, "", 2*time.Hour) // waiting
	in("13800000002", CallMissed, "", 3*time.Hour) // called back below
	ai := in("13800000003", CallCompleted, HandledByAssistant, time.Hour)
	in("13800000004", CallMissed, "", 5*24*time.Hour) // too old
	in("13800000001", CallMissed, "", 4*time.Hour)    // older call, same number
	out, _ := s.StartCall(ctx, CallRecord{DeviceID: "d1", Peer: NormalizePeer("13800000002"), Number: "13800000002", Direction: DirectionOut, Status: CallDialing, StartedAt: now.Add(-time.Hour)})
	s.EndCall(ctx, out.ID, CallCompleted, now)

	got, err := s.CallbackCandidates(ctx, now.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var numbers []string
	for _, c := range got {
		numbers = append(numbers, c.Number)
	}
	if strings.Join(numbers, ",") != "13800000003,13800000001" {
		t.Fatalf("candidates = %v", numbers)
	}
	if err := s.MarkCallbackDone(ctx, ai.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.CallbackCandidates(ctx, now.Add(-72*time.Hour))
	if len(got) != 1 || got[0].Number != "13800000001" {
		t.Fatalf("after done: %v", got)
	}
	if err := s.MarkCallbackDone(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown call: %v", err)
	}
}
