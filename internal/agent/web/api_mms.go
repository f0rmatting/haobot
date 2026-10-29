package web

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

type mmsPart struct {
	ID          int64  `json:"id"`
	ContentType string `json:"contentType"`
	Name        string `json:"name,omitempty"`
	Size        int    `json:"size"`
	Text        string `json:"text,omitempty"`
	URL         string `json:"url"`
}

// mmsParts lists a downloaded MMS's parts; an error leaves them out rather
// than failing the whole list.
func (s *Server) mmsParts(ctx context.Context, id int64) []mmsPart {
	parts, err := s.Store.MMSParts(ctx, id)
	if err != nil {
		s.Log.Warn("list MMS parts", "id", id, "error", err)
		return nil
	}
	out := make([]mmsPart, 0, len(parts))
	for _, p := range parts {
		j := mmsPart{ID: p.ID, ContentType: p.ContentType, Name: p.Name, Size: p.Size,
			URL: "/api/messages/" + strconv.FormatInt(id, 10) + "/mms/" + strconv.FormatInt(p.ID, 10)}
		if p.IsText() {
			j.Text = p.Text
		}
		out = append(out, j)
	}
	return out
}

func (s *Server) downloadMMS(w http.ResponseWriter, r *http.Request) {
	m, found := s.loadMessage(w, r)
	if !found {
		return
	}
	if m.Kind != store.KindMMS {
		writeError(w, http.StatusBadRequest, "这不是彩信")
		return
	}
	svc := s.Devices.Device(m.DeviceID)
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, "收到这条彩信的设备未连接，无法下载")
		return
	}
	if err := svc.DownloadMMS(r.Context(), m); err != nil {
		s.opError(w, err)
		return
	}
	m.MMSState, m.MMSError = store.MMSDownloading, ""
	writeJSON(w, http.StatusOK, s.toMessageJSON(r.Context(), m))
}

// inlineTypes are the part types a browser may show; content comes from
// whoever sent the MMS, so everything else is only offered as a download.
var inlineTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/bmp": true}

func (s *Server) mmsPartData(w http.ResponseWriter, r *http.Request) {
	id, valid := s.messageID(w, r)
	if !valid {
		return
	}
	partID, err := strconv.ParseInt(r.PathValue("part"), 10, 64)
	if err != nil || partID <= 0 {
		writeError(w, http.StatusBadRequest, "无效的彩信附件")
		return
	}
	p, err := s.Store.MMSPart(r.Context(), id, partID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "彩信附件不存在")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	// The type is what the bytes are, not what the sender declared.
	sniffed, _, _ := strings.Cut(http.DetectContentType(p.Data), ";")
	switch {
	case inlineTypes[sniffed]:
		h.Set("Content-Type", sniffed)
	case p.IsText():
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": partFilename(p, ".txt")}))
	default:
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": partFilename(p, "")}))
	}
	w.Write(p.Data)
}

func partFilename(p store.MMSPart, ext string) string {
	name := path.Base(strings.ReplaceAll(p.Name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || strings.ContainsRune(`"/\:*?<>|`, r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		name = "mms-" + strconv.FormatInt(p.ID, 10)
	}
	if ext != "" && path.Ext(name) == "" {
		name += ext
	}
	return name
}
