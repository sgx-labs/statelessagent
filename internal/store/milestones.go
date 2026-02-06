package store

import (
	"time"
)

// Milestone keys
const (
	MilestoneCIInit       = "ci_init_suggested"
	MilestoneGuardInit    = "guard_init_suggested"
	MilestonePushProtect  = "push_protect_suggested"
	MilestoneFirstWeek    = "first_week_tips"
)

// MilestoneShown checks if a milestone has already been shown.
func (db *DB) MilestoneShown(key string) bool {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM milestones WHERE key = ?`, key).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// RecordMilestone marks a milestone as shown.
func (db *DB) RecordMilestone(key string) error {
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO milestones (key, shown_at) VALUES (?, ?)`,
		key, time.Now().Unix(),
	)
	return err
}

// MilestoneAge returns how long ago a milestone was shown (0 if never).
func (db *DB) MilestoneAge(key string) time.Duration {
	var shownAt int64
	err := db.conn.QueryRow(`SELECT shown_at FROM milestones WHERE key = ?`, key).Scan(&shownAt)
	if err != nil {
		return 0
	}
	return time.Since(time.Unix(shownAt, 0))
}

// ClearMilestone removes a milestone (for testing or reset).
func (db *DB) ClearMilestone(key string) error {
	_, err := db.conn.Exec(`DELETE FROM milestones WHERE key = ?`, key)
	return err
}
