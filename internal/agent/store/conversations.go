package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// NormalizePeer turns the many spellings of a number into one conversation
// key: Chinese mobile numbers lose their +86/0086/86 prefix and separators
// are dropped. Short codes and alphanumeric senders are kept as they are.
func NormalizePeer(number string) string {
	n := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')', ' ':
			return -1
		}
		return r
	}, strings.TrimSpace(number))
	for _, prefix := range []string{"+86", "0086", "86"} {
		rest, ok := strings.CutPrefix(n, prefix)
		if ok && isMobile(rest) {
			return rest
		}
	}
	return strings.ToUpper(n)
}

func isMobile(n string) bool {
	if len(n) != 11 || n[0] != '1' || n[1] < '3' || n[1] > '9' {
		return false
	}
	return strings.Trim(n, "0123456789") == ""
}

// fillPeers computes conversation keys for messages stored before they
// existed.
func fillPeers(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, sender FROM messages WHERE peer = ''`)
	if err != nil {
		return err
	}
	type row struct {
		id     int64
		sender string
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sender); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range todo {
		if _, err := db.Exec(`UPDATE messages SET peer = ? WHERE id = ?`, NormalizePeer(r.sender), r.id); err != nil {
			return err
		}
	}
	return nil
}

// Conversation summarises the messages exchanged with one number through
// one device.
type Conversation struct {
	DeviceID string
	Peer     string
	Total    int
	Unread   int
	LastAt   time.Time
	// Last is the newest message, whatever the filter matched.
	Last Message
}

// ListConversations returns conversations newest first. A conversation is
// included when any of its messages match f; f.Unread selects conversations
// with unread messages.
func (s *Store) ListConversations(ctx context.Context, f Filter, limit, offset int) ([]Conversation, int, error) {
	unread := f.Unread
	f.Unread = false
	where, args := f.where()
	having := ""
	if unread {
		having = "HAVING unread > 0"
	}
	grouped := `SELECT device_id, peer, COUNT(*) AS total, COALESCE(SUM(direction = 'in' AND read = 0), 0) AS unread, MAX(sent_at) AS last_at
		FROM messages ` + where + ` GROUP BY device_id, peer ` + having
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+grouped+`)`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	// The counts above follow the filter; the badge should not, so unread is
	// recomputed over the whole conversation.
	rows, err := s.db.QueryContext(ctx, `
		WITH conv AS (`+grouped+`)
		SELECT conv.device_id, conv.peer,
			(SELECT COUNT(*) FROM messages x WHERE x.device_id = conv.device_id AND x.peer = conv.peer),
			(SELECT COUNT(*) FROM messages x WHERE x.device_id = conv.device_id AND x.peer = conv.peer AND x.direction = 'in' AND x.read = 0),
			conv.last_at,
			(SELECT id FROM messages x WHERE x.device_id = conv.device_id AND x.peer = conv.peer ORDER BY x.sent_at DESC, x.id DESC LIMIT 1)
		FROM conv ORDER BY conv.last_at DESC, conv.peer LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	var out []Conversation
	var lastIDs []int64
	for rows.Next() {
		var c Conversation
		var lastAt, lastID int64
		if err := rows.Scan(&c.DeviceID, &c.Peer, &c.Total, &c.Unread, &lastAt, &lastID); err != nil {
			rows.Close()
			return nil, 0, err
		}
		c.LastAt = time.Unix(lastAt, 0)
		out = append(out, c)
		lastIDs = append(lastIDs, lastID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i, id := range lastIDs {
		m, err := s.GetMessage(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		out[i].Last = m
	}
	return out, total, nil
}

// ConversationMessages returns one conversation newest first.
func (s *Store) ConversationMessages(ctx context.Context, deviceID, peer string, limit, offset int) ([]Message, int, error) {
	return s.ListMessages(ctx, Filter{DeviceID: deviceID, Peer: peer}, limit, offset)
}

// MarkConversationRead marks every incoming message of a conversation read.
func (s *Store) MarkConversationRead(ctx context.Context, deviceID, peer string) (int, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET read = 1 WHERE device_id = ? AND peer = ? AND direction = 'in' AND read = 0`, deviceID, peer)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

// DeleteConversation removes a conversation and its segments.
func (s *Store) DeleteConversation(ctx context.Context, deviceID, peer string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM segments WHERE message_id IN (SELECT id FROM messages WHERE device_id = ? AND peer = ?)`, deviceID, peer); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE device_id = ? AND peer = ?`, deviceID, peer)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// AddOutgoing stores a message about to be sent and returns it with its id.
func (s *Store) AddOutgoing(ctx context.Context, deviceID, to, body string, parts int, at time.Time) (Message, error) {
	m := Message{
		Sender: to, Body: body, SentAt: at, ReceivedAt: at, Parts: parts, Complete: true, Kind: KindSMS,
		Read: true, DeviceID: deviceID, Peer: NormalizePeer(to), Direction: DirectionOut, Status: StatusSending,
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (sender, body, sent_at, received_at, parts, complete, binary, kind, read, device_id, peer, direction, status)
		VALUES (?, ?, ?, ?, ?, 1, 0, 'sms', 1, ?, ?, 'out', ?)`,
		m.Sender, m.Body, at.Unix(), at.Unix(), parts, deviceID, m.Peer, StatusSending)
	if err != nil {
		return m, err
	}
	m.ID, err = result.LastInsertId()
	return m, err
}

// SetOutgoingStatus records the outcome of sending.
func (s *Store) SetOutgoingStatus(ctx context.Context, id int64, status, errText string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET status = ?, error = ? WHERE id = ? AND direction = 'out'`, status, errText, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FailStaleSending marks messages left "sending" by a previous run failed.
func (s *Store) FailStaleSending(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'failed', error = '程序重启，发送结果未知' WHERE direction = 'out' AND status = 'sending'`)
	return err
}

// Device is a modem seen by this agent, identified by its IMEI.
type Device struct {
	ID        string
	Name      string
	Model     string
	IMEI      string
	Endpoint  string
	Enabled   bool
	FirstSeen time.Time
	LastSeen  time.Time
}

const deviceColumns = `id, name, model, imei, endpoint, enabled, first_seen, last_seen`

func scanDevice(row scanner) (Device, error) {
	var d Device
	var first, last int64
	err := row.Scan(&d.ID, &d.Name, &d.Model, &d.IMEI, &d.Endpoint, &d.Enabled, &first, &last)
	d.FirstSeen, d.LastSeen = time.Unix(first, 0), time.Unix(last, 0)
	return d, err
}

// SeeDevice records that a device is connected, creating it with a default
// name the first time.
func (s *Store) SeeDevice(ctx context.Context, d Device, now time.Time) (Device, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO devices (id, name, model, imei, endpoint, enabled, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET model = excluded.model, imei = excluded.imei, endpoint = excluded.endpoint, last_seen = excluded.last_seen`,
		d.ID, d.Name, d.Model, d.IMEI, d.Endpoint, now.Unix(), now.Unix()); err != nil {
		return d, err
	}
	out, err := scanDevice(tx.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, d.ID))
	if err != nil {
		return d, err
	}
	return out, tx.Commit()
}

// Devices lists every known device, oldest first.
func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices ORDER BY first_seen, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDevice returns one device.
func (s *Store) GetDevice(ctx context.Context, id string) (Device, error) {
	d, err := scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// UpdateDevice renames or enables/disables a device.
func (s *Store) UpdateDevice(ctx context.Context, id string, name *string, enabled *bool) error {
	if name != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE devices SET name = ? WHERE id = ?`, *name, id); err != nil {
			return err
		}
	}
	if enabled != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE devices SET enabled = ? WHERE id = ?`, *enabled, id); err != nil {
			return err
		}
	}
	_, err := s.GetDevice(ctx, id)
	return err
}

// ForgetDevice removes a device record; its messages are kept.
func (s *Store) ForgetDevice(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	return err
}

// Orphans counts messages stored before devices were tracked.
func (s *Store) Orphans(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE device_id = ''`).Scan(&n)
	return n, err
}

// AdoptOrphans attributes messages stored before devices were tracked to a
// device. It returns how many messages moved.
func (s *Store) AdoptOrphans(ctx context.Context, deviceID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE messages SET device_id = ? WHERE device_id = ''`, deviceID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE segments SET device_id = ? WHERE device_id = ''`, deviceID); err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
