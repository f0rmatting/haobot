package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Call outcomes. Ringing, dialing and active describe calls in progress.
const (
	CallRinging   = "ringing"   // incoming, not answered yet
	CallDialing   = "dialing"   // outgoing, not answered yet
	CallActive    = "active"    // answered, in progress
	CallCompleted = "completed" // answered and ended
	CallMissed    = "missed"    // incoming, ended unanswered
	CallRejected  = "rejected"  // incoming, hung up here unanswered
	CallCanceled  = "canceled"  // outgoing, ended before an answer
)

// CallRecord is one entry of the call log.
type CallRecord struct {
	ID         int64
	DeviceID   string
	Peer       string // normalised, see NormalizePeer
	Number     string // as the network reported it
	Direction  string // DirectionIn or DirectionOut
	Status     string
	StartedAt  time.Time
	AnsweredAt time.Time
	EndedAt    time.Time
	// Seen is false for missed calls nobody has looked at yet.
	Seen bool
	// HandledBy is HandledByAssistant when the AI assistant took the call.
	HandledBy string
	// Transcript and Summary are the assistant's notes, as JSON.
	Transcript string
	Summary    string
	// Recording is the WAV file's path below the recordings directory.
	Recording string
}

// ListCalls kinds: every call when empty.
const (
	CallsMissed    = "missed"
	CallsAssistant = "assistant"
)

// HandledByAssistant marks calls the AI assistant answered.
const HandledByAssistant = "assistant"

const callColumns = `id, device_id, peer, number, direction, status, started_at, answered_at, ended_at, seen, handled_by, transcript, summary, recording`

func scanCall(row scanner) (CallRecord, error) {
	var c CallRecord
	var started, answered, ended int64
	err := row.Scan(&c.ID, &c.DeviceID, &c.Peer, &c.Number, &c.Direction, &c.Status, &started, &answered, &ended, &c.Seen, &c.HandledBy, &c.Transcript, &c.Summary, &c.Recording)
	c.StartedAt = time.Unix(started, 0)
	if answered != 0 {
		c.AnsweredAt = time.Unix(answered, 0)
	}
	if ended != 0 {
		c.EndedAt = time.Unix(ended, 0)
	}
	return c, err
}

// StartCall records a call that just appeared.
func (s *Store) StartCall(ctx context.Context, c CallRecord) (CallRecord, error) {
	c.Peer = NormalizePeer(c.Number)
	c.Seen = true
	result, err := s.db.ExecContext(ctx, `INSERT INTO calls (device_id, peer, number, direction, status, started_at, seen) VALUES (?, ?, ?, ?, ?, ?, 1)`,
		c.DeviceID, c.Peer, c.Number, c.Direction, c.Status, c.StartedAt.Unix())
	if err != nil {
		return c, err
	}
	c.ID, err = result.LastInsertId()
	return c, err
}

// AnswerCall marks a call as answered.
func (s *Store) AnswerCall(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET status = ?, answered_at = ? WHERE id = ? AND answered_at = 0`, CallActive, at.Unix(), id)
	return err
}

// EndCall records how a call ended. Missed calls, and calls with an
// assistant note, stay unseen until looked at.
func (s *Store) EndCall(ctx context.Context, id int64, status string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET status = ?, ended_at = ?, seen = (? AND summary = '') WHERE id = ?`, status, at.Unix(), status != CallMissed, id)
	return err
}

// SetCallHandler records who answered a call.
func (s *Store) SetCallHandler(ctx context.Context, id int64, handledBy string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET handled_by = ? WHERE id = ?`, handledBy, id)
	return err
}

// SaveCallNote stores the assistant's transcript and summary. The call
// becomes unseen so the owner notices the note.
func (s *Store) SaveCallNote(ctx context.Context, id int64, transcript, summary string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET transcript = ?, summary = ?, seen = 0 WHERE id = ?`, transcript, summary, id)
	return err
}

// SetCallRecording links a recording file to a call.
func (s *Store) SetCallRecording(ctx context.Context, id int64, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET recording = ? WHERE id = ?`, path, id)
	return err
}

// CallRecordings returns the recording files of calls.
func (s *Store) CallRecordings(ctx context.Context, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	in, args := inList(ids)
	return s.recordings(ctx, `SELECT recording FROM calls WHERE recording <> '' AND id IN `+in, args...)
}

// ExpireRecordings unlinks recordings of calls started before a time and
// returns their files, for the caller to delete.
func (s *Store) ExpireRecordings(ctx context.Context, before time.Time) ([]string, error) {
	paths, err := s.recordings(ctx, `SELECT recording FROM calls WHERE recording <> '' AND started_at < ?`, before.Unix())
	if err != nil || len(paths) == 0 {
		return paths, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE calls SET recording = '' WHERE recording <> '' AND started_at < ?`, before.Unix())
	return paths, err
}

func (s *Store) recordings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountAssistantCalls counts calls the assistant answered since a time, in
// total and from one number.
func (s *Store) CountAssistantCalls(ctx context.Context, number string, since time.Time) (total, fromPeer int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(peer = ?), 0) FROM calls WHERE handled_by = ? AND started_at >= ?`,
		NormalizePeer(number), HandledByAssistant, since.Unix()).Scan(&total, &fromPeer)
	return total, fromPeer, err
}

// EndOpenCalls closes calls left in progress, e.g. by a restart.
func (s *Store) EndOpenCalls(ctx context.Context, deviceID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE calls SET ended_at = ?, status = CASE
			WHEN answered_at <> 0 THEN 'completed'
			WHEN direction = 'in' THEN 'missed'
			ELSE 'canceled' END
		WHERE device_id = ? AND ended_at = 0`, at.Unix(), deviceID)
	return err
}

// ListCalls returns the call log newest first; missed selects unanswered
// incoming calls.
func (s *Store) ListCalls(ctx context.Context, deviceID, kind string, limit, offset int) ([]CallRecord, int, error) {
	where, args := "WHERE 1 = 1", []any{}
	if deviceID != "" {
		where += " AND device_id = ?"
		args = append(args, deviceID)
	}
	switch kind {
	case CallsMissed:
		where += " AND status = 'missed'"
	case CallsAssistant:
		where += " AND handled_by = ?"
		args = append(args, HandledByAssistant)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calls `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+callColumns+` FROM calls `+where+` ORDER BY started_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []CallRecord
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// GetCall returns one call record.
func (s *Store) GetCall(ctx context.Context, id int64) (CallRecord, error) {
	c, err := scanCall(s.db.QueryRowContext(ctx, `SELECT `+callColumns+` FROM calls WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// UnseenMissedCalls counts missed calls not looked at yet.
func (s *Store) UnseenMissedCalls(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calls WHERE seen = 0`).Scan(&n)
	return n, err
}

// MarkCallsSeen clears the missed-call badge.
func (s *Store) MarkCallsSeen(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET seen = 1 WHERE seen = 0`)
	return err
}

// DeleteCalls removes call log entries.
func (s *Store) DeleteCalls(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	in, args := inList(ids)
	result, err := s.db.ExecContext(ctx, `DELETE FROM calls WHERE id IN `+in, args...)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

// CallbackCandidates are incoming calls since a time that may still need
// a call back: missed or rejected ones, and ones the assistant took. A call
// is left out once it is marked done or the number was called afterwards;
// only the latest call per number is returned, newest first.
func (s *Store) CallbackCandidates(ctx context.Context, since time.Time) ([]CallRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+callColumns+` FROM calls c
		WHERE direction = 'in' AND peer <> '' AND started_at >= ? AND callback_done = 0
			AND (handled_by = ? OR status IN (?, ?))
			AND NOT EXISTS (SELECT 1 FROM calls o WHERE o.peer = c.peer AND o.started_at > c.started_at
				AND (o.direction = 'out' OR o.status = ? AND o.handled_by = ''))
		ORDER BY started_at DESC, id DESC`,
		since.Unix(), HandledByAssistant, CallMissed, CallRejected, CallCompleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []CallRecord
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		if seen[c.Peer] {
			continue
		}
		seen[c.Peer] = true
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkCallbackDone clears the call back reminder for a call and the earlier
// calls from the same number.
func (s *Store) MarkCallbackDone(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE calls SET callback_done = 1
		WHERE peer = (SELECT peer FROM calls WHERE id = ?) AND started_at <= (SELECT started_at FROM calls WHERE id = ?)`, id, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
