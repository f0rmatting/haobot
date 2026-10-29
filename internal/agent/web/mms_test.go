package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

func TestMMSPartsServedSafely(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	ctx, now := context.Background(), time.Now()
	e.store.SaveSegment(ctx, store.Segment{PDUHash: "mms", PDU: "p", Sender: "10655", SentAt: now, ReceivedAt: now,
		Total: 1, Seq: 1, Body: "00", Binary: true, Port: 2948, DeviceID: "866000000000001"})
	created, err := e.store.AssemblePending(ctx, now, time.Hour, func(m *store.Message) {
		m.Kind, m.Binary, m.MMSURL = store.KindMMS, false, "http://mmsc.example/1"
	})
	if err != nil || len(created) != 1 {
		t.Fatal(created, err)
	}
	id := created[0].ID
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if err := e.store.SaveMMSContent(ctx, id, "", []store.MMSPart{
		{ContentType: "text/plain", Data: []byte("你好")},
		{ContentType: "image/png", Name: "a.png", Data: png},
		// Declared an image, but is a page a browser would run.
		{ContentType: "image/jpeg", Name: `..\evil".html`, Data: []byte("<html><script>alert(1)</script>")},
	}); err != nil {
		t.Fatal(err)
	}

	var msg messageJSON
	if code := e.call(t, "GET", "/api/messages/"+itoa(id), nil, &msg); code != http.StatusOK {
		t.Fatal(code)
	}
	if msg.MMSState != store.MMSDownloaded || len(msg.MMSParts) != 3 || msg.MMSParts[0].Text != "你好" {
		t.Fatalf("message %+v", msg)
	}

	resp, err := e.client.Get(e.ts.URL + msg.MMSParts[1].URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" || resp.Header.Get("Content-Disposition") != "" {
		t.Fatalf("image served as %q %q", ct, resp.Header.Get("Content-Disposition"))
	}
	resp, err = e.client.Get(e.ts.URL + msg.MMSParts[2].URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	disposition := resp.Header.Get("Content-Disposition")
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" || !strings.HasPrefix(disposition, "attachment") || strings.ContainsAny(strings.TrimPrefix(disposition, "attachment; filename="), `\/`) {
		t.Fatalf("html served as %q %q", ct, disposition)
	}

	if code, _ := e.get(t, "/api/messages/"+itoa(id+100)+"/mms/"+itoa(msg.MMSParts[1].ID)); code != http.StatusNotFound {
		t.Fatalf("part under another message: %d", code)
	}
	// The device is not connected.
	if code := e.call(t, "POST", "/api/messages/"+itoa(id)+"/mms/download", nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("download while offline: %d", code)
	}
}
