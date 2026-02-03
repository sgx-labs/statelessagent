package store

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

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
		INSERT INTO vault_notes (path, title, tags, domain, workstream, chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.Path, rec.Title, rec.Tags, rec.Domain, rec.Workstream,
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
func (db *DB) BulkInsertNotes(records []NoteRecord, embeddings [][]float32) error {
	if len(records) != len(embeddings) {
		return fmt.Errorf("records and embeddings must have same length")
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	noteStmt, err := tx.Prepare(`
		INSERT INTO vault_notes (path, title, tags, domain, workstream, chunk_id, chunk_heading,
			text, modified, content_hash, content_type, review_by, confidence, access_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare note stmt: %w", err)
	}
	defer noteStmt.Close()

	vecStmt, err := tx.Prepare("INSERT INTO vault_notes_vec (note_id, embedding) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("prepare vec stmt: %w", err)
	}
	defer vecStmt.Close()

	for i, rec := range records {
		res, err := noteStmt.Exec(
			rec.Path, rec.Title, rec.Tags, rec.Domain, rec.Workstream,
			rec.ChunkID, rec.ChunkHeading, rec.Text, rec.Modified,
			rec.ContentHash, rec.ContentType, rec.ReviewBy, rec.Confidence, rec.AccessCount,
		)
		if err != nil {
			return fmt.Errorf("insert note %d: %w", i, err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("last insert id %d: %w", i, err)
		}

		vecData, err := sqlite_vec.SerializeFloat32(embeddings[i])
		if err != nil {
			return fmt.Errorf("serialize embedding %d: %w", i, err)
		}

		if _, err := vecStmt.Exec(id, vecData); err != nil {
			return fmt.Errorf("insert vector %d: %w", i, err)
		}
	}

	return tx.Commit()
}

// GetContentHashes returns a map of path → content_hash for all notes.
// Used for incremental reindexing.
func (db *DB) GetContentHashes() (map[string]string, error) {
	rows, err := db.conn.Query("SELECT DISTINCT path, content_hash FROM vault_notes")
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
func (db *DB) DeleteByPath(path string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Delete vectors first (referential)
	if _, err := db.conn.Exec(
		"DELETE FROM vault_notes_vec WHERE note_id IN (SELECT id FROM vault_notes WHERE path = ?)",
		path,
	); err != nil {
		return fmt.Errorf("delete vectors: %w", err)
	}

	if _, err := db.conn.Exec("DELETE FROM vault_notes WHERE path = ?", path); err != nil {
		return fmt.Errorf("delete notes: %w", err)
	}

	return nil
}

// DeleteAllNotes removes all notes and vectors. Used for force reindex.
func (db *DB) DeleteAllNotes() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.conn.Exec("DELETE FROM vault_notes_vec"); err != nil {
		return fmt.Errorf("delete all vectors: %w", err)
	}
	if _, err := db.conn.Exec("DELETE FROM vault_notes"); err != nil {
		return fmt.Errorf("delete all notes: %w", err)
	}
	return nil
}

// IncrementAccessCount increments the access count for notes at the given paths.
func (db *DB) IncrementAccessCount(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	stmt, err := db.conn.Prepare("UPDATE vault_notes SET access_count = access_count + 1 WHERE path = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range paths {
		stmt.Exec(p)
	}
	return nil
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
		SELECT id, path, title, tags, domain, workstream, chunk_id, chunk_heading,
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
		SELECT DISTINCT id, path, title, tags, domain, workstream, chunk_id, chunk_heading,
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

	return scanNotes(rows)
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
			&n.ID, &n.Path, &n.Title, &n.Tags, &n.Domain, &n.Workstream,
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
		SELECT id, path, title, tags, domain, workstream, chunk_id, chunk_heading,
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

// ParseTags parses the JSON tags string into a slice.
func ParseTags(tagsJSON string) []string {
	var tags []string
	json.Unmarshal([]byte(tagsJSON), &tags)
	return tags
}
