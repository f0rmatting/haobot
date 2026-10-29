// Package store keeps received messages, settings, web accounts and sessions in a local
// SQLite database.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a looked-up row does not exist.
var ErrNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS segments (
	id          INTEGER PRIMARY KEY,
	pdu_hash    TEXT    NOT NULL UNIQUE,
	pdu         TEXT    NOT NULL,
	sender      TEXT    NOT NULL,
	sent_at     INTEGER NOT NULL,
	received_at INTEGER NOT NULL,
	ref         INTEGER NOT NULL,
	total       INTEGER NOT NULL,
	seq         INTEGER NOT NULL,
	body        TEXT    NOT NULL,
	binary      INTEGER NOT NULL DEFAULT 0,
	port        INTEGER NOT NULL DEFAULT 0,
	device_id   TEXT    NOT NULL DEFAULT '',
	message_id  INTEGER REFERENCES messages(id)
);
CREATE INDEX IF NOT EXISTS segments_pending ON segments(message_id, sender, ref, total);

CREATE TABLE IF NOT EXISTS messages (
	id          INTEGER PRIMARY KEY,
	sender      TEXT    NOT NULL,
	body        TEXT    NOT NULL,
	sent_at     INTEGER NOT NULL,
	received_at INTEGER NOT NULL,
	parts       INTEGER NOT NULL,
	complete    INTEGER NOT NULL,
	binary      INTEGER NOT NULL DEFAULT 0,
	kind        TEXT    NOT NULL DEFAULT 'sms',
	mms_url     TEXT    NOT NULL DEFAULT '',
	mms_size    INTEGER NOT NULL DEFAULT 0,
	mms_expiry  INTEGER NOT NULL DEFAULT 0,
	mms_state   TEXT    NOT NULL DEFAULT '',
	mms_error   TEXT    NOT NULL DEFAULT '',
	category        TEXT    NOT NULL DEFAULT '',
	category_source TEXT    NOT NULL DEFAULT '',
	classified_at   INTEGER NOT NULL DEFAULT 0,
	read            INTEGER NOT NULL DEFAULT 0,
	device_id       TEXT    NOT NULL DEFAULT '',
	peer            TEXT    NOT NULL DEFAULT '',
	direction       TEXT    NOT NULL DEFAULT 'in',
	status          TEXT    NOT NULL DEFAULT '',
	error           TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS messages_sent_at ON messages(sent_at DESC);
CREATE INDEX IF NOT EXISTS messages_category ON messages(category);
CREATE INDEX IF NOT EXISTS messages_conversation ON messages(device_id, peer, sent_at);

-- mms_parts hold the content of downloaded MMS.
CREATE TABLE IF NOT EXISTS mms_parts (
	id           INTEGER PRIMARY KEY,
	message_id   INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	seq          INTEGER NOT NULL,
	content_type TEXT    NOT NULL,
	name         TEXT    NOT NULL DEFAULT '',
	data         BLOB    NOT NULL
);
CREATE INDEX IF NOT EXISTS mms_parts_message ON mms_parts(message_id, seq);

CREATE TABLE IF NOT EXISTS classify_log (
	id          INTEGER PRIMARY KEY,
	message_id  INTEGER NOT NULL,
	at          INTEGER NOT NULL,
	source      TEXT    NOT NULL,
	category    TEXT    NOT NULL DEFAULT '',
	rule        TEXT    NOT NULL DEFAULT '',
	provider    TEXT    NOT NULL DEFAULT '',
	model       TEXT    NOT NULL DEFAULT '',
	endpoint    TEXT    NOT NULL DEFAULT '',
	system      TEXT    NOT NULL DEFAULT '',
	prompt      TEXT    NOT NULL DEFAULT '',
	reply       TEXT    NOT NULL DEFAULT '',
	confidence  REAL    NOT NULL DEFAULT 0,
	latency_ms  INTEGER NOT NULL DEFAULT 0,
	error       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS classify_log_message ON classify_log(message_id);

CREATE TABLE IF NOT EXISTS calls (
	id          INTEGER PRIMARY KEY,
	device_id   TEXT    NOT NULL,
	peer        TEXT    NOT NULL,
	number      TEXT    NOT NULL,
	direction   TEXT    NOT NULL,
	status      TEXT    NOT NULL,
	started_at  INTEGER NOT NULL,
	answered_at INTEGER NOT NULL DEFAULT 0,
	ended_at    INTEGER NOT NULL DEFAULT 0,
	seen        INTEGER NOT NULL DEFAULT 1,
	handled_by  TEXT    NOT NULL DEFAULT '',
	transcript  TEXT    NOT NULL DEFAULT '',
	summary     TEXT    NOT NULL DEFAULT '',
	recording   TEXT    NOT NULL DEFAULT '',
	callback_done INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS calls_started ON calls(started_at DESC);

CREATE TABLE IF NOT EXISTS contacts (
	id         INTEGER PRIMARY KEY,
	name       TEXT    NOT NULL,
	number     TEXT    NOT NULL,
	key        TEXT    NOT NULL UNIQUE,
	note       TEXT    NOT NULL DEFAULT '',
	avatar     BLOB,
	updated_at INTEGER NOT NULL
);

-- yellow_orgs are the user's changes to the built-in yellow pages: an
-- edited or hidden built-in organisation (builtin is its name), or one of
-- their own (builtin is NULL).
CREATE TABLE IF NOT EXISTS yellow_orgs (
	id         INTEGER PRIMARY KEY,
	builtin    TEXT UNIQUE,
	name       TEXT    NOT NULL,
	category   TEXT    NOT NULL,
	numbers    TEXT    NOT NULL DEFAULT '[]',
	logo       BLOB,
	hidden     INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS devices (
	id         TEXT PRIMARY KEY,
	name       TEXT    NOT NULL,
	model      TEXT    NOT NULL DEFAULT '',
	imei       TEXT    NOT NULL DEFAULT '',
	endpoint   TEXT    NOT NULL DEFAULT '',
	enabled    INTEGER NOT NULL DEFAULT 1,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
	id    INTEGER PRIMARY KEY,
	at    INTEGER NOT NULL,
	level TEXT    NOT NULL,
	text  TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
	username      TEXT PRIMARY KEY,
	password_hash TEXT    NOT NULL,
	updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	username   TEXT    NOT NULL REFERENCES users(username) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
`

// maxEvents bounds the event log; older rows are pruned on insert.
const maxEvents = 10000

type Store struct {
	db *sql.DB
}

// Open creates or opens the database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps SQLite writes serialised without SQLITE_BUSY churn.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialise schema: %w", err)
	}
	if err := fillPeers(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("fill conversation keys: %w", err)
	}
	if err := interruptDownloads(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("reset MMS downloads: %w", err)
	}
	// The database holds every message. SQLite creates it, and the -wal and
	// -shm files after it, readable by all; the data directory may be a
	// shared one (a NAS volume) that does not keep others out.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

// addedColumns lists columns introduced after the first release, which
// migrate adds to older databases.
var addedColumns = []struct{ table, column, definition string }{
	{"segments", "binary", "INTEGER NOT NULL DEFAULT 0"},
	{"segments", "port", "INTEGER NOT NULL DEFAULT 0"},
	{"messages", "binary", "INTEGER NOT NULL DEFAULT 0"},
	{"messages", "kind", "TEXT NOT NULL DEFAULT 'sms'"},
	{"messages", "mms_url", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "mms_size", "INTEGER NOT NULL DEFAULT 0"},
	{"messages", "mms_expiry", "INTEGER NOT NULL DEFAULT 0"},
	{"messages", "mms_state", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "mms_error", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "category", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "category_source", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "classified_at", "INTEGER NOT NULL DEFAULT 0"},
	// Messages received before read tracking existed count as read.
	{"messages", "read", "INTEGER NOT NULL DEFAULT 1"},
	{"segments", "device_id", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "device_id", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "peer", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "direction", "TEXT NOT NULL DEFAULT 'in'"},
	{"messages", "status", "TEXT NOT NULL DEFAULT ''"},
	{"messages", "error", "TEXT NOT NULL DEFAULT ''"},
	{"calls", "handled_by", "TEXT NOT NULL DEFAULT ''"},
	{"calls", "transcript", "TEXT NOT NULL DEFAULT ''"},
	{"calls", "summary", "TEXT NOT NULL DEFAULT ''"},
	{"calls", "recording", "TEXT NOT NULL DEFAULT ''"},
	{"calls", "callback_done", "INTEGER NOT NULL DEFAULT 0"},
	{"contacts", "avatar", "BLOB"},
}

// migrate brings existing tables up to date, then creates anything missing.
func migrate(db *sql.DB) error {
	for _, c := range addedColumns {
		exists, err := tableExists(db, c.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		has, err := hasColumn(db, c.table, c.column)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec("ALTER TABLE " + c.table + " ADD COLUMN " + c.column + " " + c.definition); err != nil {
				return err
			}
		}
	}
	_, err := db.Exec(schema)
	return err
}

func tableExists(db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
	return n > 0, err
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	return n > 0, err
}

func (s *Store) Close() error { return s.db.Close() }

// Segment is a decoded PDU awaiting (or part of) a message.
type Segment struct {
	PDUHash    string
	PDU        string
	Sender     string
	SentAt     time.Time
	ReceivedAt time.Time
	Ref        int
	Total      int
	Seq        int
	Body       string
	Binary     bool
	Port       int
	DeviceID   string
	// LegacyHash, when set, is the hash this PDU had before hashes included
	// the device; a segment stored under it counts as a duplicate.
	LegacyHash string
}

// SaveSegment stores seg unless a segment with the same PDU hash exists.
// It reports whether a new row was written.
func (s *Store) SaveSegment(ctx context.Context, seg Segment) (bool, error) {
	if seg.LegacyHash != "" {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM segments WHERE pdu_hash = ?`, seg.LegacyHash).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO segments (pdu_hash, pdu, sender, sent_at, received_at, ref, total, seq, body, binary, port, device_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		seg.PDUHash, seg.PDU, seg.Sender, seg.SentAt.Unix(), seg.ReceivedAt.Unix(),
		seg.Ref, seg.Total, seg.Seq, seg.Body, seg.Binary, seg.Port, seg.DeviceID)
	if err != nil {
		return false, fmt.Errorf("save segment: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// Message is a fully (or, after a timeout, partially) assembled SMS.
type Message struct {
	ID         int64
	Sender     string
	Body       string
	SentAt     time.Time
	ReceivedAt time.Time
	Parts      int
	Complete   bool
	// Binary messages carry 8-bit data; Body holds it as upper-case hex.
	Binary bool
	// Port is the application port of the segments (not stored).
	Port int

	// Kind is KindSMS or KindMMS. For MMS notifications Body is the subject
	// and the MMS fields describe the content waiting on the MMSC.
	Kind      string
	MMSURL    string
	MMSSize   int64
	MMSExpiry time.Time
	// MMSState says whether the content has been fetched from the MMSC:
	// "", MMSDownloading, MMSDownloaded or MMSFailed (see MMSError).
	MMSState string
	MMSError string

	// Category is "" until the message has been classified; CategorySource
	// says how ("rule", "llm" or "manual").
	Category       string
	CategorySource string
	ClassifiedAt   time.Time
	Read           bool

	// DeviceID is the modem that received (or sent) the message; Peer is
	// the normalised other party. Together they identify a conversation.
	DeviceID  string
	Peer      string
	Direction string // DirectionIn or DirectionOut
	// Status and Error track outgoing messages.
	Status string
	Error  string
}

// Message directions and outgoing states.
const (
	DirectionIn  = "in"
	DirectionOut = "out"

	StatusSending = "sending"
	StatusSent    = "sent"
	StatusFailed  = "failed"
)

const (
	KindSMS = "sms"
	KindMMS = "mms"

	MMSDownloading = "downloading"
	MMSDownloaded  = "downloaded"
	MMSFailed      = "failed"
)

// Finalizer may rewrite a newly assembled message before it is stored, for
// example to turn a WAP Push into an MMS notification.
type Finalizer func(*Message)

type pendingSegment struct {
	id         int64
	deviceID   string
	sender     string
	sentAt     int64
	receivedAt int64
	ref        int
	total      int
	seq        int
	body       string
	binary     bool
	port       int
}

// AssemblePending turns unassigned segments into messages: single-part
// segments immediately, multipart groups once all parts are present or the
// oldest part has waited longer than staleAfter. finalize, if not nil, sees
// each message before it is stored. It returns the new messages.
func (s *Store) AssemblePending(ctx context.Context, now time.Time, staleAfter time.Duration, finalize Finalizer) ([]Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, device_id, sender, sent_at, received_at, ref, total, seq, body, binary, port
		FROM segments WHERE message_id IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type groupKey struct {
		device, sender string
		ref, total     int
		run            int
	}
	groups := make(map[groupKey][]pendingSegment)
	var order []groupKey
	// Text-numbered parts carry no reference, so two such messages from one
	// sender share a key; run tells them apart. A part joins the run sent
	// closest to it that has no other text for its number, or starts one.
	runs := make(map[groupKey]int)
	for rows.Next() {
		var p pendingSegment
		if err := rows.Scan(&p.id, &p.deviceID, &p.sender, &p.sentAt, &p.receivedAt, &p.ref, &p.total, &p.seq, &p.body, &p.binary, &p.port); err != nil {
			rows.Close()
			return nil, err
		}
		if p.total <= 1 && !p.binary {
			if seq, total, rest, ok := textPartNumber(p.body); ok {
				p.ref, p.total, p.seq, p.body = textRef, total, seq, rest
			}
		}
		key := groupKey{p.deviceID, p.sender, p.ref, p.total, 0}
		if p.total <= 1 {
			key.ref = -int(p.id) // every single-part segment is its own group
		} else if p.ref == textRef {
			base, best, bestGap := key, -1, int64(0)
			for run := range runs[base] {
				key.run = run
				group := groups[key]
				taken := false
				for _, q := range group {
					taken = taken || (q.seq == p.seq && q.body != p.body)
				}
				gap := max(p.sentAt-group[0].sentAt, group[0].sentAt-p.sentAt)
				if !taken && gap <= textPartWindow && (best < 0 || gap < bestGap) {
					best, bestGap = run, gap
				}
			}
			if best < 0 {
				best = runs[base]
				runs[base]++
			}
			key.run = best
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var created []Message
	for _, key := range order {
		parts := groups[key]
		sort.SliceStable(parts, func(i, j int) bool { return parts[i].seq < parts[j].seq })
		total := max(key.total, 1)
		// Only parts numbered within the message count. Two different
		// texts for one part mean someone sent a forged part with the same
		// reference: the first is kept, and the message marked.
		bySeq := make(map[int]pendingSegment)
		conflict := map[int]bool{}
		for _, p := range parts {
			if p.seq < 1 || p.seq > total {
				continue
			}
			if first, dup := bySeq[p.seq]; !dup {
				bySeq[p.seq] = p
			} else if first.body != p.body {
				conflict[p.seq] = true
			}
		}
		complete := len(bySeq) >= total && len(conflict) == 0
		oldest := parts[0].receivedAt
		for _, p := range parts {
			oldest = min(oldest, p.receivedAt)
		}
		if !complete && now.Sub(time.Unix(oldest, 0)) < staleAfter {
			continue
		}
		sentAt := parts[0].sentAt
		bodies := make([]string, total)
		binary := false
		for seq := 1; seq <= total; seq++ {
			if p, ok := bySeq[seq]; ok {
				bodies[seq-1] = p.body
				if conflict[seq] {
					bodies[seq-1] += conflictingPart
				}
				binary = binary || p.binary
				sentAt = min(sentAt, p.sentAt)
			}
		}
		msg := Message{
			Sender:     parts[0].sender,
			Body:       joinParts(bodies, binary),
			SentAt:     time.Unix(sentAt, 0),
			ReceivedAt: now,
			Parts:      total,
			Complete:   complete,
			Binary:     binary,
			Port:       parts[0].port,
			Kind:       KindSMS,
			DeviceID:   parts[0].deviceID,
			Direction:  DirectionIn,
		}
		if finalize != nil {
			finalize(&msg)
		}
		msg.Peer = NormalizePeer(msg.Sender)
		var expiry int64
		if !msg.MMSExpiry.IsZero() {
			expiry = msg.MMSExpiry.Unix()
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO messages (sender, body, sent_at, received_at, parts, complete, binary, kind, mms_url, mms_size, mms_expiry, read,
				device_id, peer, direction)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 'in')`,
			msg.Sender, msg.Body, msg.SentAt.Unix(), msg.ReceivedAt.Unix(), msg.Parts, msg.Complete, msg.Binary,
			msg.Kind, msg.MMSURL, msg.MMSSize, expiry, msg.DeviceID, msg.Peer)
		if err != nil {
			return nil, fmt.Errorf("insert message: %w", err)
		}
		if msg.ID, err = result.LastInsertId(); err != nil {
			return nil, err
		}
		for _, p := range parts {
			// Text-numbered parts keep their numbers so Redecode can rebuild
			// the message; the stored body keeps the prefix like the PDU.
			if _, err := tx.ExecContext(ctx, `UPDATE segments SET message_id = ?, ref = ?, total = ?, seq = ? WHERE id = ?`,
				msg.ID, p.ref, p.total, p.seq, p.id); err != nil {
				return nil, err
			}
		}
		created = append(created, msg)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

// Filter narrows ListMessages. Zero values match everything.
type Filter struct {
	Query string // substring of sender or body
	// Category is an exact category, several separated by commas, or
	// Uncategorized for messages not yet classified.
	Category string
	Since    time.Time // received at or after
	Sender   string    // exact sender
	Unread   bool
	DeviceID string
	Peer     string // normalised, see NormalizePeer
}

// Uncategorized selects messages that have not been classified yet.
const Uncategorized = "-"

const messageColumns = `id, sender, body, sent_at, received_at, parts, complete, binary, kind, mms_url, mms_size, mms_expiry,
	mms_state, mms_error, category, category_source, classified_at, read, device_id, peer, direction, status, error`

func (f Filter) where() (string, []any) {
	var clauses []string
	var args []any
	if query := strings.TrimSpace(f.Query); query != "" {
		pattern := "%" + escapeLike(query) + "%"
		clauses = append(clauses, `(sender LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}
	switch f.Category {
	case "":
	case Uncategorized:
		clauses = append(clauses, `category = '' AND direction = 'in'`)
	default:
		names := strings.Split(f.Category, ",")
		in, list := inStrings(names)
		clauses = append(clauses, `category IN `+in)
		args = append(args, list...)
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, `received_at >= ?`)
		args = append(args, f.Since.Unix())
	}
	if f.Sender != "" {
		clauses = append(clauses, `sender = ?`)
		args = append(args, f.Sender)
	}
	if f.Unread {
		clauses = append(clauses, `read = 0 AND direction = 'in'`)
	}
	if f.DeviceID != "" {
		clauses = append(clauses, `device_id = ?`)
		args = append(args, f.DeviceID)
	}
	if f.Peer != "" {
		clauses = append(clauses, `peer = ?`)
		args = append(args, f.Peer)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

type scanner interface{ Scan(...any) error }

func scanMessage(row scanner) (Message, error) {
	var m Message
	var sentAt, receivedAt, expiry, classifiedAt int64
	if err := row.Scan(&m.ID, &m.Sender, &m.Body, &sentAt, &receivedAt, &m.Parts, &m.Complete, &m.Binary,
		&m.Kind, &m.MMSURL, &m.MMSSize, &expiry, &m.MMSState, &m.MMSError, &m.Category, &m.CategorySource, &classifiedAt, &m.Read,
		&m.DeviceID, &m.Peer, &m.Direction, &m.Status, &m.Error); err != nil {
		return m, err
	}
	m.SentAt, m.ReceivedAt = time.Unix(sentAt, 0), time.Unix(receivedAt, 0)
	if expiry != 0 {
		m.MMSExpiry = time.Unix(expiry, 0)
	}
	if classifiedAt != 0 {
		m.ClassifiedAt = time.Unix(classifiedAt, 0)
	}
	return m, nil
}

// ListMessages returns messages newest first that match f, together with
// the total number of matches.
func (s *Store) ListMessages(ctx context.Context, f Filter, limit, offset int) ([]Message, int, error) {
	where, args := f.where()
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages `+where+`
		ORDER BY sent_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// GetMessage returns one message.
func (s *Store) GetMessage(ctx context.Context, id int64) (Message, error) {
	m, err := scanMessage(s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// DeleteMessages removes messages and their segments. It returns how many
// messages were removed.
func (s *Store) DeleteMessages(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	in, args := inList(ids)
	if _, err := tx.ExecContext(ctx, `DELETE FROM segments WHERE message_id IN `+in, args...); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id IN `+in, args...)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// MarkRead sets the read flag of messages; with no ids it applies to all.
func (s *Store) MarkRead(ctx context.Context, ids []int64, read bool) error {
	if len(ids) == 0 {
		_, err := s.db.ExecContext(ctx, `UPDATE messages SET read = ? WHERE direction = 'in'`, read)
		return err
	}
	in, args := inList(ids)
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET read = ? WHERE id IN `+in, append([]any{read}, args...)...)
	return err
}

// SetCategory records the category of a message.
func (s *Store) SetCategory(ctx context.Context, id int64, category, source string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET category = ?, category_source = ?, classified_at = ? WHERE id = ?`,
		category, source, at.Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetCategories queues messages for classification again: the given ids,
// or with none every message whose category was not set by hand.
func (s *Store) ResetCategories(ctx context.Context, ids []int64) (int, error) {
	query, args := `UPDATE messages SET category = '', category_source = '', classified_at = 0 WHERE category_source <> 'manual' AND direction = 'in'`, []any(nil)
	if len(ids) > 0 {
		var in string
		in, args = inList(ids)
		query = `UPDATE messages SET category = '', category_source = '', classified_at = 0 WHERE direction = 'in' AND id IN ` + in
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

// Unclassified returns the oldest messages that still need a category.
func (s *Store) Unclassified(ctx context.Context, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE category = '' AND direction = 'in'
		ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Stats summarises the message table for the dashboard.
type Stats struct {
	Total      int
	Unread     int
	Today      int
	Pending    int            // not classified yet
	Categories map[string]int // category -> count, classified messages only
	// UnreadByDevice counts unread messages per device id.
	UnreadByDevice map[string]int
}

// MessageStats counts messages; "today" starts at dayStart.
func (s *Store) MessageStats(ctx context.Context, dayStart time.Time) (Stats, error) {
	st := Stats{Categories: make(map[string]int), UnreadByDevice: make(map[string]int)}
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(read = 0), 0), COALESCE(SUM(received_at >= ?), 0), COALESCE(SUM(category = ''), 0)
		FROM messages WHERE direction = 'in'`, dayStart.Unix()).Scan(&st.Total, &st.Unread, &st.Today, &st.Pending)
	if err != nil {
		return st, err
	}
	unread, err := s.db.QueryContext(ctx, `SELECT device_id, COUNT(*) FROM messages WHERE direction = 'in' AND read = 0 GROUP BY device_id`)
	if err != nil {
		return st, err
	}
	for unread.Next() {
		var id string
		var n int
		if err := unread.Scan(&id, &n); err != nil {
			unread.Close()
			return st, err
		}
		st.UnreadByDevice[id] = n
	}
	unread.Close()
	rows, err := s.db.QueryContext(ctx, `SELECT category, COUNT(*) FROM messages WHERE category <> '' AND direction = 'in' GROUP BY category`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var category string
		var n int
		if err := rows.Scan(&category, &n); err != nil {
			return st, err
		}
		st.Categories[category] = n
	}
	return st, rows.Err()
}

func inStrings(values []string) (string, []any) {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = strings.TrimSpace(v)
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")", args
}

func inList(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

// textRef is the reference of parts numbered in their text rather than in a
// UDH header; UDH references are never negative.
const textRef = -1

// textPartWindow is how far apart, in seconds, the parts of one
// text-numbered message may have been sent; the SMSC sends them together.
const textPartWindow = 5 * 60

// maxTextParts bounds the part count read from a text prefix.
const maxTextParts = 20

// textPartPrefix matches the "(1/2)" some SMSCs put in front of each part
// when they split a long message into separate plain SMS.
var textPartPrefix = regexp.MustCompile(`^[(（](\d{1,2})/(\d{1,2})[)）]`)

// textPartNumber reads a "(seq/total)" prefix and returns the text after it.
func textPartNumber(body string) (seq, total int, rest string, ok bool) {
	m := textPartPrefix.FindStringSubmatch(body)
	if m == nil {
		return 0, 0, body, false
	}
	seq, _ = strconv.Atoi(m[1])
	total, _ = strconv.Atoi(m[2])
	if total < 2 || total > maxTextParts || seq < 1 || seq > total {
		return 0, 0, body, false
	}
	return seq, total, body[len(m[0]):], true
}

// missingPart marks a segment that never arrived.
const missingPart = "[缺失分段]"

// joinParts concatenates segment bodies in order. Text parts join seamlessly;
// hex parts are space-separated so segment boundaries stay visible.
// conflictingPart follows a part for which two different texts arrived.
const conflictingPart = "[此分段收到过不同内容，可能被伪造]"

func joinParts(bodies []string, binary bool) string {
	parts := make([]string, len(bodies))
	for i, body := range bodies {
		if body == "" {
			body = missingPart
		}
		parts[i] = body
	}
	if binary {
		return strings.Join(parts, " ")
	}
	return strings.Join(parts, "")
}

// Decoder re-decodes a stored PDU into its body and binary flag.
type Decoder func(pduHex string) (body string, binary bool, err error)

// Redecode refreshes every stored segment and message from the raw PDUs once
// per decoder version, so decoding fixes apply to messages received before
// the upgrade. It returns how many messages changed.
func (s *Store) Redecode(ctx context.Context, version string, decode Decoder) (int, error) {
	var current string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'decoder_version'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if current == version {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	type decoded struct {
		id, messageID int64
		ref, seq      int
		body          string
		binary        bool
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, pdu, ref, seq, COALESCE(message_id, 0) FROM segments`)
	if err != nil {
		return 0, err
	}
	var segments []decoded
	for rows.Next() {
		var d decoded
		var raw string
		if err := rows.Scan(&d.id, &raw, &d.ref, &d.seq, &d.messageID); err != nil {
			rows.Close()
			return 0, err
		}
		if d.body, d.binary, err = decode(raw); err != nil {
			continue // keep what was stored before
		}
		segments = append(segments, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	byMessage := make(map[int64][]decoded)
	for _, d := range segments {
		if _, err := tx.ExecContext(ctx, `UPDATE segments SET body = ?, binary = ? WHERE id = ?`, d.body, d.binary, d.id); err != nil {
			return 0, err
		}
		if d.messageID != 0 {
			byMessage[d.messageID] = append(byMessage[d.messageID], d)
		}
	}
	changed := 0
	for id, parts := range byMessage {
		var total int
		var oldBody, kind string
		var oldBinary bool
		if err := tx.QueryRowContext(ctx, `SELECT parts, body, binary, kind FROM messages WHERE id = ?`, id).Scan(&total, &oldBody, &oldBinary, &kind); err != nil {
			return 0, err
		}
		if kind != KindSMS {
			continue // an MMS body is its subject, not the raw push
		}
		bodies := make([]string, max(total, 1))
		binary := false
		for _, p := range parts {
			body := p.body
			if p.ref == textRef {
				_, _, body, _ = textPartNumber(body)
			}
			if p.seq >= 1 && p.seq <= len(bodies) && bodies[p.seq-1] == "" {
				bodies[p.seq-1] = body
				binary = binary || p.binary
			}
		}
		body := joinParts(bodies, binary)
		if body == oldBody && binary == oldBinary {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET body = ?, binary = ? WHERE id = ?`, body, binary, id); err != nil {
			return 0, err
		}
		changed++
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES ('decoder_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, version); err != nil {
		return 0, err
	}
	return changed, tx.Commit()
}

// DeleteBinaryMessages removes binary SMS (not MMS notifications) and their
// segments. It returns the number of messages removed.
func (s *Store) DeleteBinaryMessages(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	where := `binary = 1 AND kind = 'sms'`
	if _, err := tx.ExecContext(ctx, `DELETE FROM segments WHERE message_id IN (SELECT id FROM messages WHERE `+where+`)`); err != nil {
		return 0, err
	}
	// Segments still waiting for their other parts.
	if _, err := tx.ExecContext(ctx, `DELETE FROM segments WHERE message_id IS NULL AND binary = 1 AND port <> 2948`); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE `+where)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// Event is an entry of the operational log.
type Event struct {
	ID    int64
	At    time.Time
	Level string
	Text  string
}

func (s *Store) AddEvent(ctx context.Context, level, text string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO events (at, level, text) VALUES (?, ?, ?)`, time.Now().Unix(), level, text); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id <= (SELECT MAX(id) FROM events) - ?`, maxEvents)
	return err
}

// EventFilter narrows ListEvents. Level "warn" matches warnings and errors.
type EventFilter struct {
	Level string // "", "warn" or "error"
	Query string // substring of the text
}

func (f EventFilter) where() (string, []any) {
	var clauses []string
	var args []any
	switch f.Level {
	case "warn":
		clauses = append(clauses, `level IN ('warn', 'error')`)
	case "error":
		clauses = append(clauses, `level = 'error'`)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		clauses = append(clauses, `text LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(q)+"%")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

// ListEvents pages through the event log, newest first, with the number of
// matching events.
func (s *Store) ListEvents(ctx context.Context, f EventFilter, limit, offset int) ([]Event, int, error) {
	where, args := f.where()
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, level, text FROM events `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Level, &e.Text); err != nil {
			return nil, 0, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// EventCounts counts the event log by level.
func (s *Store) EventCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT level, COUNT(*) FROM events GROUP BY level`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{"info": 0, "warn": 0, "error": 0}
	for rows.Next() {
		var level string
		var n int
		if err := rows.Scan(&level, &n); err != nil {
			return nil, err
		}
		out[level] = n
	}
	return out, rows.Err()
}

func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, level, text FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Level, &e.Text); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetPasswordHash creates or updates a web account and signs out its sessions.
func (s *Store) SetPasswordHash(ctx context.Context, username, hash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		username, hash, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE username = ?`, username); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PasswordHash(ctx context.Context, username string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE username = ?`, username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0, err
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, username string, expires time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, username, expires_at) VALUES (?, ?, ?)`,
		tokenHash, username, expires.Unix())
	return err
}

// SessionUser returns the account for a valid, unexpired session.
func (s *Store) SessionUser(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	var username string
	err := s.db.QueryRowContext(ctx, `SELECT username FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, now.Unix()).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

// ExtendSession pushes a valid session's expiry to now+ttl, but only once it
// has less than ttl-every left, so an active session is written at most once
// per every. It reports whether the session was extended.
func (s *Store) ExtendSession(ctx context.Context, tokenHash string, now time.Time, ttl, every time.Duration) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token_hash = ? AND expires_at > ? AND expires_at < ?`,
		now.Add(ttl).Unix(), tokenHash, now.Unix(), now.Add(ttl-every).Unix())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// Setting returns a value from the settings table, or "" when it is unset.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetSetting stores a value; the empty string deletes it.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// BackupTo writes a consistent copy of the database to path, which must not
// exist. It works while the agent runs.
func (s *Store) BackupTo(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// Check runs SQLite's integrity check.
func (s *Store) Check(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("数据库校验失败：%s", result)
	}
	return nil
}
