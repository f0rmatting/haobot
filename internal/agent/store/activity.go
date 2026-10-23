package store

import (
	"context"
	"time"
)

// ActivityBucket counts what happened in one hour or one day.
type ActivityBucket struct {
	Start  time.Time `json:"start"`
	SMS    int       `json:"sms"`    // received SMS and MMS notices
	Spam   int       `json:"spam"`   // of SMS: marketing and telesales
	Codes  int       `json:"codes"`  // of SMS: verification codes
	Calls  int       `json:"calls"`  // incoming calls
	AI     int       `json:"ai"`     // of calls: answered by the assistant
	Missed int       `json:"missed"` // of calls: missed or rejected
}

// Activity is the dashboard's recent history.
type Activity struct {
	Hours []ActivityBucket `json:"hours"` // the last 24 hours, oldest first
	Days  []ActivityBucket `json:"days"`  // the last 7 days, oldest first
}

// spamCategories are the default categories counted as unwanted.
var spamCategories = map[string]bool{"营销推广": true, "推销骚扰": true}

// Activity buckets incoming SMS and calls by hour over the last 24 hours and
// by local day over the last 7 days.
func (s *Store) Activity(ctx context.Context, now time.Time) (Activity, error) {
	hourStart := now.Truncate(time.Hour).Add(-23 * time.Hour)
	y, m, d := now.Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -6)
	a := Activity{Hours: make([]ActivityBucket, 24), Days: make([]ActivityBucket, 7)}
	for i := range a.Hours {
		a.Hours[i].Start = hourStart.Add(time.Duration(i) * time.Hour)
	}
	for i := range a.Days {
		a.Days[i].Start = dayStart.AddDate(0, 0, i)
	}
	// add counts an event into its hour and day buckets.
	add := func(at time.Time, f func(*ActivityBucket)) {
		if !at.Before(hourStart) {
			if i := int(at.Sub(hourStart) / time.Hour); i < len(a.Hours) {
				f(&a.Hours[i])
			}
		}
		for i := len(a.Days) - 1; i >= 0; i-- {
			if !at.Before(a.Days[i].Start) {
				f(&a.Days[i])
				break
			}
		}
	}
	from := dayStart
	if hourStart.Before(from) {
		from = hourStart
	}

	rows, err := s.db.QueryContext(ctx, `SELECT received_at, category FROM messages WHERE direction = 'in' AND received_at >= ?`, from.Unix())
	if err != nil {
		return a, err
	}
	for rows.Next() {
		var at int64
		var category string
		if err := rows.Scan(&at, &category); err != nil {
			rows.Close()
			return a, err
		}
		spam, code := spamCategories[category], category == "验证码"
		add(time.Unix(at, 0), func(b *ActivityBucket) {
			b.SMS++
			if spam {
				b.Spam++
			}
			if code {
				b.Codes++
			}
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT started_at, status, handled_by FROM calls WHERE direction = 'in' AND started_at >= ?`, from.Unix())
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		var status, by string
		if err := rows.Scan(&at, &status, &by); err != nil {
			return a, err
		}
		add(time.Unix(at, 0), func(b *ActivityBucket) {
			b.Calls++
			if by == HandledByAssistant {
				b.AI++
			}
			if status == CallMissed || status == CallRejected {
				b.Missed++
			}
		})
	}
	return a, rows.Err()
}
