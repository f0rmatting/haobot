package store

import (
	"context"
	"time"
)

// maxClassifyLog bounds the classification log; older rows are pruned.
const maxClassifyLog = 2000

// ClassifyLog records one classification attempt: which rule decided, or
// exactly what was sent to the model and what it answered.
type ClassifyLog struct {
	ID         int64
	MessageID  int64
	At         time.Time
	Source     string // rule, llm or manual
	Category   string // empty when the attempt failed
	Rule       string // why a rule decided
	Provider   string
	Model      string
	Endpoint   string
	System     string // system prompt sent to the model
	Prompt     string // redacted message sent to the model
	Reply      string // raw model answer
	Confidence float64
	LatencyMs  int64
	Error      string

	// Filled by RecentClassifyLogs: the message as it is now, if it still
	// exists.
	Sender string
	Body   string
}

func (s *Store) AddClassifyLog(ctx context.Context, l ClassifyLog) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO classify_log (message_id, at, source, category, rule, provider, model, endpoint, system, prompt, reply, confidence, latency_ms, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.MessageID, l.At.Unix(), l.Source, l.Category, l.Rule, l.Provider, l.Model, l.Endpoint, l.System, l.Prompt, l.Reply,
		l.Confidence, l.LatencyMs, l.Error); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM classify_log WHERE id <= (SELECT MAX(id) FROM classify_log) - ?`, maxClassifyLog)
	return err
}

const classifyLogColumns = `l.id, l.message_id, l.at, l.source, l.category, l.rule, l.provider, l.model, l.endpoint, l.system, l.prompt, l.reply,
	l.confidence, l.latency_ms, l.error, COALESCE(m.sender, ''), COALESCE(m.body, '')`

func (s *Store) queryClassifyLogs(ctx context.Context, where string, args ...any) ([]ClassifyLog, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+classifyLogColumns+` FROM classify_log l LEFT JOIN messages m ON m.id = l.message_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClassifyLog
	for rows.Next() {
		var l ClassifyLog
		var at int64
		if err := rows.Scan(&l.ID, &l.MessageID, &at, &l.Source, &l.Category, &l.Rule, &l.Provider, &l.Model, &l.Endpoint,
			&l.System, &l.Prompt, &l.Reply, &l.Confidence, &l.LatencyMs, &l.Error, &l.Sender, &l.Body); err != nil {
			return nil, err
		}
		l.At = time.Unix(at, 0)
		out = append(out, l)
	}
	return out, rows.Err()
}

// MessageClassifyLogs returns every attempt for one message, newest first.
func (s *Store) MessageClassifyLogs(ctx context.Context, messageID int64) ([]ClassifyLog, error) {
	return s.queryClassifyLogs(ctx, `WHERE l.message_id = ? ORDER BY l.id DESC`, messageID)
}

// RecentClassifyLogs pages through the log, newest first. source filters by
// rule/llm/manual; failed selects attempts with an error.
func (s *Store) RecentClassifyLogs(ctx context.Context, source string, failed bool, limit, offset int) ([]ClassifyLog, int, error) {
	where, args := "WHERE 1 = 1", []any{}
	if source != "" {
		where += " AND l.source = ?"
		args = append(args, source)
	}
	if failed {
		where += " AND l.error <> ''"
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM classify_log l `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	logs, err := s.queryClassifyLogs(ctx, where+` ORDER BY l.id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	return logs, total, err
}
