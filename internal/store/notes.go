package store

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

// NoteRecord represents a vault note chunk in the database.
type NoteRecord struct {
	ID           int64
	Path         string
	Title        string
	Tags         string // JSON array string
	Domain       string
	Workstream   string
	Agent        string
	ChunkID      int
	ChunkHeading string
	Text         string
	Modified     float64
	ContentHash  string
	ContentType  string
	ReviewBy     string
	Confidence   float64
	AccessCount  int
}

// InsertNote inserts a note record and its embedding vector.
func (db *DB) InsertNote(rec *NoteRecord, embedding []float32) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	res, err := db.conn.Exec(`
		INSERT INTO vault_notes (path, title, tags, domain, workstream, agent, chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.Path, rec.Title, rec.Tags, rec.Domain, rec.Workstream, rec.Agent,
		rec.ChunkID, rec.ChunkHeading, rec.Text, rec.Modified,
		rec.ContentHash, rec.ContentType, rec.ReviewBy, rec.Confidence, rec.AccessCount,
	)
	if err != nil {
		return fmt.Errorf("insert note: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	vecData, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return fmt.Errorf("serialize embedding: %w", err)
	}

	if _, err := db.conn.Exec(
		"INSERT INTO vault_notes_vec (note_id, embedding) VALUES (?, ?)",
		id, vecData,
	); err != nil {
		return fmt.Errorf("insert vector: %w", err)
	}

	return nil
}

// BulkInsertNotes inserts multiple note records with their embeddings in a transaction.
// Returns a map of path -> note_id for all inserted root chunks (chunk_id=0).
func (db *DB) BulkInsertNotes(records []NoteRecord, embeddings [][]float32) (map[string]int64, error) {
	if len(records) != len(embeddings) {
		return nil, fmt.Errorf("records and embeddings must have same length")
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	noteStmt, err := tx.Prepare(`
		INSERT INTO vault_notes (path, title, tags, domain, workstream, agent, chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("prepare note stmt: %w", err)
	}
	defer noteStmt.Close()

	vecStmt, err := tx.Prepare("INSERT INTO vault_notes_vec (note_id, embedding) VALUES (?, ?)")
	if err != nil {
		return nil, fmt.Errorf("prepare vec stmt: %w", err)
	}
	defer vecStmt.Close()

	insertedIDs := make(map[string]int64)

	for i, rec := range records {
		res, err := noteStmt.Exec(
			rec.Path, rec.Title, rec.Tags, rec.Domain, rec.Workstream, rec.Agent,
			rec.ChunkID, rec.ChunkHeading, rec.Text, rec.Modified,
			rec.ContentHash, rec.ContentType, rec.ReviewBy, rec.Confidence, rec.AccessCount,
		)
		if err != nil {
			return nil, fmt.Errorf("insert note %d: %w", i, err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("last insert id %d: %w", i, err)
		}

		if rec.ChunkID == 0 {
			insertedIDs[rec.Path] = id
		}

		vecData, err := sqlite_vec.SerializeFloat32(embeddings[i])
		if err != nil {
			return nil, fmt.Errorf("serialize embedding %d: %w", i, err)
		}

		if _, err := vecStmt.Exec(id, vecData); err != nil {
			return nil, fmt.Errorf("insert vector %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return insertedIDs, nil
}

// BulkInsertNotesLite inserts note records WITHOUT embeddings (FTS5-only mode).
// Used when Ollama is not available. Notes are stored for keyword search only.
// Returns a map of path -> note_id for all inserted root chunks (chunk_id=0).
func (db *DB) BulkInsertNotesLite(records []NoteRecord) (map[string]int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO vault_notes (path, title, tags, domain, workstream, agent, chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	insertedIDs := make(map[string]int64)

	for i, rec := range records {
		res, err := stmt.Exec(
			rec.Path, rec.Title, rec.Tags, rec.Domain, rec.Workstream, rec.Agent,
			rec.ChunkID, rec.ChunkHeading, rec.Text, rec.Modified,
			rec.ContentHash, rec.ContentType, rec.ReviewBy, rec.Confidence, rec.AccessCount,
		)
		if err != nil {
			return nil, fmt.Errorf("insert note %d: %w", i, err)
		}

		if rec.ChunkID == 0 {
			id, err := res.LastInsertId()
			if err != nil {
				return nil, fmt.Errorf("last insert id %d: %w", i, err)
			}
			insertedIDs[rec.Path] = id
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return insertedIDs, nil
}

// HasVectors returns true if the vault_notes_vec table has any entries.
// Used to detect whether SAME is running in full (vector) or lite (FTS5-only) mode.
func (db *DB) HasVectors() bool {
	var exists int
	err := db.conn.QueryRow("SELECT EXISTS(SELECT 1 FROM vault_notes_vec LIMIT 1)").Scan(&exists)
	if err != nil {
		return false
	}
	return exists == 1
}

// GetContentHashes returns a map of path → content_hash for all notes.
// Used for incremental reindexing. Only reads chunk_id=0 (the root chunk)
// to avoid scanning all chunks — each note's chunks share the same hash.
// Uses the covering index idx_vault_notes_path_hash for an index-only scan.
func (db *DB) GetContentHashes() (map[string]string, error) {
	rows, err := db.conn.Query("SELECT path, content_hash FROM vault_notes WHERE chunk_id = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hashes := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		hashes[path] = hash
	}
	return hashes, rows.Err()
}

// DeleteByPath removes all chunks for a given note path.
// Uses a transaction to ensure vectors and notes are deleted atomically.
func (db *DB) DeleteByPath(path string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete vectors first (referential)
	if _, err := tx.Exec(
		"DELETE FROM vault_notes_vec WHERE note_id IN (SELECT id FROM vault_notes WHERE path = ?)",
		path,
	); err != nil {
		return fmt.Errorf("delete vectors: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM vault_notes WHERE path = ?", path); err != nil {
		return fmt.Errorf("delete notes: %w", err)
	}

	return tx.Commit()
}

// DeleteAllNotes removes all notes and vectors. Used for force reindex.
// Uses a transaction to ensure vectors and notes are deleted atomically.
func (db *DB) DeleteAllNotes() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM vault_notes_vec"); err != nil {
		return fmt.Errorf("delete all vectors: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM vault_notes"); err != nil {
		return fmt.Errorf("delete all notes: %w", err)
	}
	return tx.Commit()
}

// IncrementAccessCount increments the access count for notes at the given paths.
// Uses a single batched UPDATE with an IN clause to avoid N+1 round-trips.
func (db *DB) IncrementAccessCount(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	// Build IN clause with placeholders
	placeholders := make([]string, len(paths))
	args := make([]interface{}, len(paths))
	for i, p := range paths {
		placeholders[i] = "?"
		args[i] = p
	}
	query := "UPDATE vault_notes SET access_count = access_count + 1 WHERE path IN (" +
		strings.Join(placeholders, ",") + ")"
	_, err := db.conn.Exec(query, args...)
	return err
}

// NoteCount returns the number of unique note paths in the index.
func (db *DB) NoteCount() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(DISTINCT path) FROM vault_notes").Scan(&count)
	return count, err
}

// ChunkCount returns the total number of chunks in the index.
func (db *DB) ChunkCount() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM vault_notes").Scan(&count)
	return count, err
}

// GetNoteByPath returns all chunks for a note at the given path.
func (db *DB) GetNoteByPath(path string) ([]NoteRecord, error) {
	rows, err := db.conn.Query(`
		SELECT id, path, title, tags, domain, workstream, COALESCE(agent, ''), chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count
		FROM vault_notes WHERE path = ? ORDER BY chunk_id`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNotes(rows)
}

// GetStaleNotes returns notes with review_by dates that are past due.
// SECURITY: Excludes _PRIVATE/ content from results.
func (db *DB) GetStaleNotes(maxResults int, overdueOnly bool) ([]NoteRecord, error) {
	query := `
		SELECT DISTINCT id, path, title, tags, domain, workstream, COALESCE(agent, ''), chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count
		FROM vault_notes
		WHERE review_by != '' AND review_by IS NOT NULL AND path NOT LIKE '_PRIVATE/%'
		GROUP BY path
		HAVING MIN(chunk_id)
		ORDER BY review_by ASC
		LIMIT ?`

	rows, err := db.conn.Query(query, maxResults*2) // fetch extra for post-filtering
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results, err := scanNotes(rows)
	if err != nil {
		return nil, err
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return results, nil
}

// GetNoteEmbedding returns the first chunk's embedding for a note.
func (db *DB) GetNoteEmbedding(path string) ([]float32, error) {
	var noteID int64
	err := db.conn.QueryRow(
		"SELECT id FROM vault_notes WHERE path = ? ORDER BY chunk_id LIMIT 1", path,
	).Scan(&noteID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	var vecData []byte
	err = db.conn.QueryRow(
		"SELECT embedding FROM vault_notes_vec WHERE note_id = ?", noteID,
	).Scan(&vecData)
	if err != nil {
		return nil, err
	}

	return deserializeFloat32(vecData)
}

// deserializeFloat32 converts raw little-endian bytes back to []float32.
func deserializeFloat32(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid vector data length: %d", len(data))
	}
	n := len(data) / 4
	vec := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(data[i*4 : (i+1)*4])
		vec[i] = math.Float32frombits(bits)
	}
	return vec, nil
}

func scanNotes(rows *sql.Rows) ([]NoteRecord, error) {
	var notes []NoteRecord
	for rows.Next() {
		var n NoteRecord
		if err := rows.Scan(
			&n.ID, &n.Path, &n.Title, &n.Tags, &n.Domain, &n.Workstream, &n.Agent,
			&n.ChunkID, &n.ChunkHeading, &n.Text, &n.Modified,
			&n.ContentHash, &n.ContentType, &n.ReviewBy, &n.Confidence, &n.AccessCount,
		); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// RecentNotes returns the most recently modified notes (one chunk per path).
// SECURITY: Excludes _PRIVATE/ content from results.
func (db *DB) RecentNotes(limit int) ([]NoteRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.conn.Query(`
		SELECT id, path, title, tags, domain, workstream, COALESCE(agent, ''), chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count
		FROM vault_notes
		WHERE chunk_id = 0 AND path NOT LIKE '_PRIVATE/%'
		ORDER BY modified DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}

// AllNotes returns all notes (chunk_id=0 only, one per path).
// SECURITY: Excludes _PRIVATE/ content from results.
func (db *DB) AllNotes() ([]NoteRecord, error) {
	rows, err := db.conn.Query(`
		SELECT id, path, title, tags, domain, workstream, COALESCE(agent, ''), chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count
		FROM vault_notes
		WHERE chunk_id = 0 AND path NOT LIKE '_PRIVATE/%'
		ORDER BY modified DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}

// AdjustConfidence sets the confidence for all chunks of a note at the given path.
func (db *DB) AdjustConfidence(path string, newConfidence float64) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec("UPDATE vault_notes SET confidence = ? WHERE path = ?", newConfidence, path)
	return err
}

// SetAccessBoost increments the access_count by boost for all chunks at the given path.
func (db *DB) SetAccessBoost(path string, boost int) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec("UPDATE vault_notes SET access_count = access_count + ? WHERE path = ?", boost, path)
	return err
}

// ParseTags parses the JSON tags string into a slice.
func ParseTags(tagsJSON string) []string {
	var tags []string
	json.Unmarshal([]byte(tagsJSON), &tags)
	return tags
}
