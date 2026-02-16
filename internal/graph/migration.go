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
