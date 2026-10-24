package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func segment(hash, sender string, ref, total, seq int, body string, received time.Time) Segment {
	return Segment{
		PDUHash: hash, PDU: hash, Sender: sender, SentAt: received, ReceivedAt: received,
		Ref: ref, Total: total, Seq: seq, Body: body,
	}
}

func TestSaveSegmentDeduplicates(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	for i, want := range []bool{true, false} {
		inserted, err := s.SaveSegment(ctx, segment("h1", "10010", 0, 1, 1, "hi", now))
		if err != nil || inserted != want {
			t.Fatalf("attempt %d: inserted=%v err=%v, want %v", i, inserted, err, want)
		}
	}
}

func TestAssembleMultipartInOrder(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	// Parts arrive out of order, interleaved with a single-part message.
	for _, seg := range []Segment{
		segment("b", "10086", 7, 2, 2, "世界", now),
		segment("x", "10010", 0, 1, 1, "单条", now),
	} {
		if _, err := s.SaveSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	created, err := s.AssemblePending(ctx, now, time.Hour, nil)
	if err != nil || len(created) != 1 || created[0].Body != "单条" {
		t.Fatalf("first pass: %+v err=%v", created, err)
	}
	if _, err := s.SaveSegment(ctx, segment("a", "10086", 7, 2, 1, "你好", now)); err != nil {
		t.Fatal(err)
	}
	created, err = s.AssemblePending(ctx, now, time.Hour, nil)
	if err != nil || len(created) != 1 {
		t.Fatalf("second pass: %+v err=%v", created, err)
	}
	if got := created[0]; got.Body != "你好世界" || !got.Complete || got.Parts != 2 {
		t.Fatalf("assembled %+v", got)
	}
	messages, total, err := s.ListMessages(ctx, Filter{Query: "世界"}, 10, 0)
	if err != nil || total != 1 || len(messages) != 1 {
		t.Fatalf("search: total=%d err=%v", total, err)
	}
}

func TestAssembleStalePartial(t *testing.T) {
	s, ctx, then := openTest(t), context.Background(), time.Now().Add(-3*time.Hour)
	if _, err := s.SaveSegment(ctx, segment("p", "95588", 3, 3, 2, "中间", then)); err != nil {
		t.Fatal(err)
	}
	created, err := s.AssemblePending(ctx, time.Now(), 2*time.Hour, nil)
	if err != nil || len(created) != 1 {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if got := created[0]; got.Complete || got.Body != "[缺失分段]中间[缺失分段]" {
		t.Fatalf("partial %+v", got)
	}
}

func TestAssembleTextNumberedParts(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	// A lone part is no message yet, so nothing classifies or notifies it.
	if _, err := s.SaveSegment(ctx, segment("a1", "10690", 0, 1, 1, "(1/2)防骗请关注“803反诈”，安装“", now)); err != nil {
		t.Fatal(err)
	}
	if created, err := s.AssemblePending(ctx, now, time.Hour, nil); err != nil || len(created) != 0 {
		t.Fatalf("lone part: created=%+v err=%v", created, err)
	}
	if pending, err := s.Unclassified(ctx, 10); err != nil || len(pending) != 0 {
		t.Fatalf("lone part unclassified=%+v err=%v", pending, err)
	}
	// The SMSC sent a long message as plain SMS numbered in their text; a
	// second message with the same numbering follows.
	for _, seg := range []Segment{
		segment("b1", "10690", 0, 1, 1, "(1/2)另一条", now),
		segment("a2", "10690", 0, 1, 1, "(2/2)金护”app。", now),
		segment("b2", "10690", 0, 1, 1, "（2/2）短信", now),
		segment("c", "10690", 0, 1, 1, "(3/2)不是分段", now),
	} {
		if _, err := s.SaveSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	created, err := s.AssemblePending(ctx, now, time.Hour, nil)
	if err != nil || len(created) != 3 {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	want := []string{"防骗请关注“803反诈”，安装“金护”app。", "另一条短信", "(3/2)不是分段"}
	for i, msg := range created {
		if msg.Body != want[i] || !msg.Complete {
			t.Fatalf("message %d: %+v, want body %q", i, msg, want[i])
		}
	}

	// Re-decoding keeps the parts together and the prefixes out.
	decode := func(raw string) (string, bool, error) {
		return map[string]string{"a1": "(1/2)防骗", "a2": "(2/2)金护"}[raw], false, nil
	}
	if _, err := s.Redecode(ctx, "text-parts", decode); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, created[0].ID)
	if err != nil || got.Body != "防骗金护" {
		t.Fatalf("after redecode: %+v err=%v", got, err)
	}
}

func TestAssembleTextNumberedPartsBySendTime(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	// Two messages a minute apart, their second parts arriving swapped; a
	// part sent much later belongs to neither.
	at := func(seg Segment, sent time.Time) Segment { seg.SentAt = sent; return seg }
	for _, seg := range []Segment{
		at(segment("a1", "10690", 0, 1, 1, "(1/2)甲", now), now.Add(-time.Minute)),
		at(segment("b1", "10690", 0, 1, 1, "(1/2)乙", now), now),
		at(segment("b2", "10690", 0, 1, 1, "(2/2)二", now), now),
		at(segment("a2", "10690", 0, 1, 1, "(2/2)一", now), now.Add(-time.Minute)),
		at(segment("c2", "10690", 0, 1, 1, "(2/2)晚", now), now.Add(time.Hour)),
	} {
		if _, err := s.SaveSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	created, err := s.AssemblePending(ctx, now, time.Hour, nil)
	if err != nil || len(created) != 2 || created[0].Body != "甲一" || created[1].Body != "乙二" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
}

func TestOpenMigratesFirstReleaseSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// The first release had no binary columns.
	if _, err := db.Exec(`
		CREATE TABLE segments (id INTEGER PRIMARY KEY, pdu_hash TEXT NOT NULL UNIQUE, pdu TEXT NOT NULL,
			sender TEXT NOT NULL, sent_at INTEGER NOT NULL, received_at INTEGER NOT NULL, ref INTEGER NOT NULL,
			total INTEGER NOT NULL, seq INTEGER NOT NULL, body TEXT NOT NULL, message_id INTEGER);
		CREATE TABLE messages (id INTEGER PRIMARY KEY, sender TEXT NOT NULL, body TEXT NOT NULL,
			sent_at INTEGER NOT NULL, received_at INTEGER NOT NULL, parts INTEGER NOT NULL, complete INTEGER NOT NULL);
		INSERT INTO messages VALUES (1, '1064', 'garbled', 0, 0, 1, 1);
		INSERT INTO segments VALUES (1, 'h', 'RAW', '1064', 0, 0, 0, 1, 1, 'garbled', 1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	decode := func(raw string) (string, bool, error) { return "30EB1F", true, nil }
	changed, err := s.Redecode(ctx, "2", decode)
	if err != nil || changed != 1 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	messages, _, err := s.ListMessages(ctx, Filter{}, 10, 0)
	if err != nil || len(messages) != 1 || messages[0].Body != "30EB1F" || !messages[0].Binary {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	// The same decoder version does not run twice.
	if changed, err := s.Redecode(ctx, "2", decode); err != nil || changed != 0 {
		t.Fatalf("second run: changed=%d err=%v", changed, err)
	}
}

func TestSessions(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	if err := s.SetPasswordHash(ctx, "admin", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "tok", "admin", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if user, err := s.SessionUser(ctx, "tok", now); err != nil || user != "admin" {
		t.Fatalf("user=%q err=%v", user, err)
	}
	if _, err := s.SessionUser(ctx, "tok", now.Add(2*time.Hour)); err != ErrNotFound {
		t.Fatalf("expired session: err=%v", err)
	}
	// Changing the password signs every session out.
	if err := s.SetPasswordHash(ctx, "admin", "hash2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "tok", now); err != ErrNotFound {
		t.Fatalf("session survived password change: err=%v", err)
	}
}

func TestCategoriesReadAndDelete(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	for i, body := range []string{"一", "二", "三"} {
		if _, err := s.SaveSegment(ctx, segment(body, "100", 0, 1, 1, body, now.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	created, err := s.AssemblePending(ctx, now, time.Hour, nil)
	if err != nil || len(created) != 3 {
		t.Fatalf("assembled %d, %v", len(created), err)
	}
	pending, _ := s.Unclassified(ctx, 10)
	if len(pending) != 3 || pending[0].Read {
		t.Fatalf("unclassified = %+v", pending)
	}
	if err := s.SetCategory(ctx, created[0].ID, "账单交易", "llm", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCategory(ctx, created[1].ID, "验证码", "manual", now); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.ListMessages(ctx, Filter{Category: Uncategorized}, 10, 0); total != 1 {
		t.Fatalf("uncategorised = %d, want 1", total)
	}
	// Reclassifying everything keeps manual choices.
	if n, _ := s.ResetCategories(ctx, nil); n != 2 {
		t.Fatalf("reset %d, want 2", n)
	}
	if m, _ := s.GetMessage(ctx, created[1].ID); m.Category != "验证码" {
		t.Fatalf("manual category lost: %+v", m)
	}
	if err := s.MarkRead(ctx, []int64{created[2].ID}, true); err != nil {
		t.Fatal(err)
	}
	stats, err := s.MessageStats(ctx, now.Add(-time.Hour))
	if err != nil || stats.Total != 3 || stats.Unread != 2 || stats.Pending != 2 || stats.Categories["验证码"] != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	if n, err := s.DeleteMessages(ctx, []int64{created[0].ID, created[1].ID}); err != nil || n != 2 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	var segments int
	s.db.QueryRow(`SELECT COUNT(*) FROM segments`).Scan(&segments)
	if segments != 1 {
		t.Fatalf("segments left %d, want 1", segments)
	}
	if _, err := s.GetMessage(ctx, created[0].ID); err != ErrNotFound {
		t.Fatalf("deleted message lookup err = %v", err)
	}
}

func TestExtendSessionAtMostOncePerInterval(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	if err := s.SetPasswordHash(ctx, "admin", "h"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ttl, every := 30*24*time.Hour, 24*time.Hour
	if err := s.CreateSession(ctx, "tok", "admin", now.Add(ttl)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ExtendSession(ctx, "tok", now.Add(time.Hour), ttl, every); ok {
		t.Fatal("fresh session extended")
	}
	later := now.Add(10 * 24 * time.Hour)
	if ok, err := s.ExtendSession(ctx, "tok", later, ttl, every); !ok || err != nil {
		t.Fatalf("active session not extended: %v", err)
	}
	// It now lasts 30 days from the last use, well past the original expiry.
	if _, err := s.SessionUser(ctx, "tok", now.Add(35*24*time.Hour)); err != nil {
		t.Fatalf("extended session expired: %v", err)
	}
	if ok, _ := s.ExtendSession(ctx, "tok", now.Add(41*24*time.Hour), ttl, every); ok {
		t.Fatal("expired session extended")
	}
}

func TestActivityBuckets(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 15, 30, 0, 0, time.Local)
	for i, c := range []struct {
		at       time.Time
		category string
	}{{now.Add(-10 * time.Minute), "验证码"}, {now.Add(-70 * time.Minute), "营销推广"}, {now.Add(-30 * time.Hour), ""}, {now.Add(-9 * 24 * time.Hour), ""}} {
		st.SaveSegment(ctx, Segment{PDUHash: fmt.Sprint("h", i), PDU: "p", Sender: "10690", SentAt: c.at, ReceivedAt: c.at, Total: 1, Seq: 1, Body: "x", DeviceID: "d1"})
		msgs, _ := st.AssemblePending(ctx, c.at, 0, nil)
		for _, m := range msgs {
			if c.category != "" {
				st.SetCategory(ctx, m.ID, c.category, "llm", c.at)
			}
		}
	}
	call, _ := st.StartCall(ctx, CallRecord{DeviceID: "d1", Number: "138", Direction: DirectionIn, Status: CallRinging, StartedAt: now.Add(-5 * time.Minute)})
	st.EndCall(ctx, call.ID, CallMissed, now)
	a, err := st.Activity(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	last, prev := a.Hours[23], a.Hours[22]
	if last.SMS != 1 || last.Calls != 1 || last.Missed != 1 || prev.SMS != 1 || prev.Spam != 1 {
		t.Fatalf("hours: last %+v, prev %+v", last, prev)
	}
	today, yesterday := a.Days[6], a.Days[5]
	if today.SMS != 2 || today.Spam != 1 || yesterday.SMS != 1 {
		t.Fatalf("days: today %+v, yesterday %+v", today, yesterday)
	}
	total := 0
	for _, d := range a.Days {
		total += d.SMS
	}
	if total != 3 {
		t.Fatalf("message from 9 days ago counted: %d", total)
	}
}

func TestListEventsFilterAndPage(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		level := "info"
		if i%5 == 0 {
			level = "warn"
		}
		if i == 7 {
			level = "error"
		}
		st.AddEvent(ctx, level, fmt.Sprintf("事件 %d", i))
	}
	page, total, err := st.ListEvents(ctx, EventFilter{}, 10, 10)
	if err != nil || total != 25 || len(page) != 10 || page[0].Text != "事件 14" {
		t.Fatalf("page 2 = %v (total %d, %v)", page, total, err)
	}
	warn, total, _ := st.ListEvents(ctx, EventFilter{Level: "warn"}, 50, 0)
	if total != 6 || len(warn) != 6 {
		t.Fatalf("warn total %d", total)
	}
	found, total, _ := st.ListEvents(ctx, EventFilter{Query: "事件 2"}, 50, 0)
	if total != 6 || found[0].Text != "事件 24" { // 2, 20..24
		t.Fatalf("query total %d: %v", total, found)
	}
	counts, _ := st.EventCounts(ctx)
	if counts["info"] != 19 || counts["warn"] != 5 || counts["error"] != 1 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestFilterSeveralCategoriesSince(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	now := time.Now()
	for i, c := range []struct {
		at       time.Time
		category string
	}{{now, "营销推广"}, {now, "推销骚扰"}, {now, "验证码"}, {now.Add(-48 * time.Hour), "营销推广"}} {
		st.SaveSegment(ctx, Segment{PDUHash: fmt.Sprint("f", i), PDU: "p", Sender: fmt.Sprint("1069", i), SentAt: c.at, ReceivedAt: c.at, Total: 1, Seq: 1, Body: "x", DeviceID: "d1"})
		msgs, _ := st.AssemblePending(ctx, c.at, 0, nil)
		for _, m := range msgs {
			st.SetCategory(ctx, m.ID, c.category, "llm", c.at)
		}
	}
	_, total, err := st.ListMessages(ctx, Filter{Category: "营销推广,推销骚扰"}, 50, 0)
	if err != nil || total != 3 {
		t.Fatalf("two categories: %d, %v", total, err)
	}
	_, total, _ = st.ListMessages(ctx, Filter{Category: "营销推广,推销骚扰", Since: now.Add(-time.Hour)}, 50, 0)
	if total != 2 {
		t.Fatalf("two categories since an hour ago: %d", total)
	}
}

func TestDatabaseIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: mode %v, %v; want 0600", suffix, fi.Mode().Perm(), err)
		}
	}
}

// Forged parts (numbered outside the message, or a second text for a part
// already received) do not complete or silently replace a message.
func TestAssembleIgnoresForgedParts(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	for _, seg := range []Segment{
		segment("a", "95588", 9, 3, 1, "您转账", now),
		segment("f1", "95588", 9, 3, 0, "伪造", now),
		segment("f2", "95588", 9, 3, 200, "伪造", now),
	} {
		if _, err := s.SaveSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	if created, err := s.AssemblePending(ctx, now, time.Hour, nil); err != nil || len(created) != 0 {
		t.Fatalf("out-of-range parts completed a message: %+v %v", created, err)
	}
	for _, seg := range []Segment{
		segment("b", "95588", 9, 3, 2, "给张三", now),
		segment("b2", "95588", 9, 3, 2, "给骗子账户", now),
		segment("c", "95588", 9, 3, 3, "成功", now),
	} {
		if _, err := s.SaveSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	created, err := s.AssemblePending(ctx, now.Add(2*time.Hour), time.Hour, nil)
	if err != nil || len(created) != 1 {
		t.Fatalf("assembled %+v %v", created, err)
	}
	if got := created[0]; got.Complete || got.Body != "您转账给张三"+conflictingPart+"成功" {
		t.Fatalf("message = %+v", got)
	}
}
