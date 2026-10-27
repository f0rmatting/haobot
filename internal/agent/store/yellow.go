package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// YellowNumber is one of a yellow pages organisation's numbers.
type YellowNumber struct {
	Number string `json:"number"`
	Label  string `json:"label,omitempty"`
}

// YellowOrg is the user's change to the yellow pages: a built-in
// organisation edited or hidden, or an organisation of their own.
type YellowOrg struct {
	ID int64
	// Builtin is the name of the built-in organisation this replaces; it is
	// "" for the user's own.
	Builtin  string
	Name     string
	Category string
	Numbers  []YellowNumber
	// HasLogo says whether YellowLogo has a picture; without one an edited
	// built-in organisation keeps its built-in logo.
	HasLogo   bool
	Hidden    bool
	UpdatedAt time.Time
}

// YellowOrgs returns the user's yellow pages changes, oldest first.
func (s *Store) YellowOrgs(ctx context.Context) ([]YellowOrg, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(builtin, ''), name, category, numbers, COALESCE(length(logo), 0) > 0, hidden, updated_at FROM yellow_orgs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []YellowOrg
	for rows.Next() {
		var o YellowOrg
		var numbers string
		var updated int64
		if err := rows.Scan(&o.ID, &o.Builtin, &o.Name, &o.Category, &numbers, &o.HasLogo, &o.Hidden, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(numbers), &o.Numbers); err != nil {
			return nil, err
		}
		o.UpdatedAt = time.Unix(updated, 0)
		out = append(out, o)
	}
	return out, rows.Err()
}

// SaveYellowOrg adds o, or updates the row with o.ID, or else the row that
// already changes the same built-in organisation. It returns o with its ID.
func (s *Store) SaveYellowOrg(ctx context.Context, o YellowOrg) (YellowOrg, error) {
	if o.ID == 0 && o.Builtin != "" {
		err := s.db.QueryRowContext(ctx, `SELECT id FROM yellow_orgs WHERE builtin = ?`, o.Builtin).Scan(&o.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return o, err
		}
	}
	if o.Numbers == nil {
		o.Numbers = []YellowNumber{}
	}
	numbers, err := json.Marshal(o.Numbers)
	if err != nil {
		return o, err
	}
	var builtin any
	if o.Builtin != "" {
		builtin = o.Builtin
	}
	o.UpdatedAt = time.Now()
	if o.ID == 0 {
		result, err := s.db.ExecContext(ctx, `INSERT INTO yellow_orgs (builtin, name, category, numbers, hidden, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			builtin, o.Name, o.Category, string(numbers), o.Hidden, o.UpdatedAt.Unix())
		if err != nil {
			return o, err
		}
		o.ID, err = result.LastInsertId()
		return o, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE yellow_orgs SET builtin = ?, name = ?, category = ?, numbers = ?, hidden = ?, updated_at = ? WHERE id = ?`,
		builtin, o.Name, o.Category, string(numbers), o.Hidden, o.UpdatedAt.Unix(), o.ID)
	if err != nil {
		return o, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return o, ErrNotFound
	}
	return o, nil
}

// DeleteYellowOrg removes a change: the user's own organisation goes, and
// an edited or hidden built-in one is back as it ships.
func (s *Store) DeleteYellowOrg(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM yellow_orgs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UnhideYellowOrgs brings back every hidden built-in organisation.
func (s *Store) UnhideYellowOrgs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM yellow_orgs WHERE hidden <> 0`)
	return err
}

// SetYellowLogo stores an organisation's logo, or removes it when data is
// empty.
func (s *Store) SetYellowLogo(ctx context.Context, id int64, data []byte) error {
	var logo any
	if len(data) > 0 {
		logo = data
	}
	result, err := s.db.ExecContext(ctx, `UPDATE yellow_orgs SET logo = ?, updated_at = ? WHERE id = ?`, logo, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// YellowLogo returns the logo the user gave an organisation.
func (s *Store) YellowLogo(ctx context.Context, id int64) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT logo FROM yellow_orgs WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) || err == nil && len(data) == 0 {
		return nil, ErrNotFound
	}
	return data, err
}
