package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NoteSource represents a provenance record for a note.
type NoteSource struct {
	ID         int64
	NotePath   string
	SourcePath string
	SourceType string // "file", "note", "url"
	SourceHash string // SHA256 at capture time
	CapturedAt int64  // Unix timestamp
}

// DivergenceResult describes a source whose current hash differs from the stored hash.
type DivergenceResult struct {
	NotePath    string
	SourcePath  string
	StoredHash  string
	CurrentHash string
	CapturedAt  int64
}

// TrustSummary holds counts of each trust state across all notes.
type TrustSummary struct {
	Validated    int
	Stale        int
	Contradicted int
	Unknown      int
}

// RecordSource records that a note was derived from a source.
func (db *DB) RecordSource(notePath, sourcePath, sourceType, sourceHash string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(
		`INSERT INTO note_sources (note_path, source_path, source_type, source_hash)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(note_path, source_path) DO UPDATE SET
		   source_type = excluded.source_type,
		   source_hash = excluded.source_hash,
		   captured_at = unixepoch()`,
		notePath, sourcePath, sourceType, sourceHash,
	)
	if err != nil {
		return fmt.Errorf("record source: %w", err)
	}
	return nil
}

// RecordSources records multiple sources for a note in one transaction.
func (db *DB) RecordSources(notePath string, sources []NoteSource) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin record sources: %w", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO note_sources (note_path, source_path, source_type, source_hash)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(note_path, source_path) DO UPDATE SET
		   source_type = excluded.source_type,
		   source_hash = excluded.source_hash,
		   captured_at = unixepoch()`,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare record sources: %w", err)
	}
	defer stmt.Close()
	for _, s := range sources {
		if _, err := stmt.Exec(notePath, s.SourcePath, s.SourceType, s.SourceHash); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record source %s: %w", s.SourcePath, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record sources: %w", err)
	}
	return nil
}

// GetSourcesForNote returns all sources for a given note path.
func (db *DB) GetSourcesForNote(notePath string) ([]NoteSource, error) {
	rows, err := db.conn.Query(
		`SELECT id, note_path, source_path, source_type, source_hash, captured_at
		 FROM note_sources
		 WHERE note_path = ?
		 ORDER BY source_path ASC`,
		notePath,
	)
	if err != nil {
		return nil, fmt.Errorf("get sources for note: %w", err)
	}
	defer rows.Close()

	var sources []NoteSource
	for rows.Next() {
		var s NoteSource
		if err := rows.Scan(&s.ID, &s.NotePath, &s.SourcePath, &s.SourceType, &s.SourceHash, &s.CapturedAt); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// GetDependentNotes returns all note paths that depend on a given source path.
func (db *DB) GetDependentNotes(sourcePath string) ([]string, error) {
	rows, err := db.conn.Query(
		`SELECT DISTINCT note_path FROM note_sources WHERE source_path = ? ORDER BY note_path ASC`,
		sourcePath,
	)
	if err != nil {
		return nil, fmt.Errorf("get dependent notes: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan dependent note: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// DeleteSourcesForNote removes all source records for a note (used on re-index).
func (db *DB) DeleteSourcesForNote(notePath string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(`DELETE FROM note_sources WHERE note_path = ?`, notePath)
	if err != nil {
		return fmt.Errorf("delete sources for note: %w", err)
	}
	return nil
}

// CheckSourceDivergence compares stored source hashes against current file hashes.
// Returns notes whose file-type sources have changed since capture. Sources that
// no longer exist on disk are reported with an empty CurrentHash.
func (db *DB) CheckSourceDivergence(vaultPath string) ([]DivergenceResult, error) {
	rows, err := db.conn.Query(
		`SELECT note_path, source_path, source_hash, captured_at
		 FROM note_sources
		 WHERE source_type = 'file'
		 ORDER BY note_path ASC, source_path ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("check source divergence: %w", err)
	}
	defer rows.Close()

	var results []DivergenceResult
	for rows.Next() {
		var notePath, sourcePath, storedHash string
		var capturedAt int64
		if err := rows.Scan(&notePath, &sourcePath, &storedHash, &capturedAt); err != nil {
			return nil, fmt.Errorf("scan divergence row: %w", err)
		}

		fullPath := filepath.Join(vaultPath, sourcePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			// File no longer exists or is unreadable — that's divergence.
			results = append(results, DivergenceResult{
				NotePath:    notePath,
				SourcePath:  sourcePath,
				StoredHash:  storedHash,
				CurrentHash: "",
				CapturedAt:  capturedAt,
			})
			continue
		}

		currentHash := fileSHA256(content)
		if !strings.EqualFold(currentHash, storedHash) {
			results = append(results, DivergenceResult{
				NotePath:    notePath,
				SourcePath:  sourcePath,
				StoredHash:  storedHash,
				CurrentHash: currentHash,
				CapturedAt:  capturedAt,
			})
		}
	}
	return results, rows.Err()
}

// UpdateTrustState sets the trust_state for notes by path.
// Valid states: "validated", "stale", "contradicted", "unknown".
func (db *DB) UpdateTrustState(paths []string, state string) error {
	if len(paths) == 0 {
		return nil
	}
	validStates := map[string]bool{
		"validated":    true,
		"stale":        true,
		"contradicted": true,
		"unknown":      true,
	}
	if !validStates[state] {
		return fmt.Errorf("invalid trust state: %q", state)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	placeholders := make([]string, len(paths))
	args := make([]interface{}, 0, len(paths)+1)
	args = append(args, state)
	for i, p := range paths {
		placeholders[i] = "?"
		args = append(args, p)
	}
	query := fmt.Sprintf(
		"UPDATE vault_notes SET trust_state = ? WHERE path IN (%s)",
		strings.Join(placeholders, ","),
	)
	_, err := db.conn.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("update trust state: %w", err)
	}
	return nil
}

// GetTrustStateSummary returns counts of each trust state across all notes
// (only counting chunk_id=0 to avoid double-counting chunked notes).
func (db *DB) GetTrustStateSummary() (*TrustSummary, error) {
	rows, err := db.conn.Query(
		`SELECT COALESCE(trust_state, 'unknown') AS ts, COUNT(*)
		 FROM vault_notes
		 WHERE chunk_id = 0
		 GROUP BY ts`,
	)
	if err != nil {
		return nil, fmt.Errorf("get trust state summary: %w", err)
	}
	defer rows.Close()

	summary := &TrustSummary{}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scan trust state: %w", err)
		}
		switch state {
		case "validated":
			summary.Validated = count
		case "stale":
			summary.Stale = count
		case "contradicted":
			summary.Contradicted = count
		default:
			summary.Unknown += count
		}
	}
	return summary, rows.Err()
}

// GetNotesWithSources returns distinct note paths that have at least one source recorded.
func (db *DB) GetNotesWithSources() ([]string, error) {
	rows, err := db.conn.Query(
		`SELECT DISTINCT note_path FROM note_sources ORDER BY note_path ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get notes with sources: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan note with sources: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// fileSHA256 computes the hex-encoded SHA256 hash of file content.
func fileSHA256(content []byte) string {
	h := sha256.Sum256(content)
	return fmt.Sprintf("%x", h)
}
