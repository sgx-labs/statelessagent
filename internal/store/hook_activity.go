package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const maxHookActivityRows = 500

var hookLogSeq uint64

// HookActivityRecord captures one hook invocation summary for user-visible logs.
type HookActivityRecord struct {
	TimestampUnix   int64    `json:"timestamp_unix"`
	HookSessionID   string   `json:"hook_session_id,omitempty"`
	HookName        string   `json:"hook_name"`
	Status          string   `json:"status"`
	SurfacedNotes   int      `json:"surfaced_notes"`
	EstimatedTokens int      `json:"estimated_tokens"`
	ErrorMessage    string   `json:"error_message,omitempty"`
	Detail          string   `json:"detail,omitempty"`
	NotePaths       []string `json:"note_paths,omitempty"`
}

// InsertHookActivity appends a hook activity record to session_log and prunes old hook rows.
func (db *DB) InsertHookActivity(rec *HookActivityRecord) error {
	if rec == nil {
		return nil
	}

	now := time.Now().UTC()
	ts := rec.TimestampUnix
	if ts <= 0 {
		ts = now.Unix()
	}
	status := strings.ToLower(strings.TrimSpace(rec.Status))
	if status == "" {
		status = "empty"
	}

	notePaths := rec.NotePaths
	if notePaths == nil {
		notePaths = []string{}
	}
	pathsJSON, _ := json.Marshal(notePaths)

	rowID := fmt.Sprintf("hook-%d-%d", now.UnixNano(), atomic.AddUint64(&hookLogSeq, 1))

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(`
		INSERT INTO session_log (
			session_id, started_at, ended_at, handoff_path, machine, files_changed, summary,
			entry_kind, hook_timestamp, hook_name, hook_status, surfaced_notes, estimated_tokens,
			error_message, note_paths, detail, hook_session_id
		) VALUES (?, ?, ?, '', '', '[]', '', 'hook', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rowID,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		ts,
		rec.HookName,
		status,
		max(rec.SurfacedNotes, 0),
		max(rec.EstimatedTokens, 0),
		rec.ErrorMessage,
		string(pathsJSON),
		rec.Detail,
		rec.HookSessionID,
	)
	if err != nil {
		return fmt.Errorf("insert hook activity: %w", err)
	}

	_, _ = db.conn.Exec(`
		DELETE FROM session_log
		WHERE entry_kind = 'hook'
		  AND session_id NOT IN (
			SELECT session_id
			FROM session_log
			WHERE entry_kind = 'hook'
			ORDER BY hook_timestamp DESC, rowid DESC
			LIMIT ?
		  )`,
		maxHookActivityRows,
	)
	return nil
}

// GetRecentHookActivity returns hook activity rows ordered newest-first.
func (db *DB) GetRecentHookActivity(limit int) ([]HookActivityRecord, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := db.conn.Query(`
		SELECT hook_timestamp, hook_session_id, hook_name, hook_status, surfaced_notes,
		       estimated_tokens, error_message, detail, note_paths
		FROM session_log
		WHERE entry_kind = 'hook'
		ORDER BY hook_timestamp DESC, rowid DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query hook activity: %w", err)
	}
	defer rows.Close()

	var out []HookActivityRecord
	for rows.Next() {
		var rec HookActivityRecord
		var notePathsJSON string
		if err := rows.Scan(
			&rec.TimestampUnix,
			&rec.HookSessionID,
			&rec.HookName,
			&rec.Status,
			&rec.SurfacedNotes,
			&rec.EstimatedTokens,
			&rec.ErrorMessage,
			&rec.Detail,
			&notePathsJSON,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(notePathsJSON), &rec.NotePaths)
		out = append(out, rec)
	}
	return out, rows.Err()
}
