package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"strings"
)

// MMSPart is one part of a downloaded MMS. Lists carry Text for text parts
// and leave Data empty; MMSPart loads Data.
type MMSPart struct {
	ID          int64
	MessageID   int64
	ContentType string
	Name        string
	Size        int
	Text        string
	Data        []byte
}

// IsText reports a part shown as text: plain text, not SMIL layout.
func (p MMSPart) IsText() bool { return p.ContentType == "text/plain" }

// interruptDownloads marks downloads cut short by a restart as failed.
func interruptDownloads(db *sql.DB) error {
	_, err := db.Exec(`UPDATE messages SET mms_state = ?, mms_error = '下载被中断（程序重启）' WHERE mms_state = ?`, MMSFailed, MMSDownloading)
	return err
}

// StartMMSDownload marks an MMS as downloading. It fails with ErrNotFound
// for a message that is not an MMS or is downloading already.
func (s *Store) StartMMSDownload(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET mms_state = ?, mms_error = ''
		WHERE id = ? AND kind = ? AND mms_state <> ?`, MMSDownloading, id, KindMMS, MMSDownloading)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return cmp.Or(err, ErrNotFound)
	}
	return nil
}

// FailMMSDownload records why a download did not work.
func (s *Store) FailMMSDownload(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET mms_state = ?, mms_error = ? WHERE id = ?`, MMSFailed, reason, id)
	return err
}

// SaveMMSContent replaces the stored parts of an MMS and marks it
// downloaded. An empty subject keeps the one from the notification.
func (s *Store) SaveMMSContent(ctx context.Context, id int64, subject string, parts []MMSPart) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mms_parts WHERE message_id = ?`, id); err != nil {
		return err
	}
	for i, p := range parts {
		data := p.Data
		if data == nil {
			data = []byte(p.Text)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mms_parts (message_id, seq, content_type, name, data) VALUES (?, ?, ?, ?, ?)`,
			id, i, p.ContentType, p.Name, data); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE messages SET mms_state = ?, mms_error = '',
		body = CASE WHEN ? = '' THEN body ELSE ? END WHERE id = ?`, MMSDownloaded, subject, subject, id)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return cmp.Or(err, ErrNotFound)
	}
	return tx.Commit()
}

// MMSParts lists the parts of an MMS in order, with text inline.
func (s *Store) MMSParts(ctx context.Context, id int64) ([]MMSPart, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, content_type, name, length(data),
		CASE WHEN content_type = 'text/plain' THEN CAST(data AS TEXT) ELSE '' END
		FROM mms_parts WHERE message_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MMSPart
	for rows.Next() {
		p := MMSPart{MessageID: id}
		if err := rows.Scan(&p.ID, &p.ContentType, &p.Name, &p.Size, &p.Text); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MMSText joins the text parts of an MMS.
func (s *Store) MMSText(ctx context.Context, id int64) (string, error) {
	parts, err := s.MMSParts(ctx, id)
	if err != nil {
		return "", err
	}
	var texts []string
	for _, p := range parts {
		if t := strings.TrimSpace(p.Text); p.IsText() && t != "" {
			texts = append(texts, t)
		}
	}
	return strings.Join(texts, "\n"), nil
}

// MMSPart loads one part with its data.
func (s *Store) MMSPart(ctx context.Context, messageID, partID int64) (MMSPart, error) {
	p := MMSPart{ID: partID, MessageID: messageID}
	err := s.db.QueryRowContext(ctx, `SELECT content_type, name, data FROM mms_parts WHERE id = ? AND message_id = ?`, partID, messageID).
		Scan(&p.ContentType, &p.Name, &p.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Size = len(p.Data)
	return p, err
}
