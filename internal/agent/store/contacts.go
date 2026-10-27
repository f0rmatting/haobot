package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrContactExists is returned when another contact has the same number.
var ErrContactExists = errors.New("这个号码已经在通讯录里了")

// Contact is an entry of the user's own phone book.
type Contact struct {
	ID     int64
	Name   string
	Number string // as entered
	// Key is the number in lookup form (package phonebook); it is unique.
	Key  string
	Note string
	// HasAvatar says whether ContactAvatar has a picture; it is not loaded
	// with the contact.
	HasAvatar bool
	UpdatedAt time.Time
}

// Contacts returns every contact, by name.
func (s *Store) Contacts(ctx context.Context) ([]Contact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, number, key, note, COALESCE(length(avatar), 0) > 0, updated_at FROM contacts ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		var updated int64
		if err := rows.Scan(&c.ID, &c.Name, &c.Number, &c.Key, &c.Note, &c.HasAvatar, &updated); err != nil {
			return nil, err
		}
		c.UpdatedAt = time.Unix(updated, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveContact adds c, or updates the contact with c.ID when it is set.
func (s *Store) SaveContact(ctx context.Context, c Contact) (Contact, error) {
	var other int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM contacts WHERE key = ? AND id <> ?`, c.Key, c.ID).Scan(&other)
	if err == nil {
		return c, ErrContactExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	c.UpdatedAt = time.Now()
	if c.ID == 0 {
		result, err := s.db.ExecContext(ctx, `INSERT INTO contacts (name, number, key, note, updated_at) VALUES (?, ?, ?, ?, ?)`,
			c.Name, c.Number, c.Key, c.Note, c.UpdatedAt.Unix())
		if err != nil {
			return c, err
		}
		c.ID, err = result.LastInsertId()
		return c, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE contacts SET name = ?, number = ?, key = ?, note = ?, updated_at = ? WHERE id = ?`,
		c.Name, c.Number, c.Key, c.Note, c.UpdatedAt.Unix(), c.ID)
	if err != nil {
		return c, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return c, ErrNotFound
	}
	return c, nil
}

// DeleteContact removes a contact.
func (s *Store) DeleteContact(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM contacts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetContactAvatar stores a contact's picture, or removes it when data is
// empty.
func (s *Store) SetContactAvatar(ctx context.Context, id int64, data []byte) error {
	var avatar any
	if len(data) > 0 {
		avatar = data
	}
	result, err := s.db.ExecContext(ctx, `UPDATE contacts SET avatar = ?, updated_at = ? WHERE id = ?`, avatar, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ContactAvatar returns a contact's picture.
func (s *Store) ContactAvatar(ctx context.Context, id int64) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT avatar FROM contacts WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) || err == nil && len(data) == 0 {
		return nil, ErrNotFound
	}
	return data, err
}
