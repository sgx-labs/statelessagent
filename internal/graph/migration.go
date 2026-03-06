package graph

import (
	"database/sql"
	"fmt"
)

// GraphSchemaSQL returns the SQL statements to create the graph tables.
func GraphSchemaSQL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS graph_nodes (
			id INTEGER PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			note_id INTEGER,
			properties TEXT DEFAULT '{}',
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			FOREIGN KEY (note_id) REFERENCES vault_notes(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_graph_nodes_type_name ON graph_nodes(type, name)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_nodes_type ON graph_nodes(type)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_nodes_note_id ON graph_nodes(note_id)`,

		`CREATE TABLE IF NOT EXISTS graph_edges (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL,
			target_id INTEGER NOT NULL,
			relationship TEXT NOT NULL,
			weight REAL DEFAULT 1.0,
			properties TEXT DEFAULT '{}',
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			FOREIGN KEY (source_id) REFERENCES graph_nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES graph_nodes(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_graph_edges_src_tgt_rel ON graph_edges(source_id, target_id, relationship)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_edges_target ON graph_edges(target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_edges_relationship ON graph_edges(relationship)`,
	}
}

// PopulateFromExistingNotes bootstraps the graph from existing vault notes.
func PopulateFromExistingNotes(conn *sql.DB) error {
	// 1. Insert note nodes from vault_notes (chunk_id=0 only, one node per note path)
	queryNodes := `
		INSERT INTO graph_nodes (type, name, note_id)
		SELECT 'note', path, id
		FROM vault_notes
		WHERE chunk_id = 0
		ON CONFLICT(type, name) DO UPDATE SET note_id = excluded.note_id`

	if _, err := conn.Exec(queryNodes); err != nil {
		return fmt.Errorf("populate notes: %w", err)
	}

	// 2. Insert agent nodes from DISTINCT agent values
	queryAgents := `
		INSERT OR IGNORE INTO graph_nodes (type, name)
		SELECT DISTINCT 'agent', agent
		FROM vault_notes
		WHERE agent != '' AND agent IS NOT NULL`

	if _, err := conn.Exec(queryAgents); err != nil {
		return fmt.Errorf("populate agents: %w", err)
	}

	// 3. Create "produced" edges from agent → note
	// We join graph_nodes to get IDs
	queryEdges := `
		INSERT OR IGNORE INTO graph_edges (source_id, target_id, relationship)
		SELECT a.id, n.id, 'produced'
		FROM vault_notes vn
		JOIN graph_nodes a ON a.type = 'agent' AND a.name = vn.agent
		JOIN graph_nodes n ON n.type = 'note' AND n.note_id = vn.id
		WHERE vn.chunk_id = 0 AND vn.agent != ''`

	if _, err := conn.Exec(queryEdges); err != nil {
		return fmt.Errorf("populate produced edges: %w", err)
	}

	return nil
}

// RebuildStats captures rebuild output for UX/reporting.
type RebuildStats struct {
	NotesProcessed int
	NotesSucceeded int
	NotesFailed    int
	TotalNodes     int
	TotalEdges     int
	Errors         []string // per-file error messages (only when ContinueOnError is true)
}

// RebuildOptions controls rebuild behavior.
type RebuildOptions struct {
	// ContinueOnError makes the rebuild continue past extraction failures
	// instead of aborting on the first error. Default is true.
	ContinueOnError bool
}

// RebuildFromIndexedNotes clears graph tables, then rebuilds nodes/edges from
// indexed notes (including regex/decision/agent extraction).
//
// If extractor is nil, a default regex-only extractor is used.
// If opts is nil, defaults are used (ContinueOnError=true).
func RebuildFromIndexedNotes(conn *sql.DB, extractor *Extractor, opts *RebuildOptions) (*RebuildStats, error) {
	if extractor == nil {
		extractor = NewExtractor(NewDB(conn))
	}
	if opts == nil {
		opts = &RebuildOptions{ContinueOnError: true}
	}

	if _, err := conn.Exec("DELETE FROM graph_edges"); err != nil {
		return nil, fmt.Errorf("clear graph edges: %w", err)
	}
	if _, err := conn.Exec("DELETE FROM graph_nodes"); err != nil {
		return nil, fmt.Errorf("clear graph nodes: %w", err)
	}

	rows, err := conn.Query(`
		SELECT root.id, root.path, COALESCE(root.agent, ''),
			COALESCE((
				SELECT group_concat(ch.text, char(10) || char(10))
				FROM (
					SELECT text
					FROM vault_notes
					WHERE path = root.path
					ORDER BY chunk_id
				) ch
			), '')
		FROM vault_notes root
		WHERE root.chunk_id = 0
		ORDER BY root.path`)
	if err != nil {
		return nil, fmt.Errorf("load indexed notes: %w", err)
	}
	defer rows.Close()

	type indexedNote struct {
		id      int64
		path    string
		agent   string
		content string
	}
	var notes []indexedNote

	stats := &RebuildStats{}
	for rows.Next() {
		var (
			noteID  int64
			path    string
			agent   string
			content string
		)
		if err := rows.Scan(&noteID, &path, &agent, &content); err != nil {
			return nil, fmt.Errorf("scan indexed note: %w", err)
		}
		notes = append(notes, indexedNote{
			id:      noteID,
			path:    path,
			agent:   agent,
			content: content,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate indexed notes: %w", err)
	}

	for _, n := range notes {
		stats.NotesProcessed++
		if err := extractor.ExtractFromNote(n.id, n.path, n.content, n.agent); err != nil {
			if opts.ContinueOnError {
				stats.NotesFailed++
				stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", n.path, err))
				continue
			}
			return nil, fmt.Errorf("extract graph for %s: %w", n.path, err)
		}
		stats.NotesSucceeded++
	}

	gdb := NewDB(conn)
	gstats, err := gdb.GetStats()
	if err != nil {
		return nil, fmt.Errorf("final graph stats: %w", err)
	}
	stats.TotalNodes = gstats.TotalNodes
	stats.TotalEdges = gstats.TotalEdges

	return stats, nil
}
