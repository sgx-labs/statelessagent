package store

import (
	"encoding/binary"
	"fmt"
	"math"
)

// FactRecord represents an atomic fact stored in the database.
type FactRecord struct {
	ID         int64
	FactText   string
	SourcePath string
	ChunkID    int
	Confidence float64
	CreatedAt  int64
}

// FactSearchResult represents a fact returned by vector search.
type FactSearchResult struct {
	ID         int64   `json:"id"`
	FactText   string  `json:"fact_text"`
	SourcePath string  `json:"source_path"`
	ChunkID    int     `json:"chunk_id"`
	Confidence float64 `json:"confidence"`
	Distance   float64 `json:"distance"`
}

// InsertFact inserts a fact record and its embedding vector.
func (db *DB) InsertFact(rec *FactRecord, embedding []float32) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	res, err := db.conn.Exec(`
		INSERT INTO facts (fact_text, source_path, chunk_id, confidence)
		VALUES (?, ?, ?, ?)`,
		rec.FactText, rec.SourcePath, rec.ChunkID, rec.Confidence,
	)
	if err != nil {
		return fmt.Errorf("insert fact: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	vecData, err := serializeFactVec(embedding)
	if err != nil {
		return fmt.Errorf("serialize fact embedding: %w", err)
	}

	if _, err := db.conn.Exec(
		"INSERT INTO facts_vec (fact_id, embedding) VALUES (?, ?)",
		id, vecData,
	); err != nil {
		return fmt.Errorf("insert fact vector: %w", err)
	}

	return nil
}

// SearchFacts performs a KNN vector search on fact embeddings.
func (db *DB) SearchFacts(queryVec []float32, topK int) ([]FactSearchResult, error) {
	if topK <= 0 {
		topK = 10
	}
	if topK > 100 {
		topK = 100
	}

	vecData, err := serializeFactVec(queryVec)
	if err != nil {
		return nil, fmt.Errorf("serialize query: %w", err)
	}

	rows, err := db.conn.Query(`
		SELECT v.distance, f.id, f.fact_text, f.source_path, f.chunk_id, f.confidence
		FROM facts_vec v
		JOIN facts f ON f.id = v.fact_id
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance`,
		vecData, topK,
	)
	if err != nil {
		return nil, fmt.Errorf("fact vector search: %w", err)
	}
	defer rows.Close()

	var results []FactSearchResult
	for rows.Next() {
		var r FactSearchResult
		if err := rows.Scan(&r.Distance, &r.ID, &r.FactText, &r.SourcePath, &r.ChunkID, &r.Confidence); err != nil {
			return nil, fmt.Errorf("scan fact result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// DeleteFactsForPath removes all facts associated with a source path.
// Used when a note is re-indexed or deleted.
func (db *DB) DeleteFactsForPath(sourcePath string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Delete vectors first (references fact IDs)
	if _, err := tx.Exec(`
		DELETE FROM facts_vec WHERE fact_id IN (
			SELECT id FROM facts WHERE source_path = ?
		)`, sourcePath); err != nil {
		return fmt.Errorf("delete fact vectors: %w", err)
	}

	// Delete facts
	if _, err := tx.Exec(`DELETE FROM facts WHERE source_path = ?`, sourcePath); err != nil {
		return fmt.Errorf("delete facts: %w", err)
	}

	return tx.Commit()
}

// FactCount returns the total number of facts in the database.
func (db *DB) FactCount() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM facts").Scan(&count)
	return count, err
}

// GetFactsForPath returns all facts associated with a source path.
func (db *DB) GetFactsForPath(sourcePath string) ([]FactRecord, error) {
	rows, err := db.conn.Query(`
		SELECT id, fact_text, source_path, chunk_id, confidence, created_at
		FROM facts
		WHERE source_path = ?
		ORDER BY id ASC`,
		sourcePath,
	)
	if err != nil {
		return nil, fmt.Errorf("get facts for path: %w", err)
	}
	defer rows.Close()

	var facts []FactRecord
	for rows.Next() {
		var f FactRecord
		if err := rows.Scan(&f.ID, &f.FactText, &f.SourcePath, &f.ChunkID, &f.Confidence, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// PathsWithFacts returns the set of source paths that already have facts extracted.
// Used by the fact extraction backfill to skip notes that have already been processed.
func (db *DB) PathsWithFacts() (map[string]bool, error) {
	rows, err := db.conn.Query("SELECT DISTINCT source_path FROM facts")
	if err != nil {
		return nil, fmt.Errorf("paths with facts: %w", err)
	}
	defer rows.Close()

	paths := make(map[string]bool)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths[p] = true
	}
	return paths, rows.Err()
}

// GetSampleFacts returns up to limit facts ordered by most recently created.
func (db *DB) GetSampleFacts(limit int) ([]FactRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.conn.Query(`
		SELECT id, fact_text, source_path, chunk_id, confidence, created_at
		FROM facts
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("get sample facts: %w", err)
	}
	defer rows.Close()

	var facts []FactRecord
	for rows.Next() {
		var f FactRecord
		if err := rows.Scan(&f.ID, &f.FactText, &f.SourcePath, &f.ChunkID, &f.Confidence, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// HasFacts returns true if the facts table has any entries.
func (db *DB) HasFacts() bool {
	var exists int
	err := db.conn.QueryRow("SELECT EXISTS(SELECT 1 FROM facts LIMIT 1)").Scan(&exists)
	if err != nil {
		return false
	}
	return exists == 1
}

// serializeFactVec converts a float32 slice to little-endian bytes for sqlite-vec.
// This is the same encoding as serializeFloat32 in notes.go but kept as a
// package-private function to avoid exporting the low-level serialization detail.
func serializeFactVec(vec []float32) ([]byte, error) {
	data := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(data[i*4:(i+1)*4], math.Float32bits(v))
	}
	return data, nil
}
