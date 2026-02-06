package store

import "fmt"

// PinNote pins a note path so it always appears in context surfacing.
func (db *DB) PinNote(path string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO pinned_notes (path) VALUES (?)`,
		path,
	)
	if err != nil {
		return fmt.Errorf("pin note: %w", err)
	}
	return nil
}

// UnpinNote removes a pin from a note path.
func (db *DB) UnpinNote(path string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	res, err := db.conn.Exec(
		`DELETE FROM pinned_notes WHERE path = ?`,
		path,
	)
	if err != nil {
		return fmt.Errorf("unpin note: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("note is not pinned: %s", path)
	}
	return nil
}

// GetPinnedPaths returns all pinned note paths.
func (db *DB) GetPinnedPaths() ([]string, error) {
	rows, err := db.conn.Query(
		`SELECT path FROM pinned_notes ORDER BY pinned_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get pinned: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("scan pinned: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// IsPinned checks if a note path is pinned.
func (db *DB) IsPinned(path string) (bool, error) {
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM pinned_notes WHERE path = ?`,
		path,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check pinned: %w", err)
	}
	return count > 0, nil
}

// GetPinnedNotes returns the full NoteRecord for each pinned note.
// Returns deduplicated records (one per path, preferring chunk 0).
func (db *DB) GetPinnedNotes() ([]NoteRecord, error) {
	pinnedPaths, err := db.GetPinnedPaths()
	if err != nil {
		return nil, err
	}
	if len(pinnedPaths) == 0 {
		return nil, nil
	}

	var records []NoteRecord
	for _, path := range pinnedPaths {
		row := db.conn.QueryRow(
			`SELECT id, path, title, tags, domain, workstream, chunk_id, chunk_heading,
			        text, modified, content_hash, content_type, review_by, confidence, access_count
			 FROM vault_notes
			 WHERE path = ?
			 ORDER BY chunk_id ASC
			 LIMIT 1`,
			path,
		)
		var rec NoteRecord
		err := row.Scan(
			&rec.ID, &rec.Path, &rec.Title, &rec.Tags, &rec.Domain, &rec.Workstream,
			&rec.ChunkID, &rec.ChunkHeading, &rec.Text, &rec.Modified,
			&rec.ContentHash, &rec.ContentType, &rec.ReviewBy, &rec.Confidence, &rec.AccessCount,
		)
		if err != nil {
			continue // Note may have been deleted from vault but pin remains
		}
		records = append(records, rec)
	}
	return records, nil
}

// GetLatestHandoff returns the most recently modified handoff note.
func (db *DB) GetLatestHandoff() (*NoteRecord, error) {
	row := db.conn.QueryRow(
		`SELECT id, path, title, tags, domain, workstream, chunk_id, chunk_heading,
		        text, modified, content_hash, content_type, review_by, confidence, access_count
		 FROM vault_notes
		 WHERE content_type = 'handoff'
		 ORDER BY modified DESC
		 LIMIT 1`,
	)
	var rec NoteRecord
	err := row.Scan(
		&rec.ID, &rec.Path, &rec.Title, &rec.Tags, &rec.Domain, &rec.Workstream,
		&rec.ChunkID, &rec.ChunkHeading, &rec.Text, &rec.Modified,
		&rec.ContentHash, &rec.ContentType, &rec.ReviewBy, &rec.Confidence, &rec.AccessCount,
	)
	if err != nil {
		return nil, fmt.Errorf("no handoff notes found")
	}
	return &rec, nil
}
