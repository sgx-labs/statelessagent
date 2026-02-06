package store

import (
	"encoding/json"
	"fmt"
)

// UsageRecord represents a context injection event.
type UsageRecord struct {
	ID              int64    `json:"id,omitempty"`
	SessionID       string   `json:"session_id"`
	Timestamp       string   `json:"timestamp"`
	HookName        string   `json:"hook_name"`
	InjectedPaths   []string `json:"injected_paths"`
	EstimatedTokens int      `json:"estimated_tokens"`
	WasReferenced   bool     `json:"was_referenced"`
}

// InsertUsage logs a context injection event.
func (db *DB) InsertUsage(rec *UsageRecord) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	pathsJSON, _ := json.Marshal(rec.InjectedPaths)
	wasRef := 0
	if rec.WasReferenced {
		wasRef = 1
	}

	_, err := db.conn.Exec(`
		INSERT INTO context_usage (session_id, timestamp, hook_name, injected_paths, estimated_tokens, was_referenced)
		VALUES (?, ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.Timestamp, rec.HookName,
		string(pathsJSON), rec.EstimatedTokens, wasRef,
	)
	if err != nil {
		return fmt.Errorf("insert usage: %w", err)
	}
	return nil
}

// GetUsageBySession returns all usage records for a session.
func (db *DB) GetUsageBySession(sessionID string) ([]UsageRecord, error) {
	rows, err := db.conn.Query(`
		SELECT id, session_id, timestamp, hook_name, injected_paths, estimated_tokens, was_referenced
		FROM context_usage
		WHERE session_id = ?
		ORDER BY timestamp`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUsage(rows)
}

// GetRecentUsage returns usage records from the most recent sessions.
func (db *DB) GetRecentUsage(lastNSessions int) ([]UsageRecord, error) {
	rows, err := db.conn.Query(`
		SELECT id, session_id, timestamp, hook_name, injected_paths, estimated_tokens, was_referenced
		FROM context_usage
		WHERE session_id IN (
			SELECT DISTINCT session_id FROM context_usage ORDER BY timestamp DESC LIMIT ?
		)
		ORDER BY timestamp`, lastNSessions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUsage(rows)
}

// MarkReferenced updates the was_referenced flag for a usage record.
func (db *DB) MarkReferenced(id int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec("UPDATE context_usage SET was_referenced = 1 WHERE id = ?", id)
	return err
}

// DecisionRecord represents a context surfacing gate decision.
type DecisionRecord struct {
	SessionID     string   `json:"session_id"`
	PromptSnippet string   `json:"prompt_snippet"`
	Mode          string   `json:"mode"`
	JaccardScore  float64  `json:"jaccard_score"`
	Decision      string   `json:"decision"`
	InjectedPaths []string `json:"injected_paths,omitempty"`
}

// InsertDecision logs a context surfacing gate decision.
func (db *DB) InsertDecision(rec *DecisionRecord) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	pathsJSON, _ := json.Marshal(rec.InjectedPaths)
	_, err := db.conn.Exec(`
		INSERT INTO context_decisions (session_id, timestamp, prompt_snippet, mode, jaccard_score, decision, injected_paths)
		VALUES (?, datetime('now'), ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.PromptSnippet, rec.Mode, rec.JaccardScore, rec.Decision, string(pathsJSON),
	)
	return err
}

func scanUsage(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}) ([]UsageRecord, error) {
	var records []UsageRecord
	for rows.Next() {
		var r UsageRecord
		var pathsJSON string
		var wasRef int
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Timestamp, &r.HookName,
			&pathsJSON, &r.EstimatedTokens, &wasRef); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(pathsJSON), &r.InjectedPaths)
		r.WasReferenced = wasRef != 0
		records = append(records, r)
	}
	return records, rows.Err()
}
