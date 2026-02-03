package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SessionRecord represents a session log entry.
type SessionRecord struct {
	SessionID    string   `json:"session_id"`
	StartedAt    string   `json:"started_at"`
	EndedAt      string   `json:"ended_at"`
	HandoffPath  string   `json:"handoff_path"`
	Machine      string   `json:"machine"`
	FilesChanged []string `json:"files_changed"`
	Summary      string   `json:"summary"`
}

// InsertSession logs a session to the session_log table.
// Skips silently if session_id already exists.
func (db *DB) InsertSession(rec *SessionRecord) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	filesJSON, _ := json.Marshal(rec.FilesChanged)

	_, err := db.conn.Exec(`
		INSERT OR IGNORE INTO session_log (session_id, started_at, ended_at, handoff_path, machine, files_changed, summary)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.StartedAt, rec.EndedAt, rec.HandoffPath,
		rec.Machine, string(filesJSON), rec.Summary,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetRecentSessions returns the most recent sessions, optionally filtered by machine.
func (db *DB) GetRecentSessions(count int, machine string) ([]SessionRecord, error) {
	var rows *sql.Rows
	var err error

	if machine != "" {
		rows, err = db.conn.Query(`
			SELECT session_id, started_at, ended_at, handoff_path, machine, files_changed, summary
			FROM session_log
			WHERE LOWER(machine) = LOWER(?)
			ORDER BY started_at DESC
			LIMIT ?`, machine, count)
	} else {
		rows, err = db.conn.Query(`
			SELECT session_id, started_at, ended_at, handoff_path, machine, files_changed, summary
			FROM session_log
			ORDER BY started_at DESC
			LIMIT ?`, count)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var s SessionRecord
		var filesJSON string
		if err := rows.Scan(&s.SessionID, &s.StartedAt, &s.EndedAt,
			&s.HandoffPath, &s.Machine, &filesJSON, &s.Summary); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(filesJSON), &s.FilesChanged)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// LastSession returns the most recent session, or nil if none.
func (db *DB) LastSession() (*SessionRecord, error) {
	sessions, err := db.GetRecentSessions(1, "")
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	return &sessions[0], nil
}

// IndexAge returns how long ago the last indexing happened, based on the most
// recent modified timestamp in the database. Returns 0 if no data.
func (db *DB) IndexAge() (time.Duration, error) {
	var maxMod float64
	err := db.conn.QueryRow("SELECT COALESCE(MAX(modified), 0) FROM vault_notes").Scan(&maxMod)
	if err != nil || maxMod == 0 {
		return 0, err
	}
	indexed := time.Unix(int64(maxMod), 0)
	return time.Since(indexed), nil
}
