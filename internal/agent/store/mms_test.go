package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// addMMS stores an MMS notification and returns it.
func addMMS(t *testing.T, s *Store) Message {
	t.Helper()
	ctx, now := context.Background(), time.Now()
	seg := segment("mms1", "10655", 0, 1, 1, "00", now)
	seg.Binary, seg.Port = true, 2948
	if _, err := s.SaveSegment(ctx, seg); err != nil {
		t.Fatal(err)
	}
	created, err := s.AssemblePending(ctx, now, time.Hour, func(m *Message) {
		m.Kind, m.Binary, m.Body, m.MMSURL = KindMMS, false, "主题", "http://mmsc.example/1"
	})
	if err != nil || len(created) != 1 {
		t.Fatalf("created %v, %v", created, err)
	}
	return created[0]
}

func TestMMSDownloadLifecycle(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	m := addMMS(t, s)
	if err := s.StartMMSDownload(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.StartMMSDownload(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second start: %v", err)
	}
	parts := []MMSPart{
		{ContentType: "application/smil", Data: []byte("<smil/>")},
		{ContentType: "text/plain", Name: "a.txt", Data: []byte("你好")},
		{ContentType: "image/jpeg", Name: "p.jpg", Data: []byte{0xFF, 0xD8, 0xFF}},
	}
	if err := s.SaveMMSContent(ctx, m.ID, "", parts); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, m.ID)
	if err != nil || got.MMSState != MMSDownloaded || got.Body != "主题" {
		t.Fatalf("message %+v, %v", got, err)
	}
	listed, err := s.MMSParts(ctx, m.ID)
	if err != nil || len(listed) != 3 {
		t.Fatalf("parts %+v, %v", listed, err)
	}
	if listed[1].Text != "你好" || listed[2].Text != "" || listed[2].Size != 3 || listed[2].Data != nil {
		t.Fatalf("parts %+v", listed)
	}
	if text, _ := s.MMSText(ctx, m.ID); text != "你好" {
		t.Fatalf("text %q", text)
	}
	image, err := s.MMSPart(ctx, m.ID, listed[2].ID)
	if err != nil || len(image.Data) != 3 || image.ContentType != "image/jpeg" {
		t.Fatalf("image %+v, %v", image, err)
	}
	if _, err := s.MMSPart(ctx, m.ID+1, listed[2].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("part of another message: %v", err)
	}

	if _, err := s.DeleteMessages(ctx, []int64{m.ID}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM mms_parts`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d parts left after deleting the message", n)
	}
}

func TestMMSDownloadFailureAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m := addMMS(t, s)
	if err := s.FailMMSDownload(ctx, m.ID, "HTTP 404"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMessage(ctx, m.ID); got.MMSState != MMSFailed || got.MMSError != "HTTP 404" {
		t.Fatalf("message %+v", got)
	}
	if err := s.StartMMSDownload(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, _ := s.GetMessage(ctx, m.ID); got.MMSState != MMSFailed || got.MMSError == "" {
		t.Fatalf("download left running after restart: %+v", got)
	}
}

func TestStartMMSDownloadRefusesSMS(t *testing.T) {
	s, ctx, now := openTest(t), context.Background(), time.Now()
	s.SaveSegment(ctx, segment("t1", "10010", 0, 1, 1, "hi", now))
	created, _ := s.AssemblePending(ctx, now, time.Hour, nil)
	if err := s.StartMMSDownload(ctx, created[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
