package graph

import (
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func setupMigrationDB(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec(`
		CREATE TABLE vault_notes (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL,
			agent TEXT,
			chunk_id INTEGER NOT NULL,
			text TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create vault_notes: %v", err)
	}

	for _, stmt := range GraphSchemaSQL() {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("graph schema: %v", err)
		}
	}

	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestRebuildFromIndexedNotes_RecreatesRelationships(t *testing.T) {
	conn := setupMigrationDB(t)

	if _, err := conn.Exec(`
		INSERT INTO vault_notes (id, path, agent, chunk_id, text) VALUES
			(1, 'notes/architecture.md', 'woody', 0, '# Architecture'),
			(2, 'notes/architecture.md', 'woody', 1, 'We decided: use queue workers. See notes/queue.md and internal/store/db.go.'),
			(3, 'notes/queue.md', 'buzz', 0, '# Queue Design')
	`); err != nil {
		t.Fatalf("seed vault_notes: %v", err)
	}

	if _, err := conn.Exec(`
		INSERT INTO graph_nodes (type, name, properties, created_at)
		VALUES ('file', 'stale/path.go', '{}', unixepoch())
	`); err != nil {
		t.Fatalf("seed stale graph node: %v", err)
	}

	stats, err := RebuildFromIndexedNotes(conn, nil)
	if err != nil {
		t.Fatalf("RebuildFromIndexedNotes: %v", err)
	}
	if stats.NotesProcessed != 2 {
		t.Fatalf("NotesProcessed = %d, want 2", stats.NotesProcessed)
	}
	if stats.TotalNodes < 6 {
		t.Fatalf("TotalNodes = %d, want at least 6", stats.TotalNodes)
	}
	if stats.TotalEdges < 4 {
		t.Fatalf("TotalEdges = %d, want at least 4", stats.TotalEdges)
	}

	gdb := NewDB(conn)
	if _, err := gdb.FindNode(NodeFile, "stale/path.go"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale node should be removed, got err=%v", err)
	}

	if _, err := gdb.FindNode(NodeAgent, "woody"); err != nil {
		t.Fatalf("expected agent node woody: %v", err)
	}
	if _, err := gdb.FindNode(NodeDecision, "use queue workers. See notes/queue.md and internal/store/db.go"); err != nil {
		t.Fatalf("expected decision node: %v", err)
	}
	if _, err := gdb.FindNode(NodeFile, "internal/store/db.go"); err != nil {
		t.Fatalf("expected file node from regex extraction: %v", err)
	}
	if _, err := gdb.FindNode(NodeNote, "notes/queue.md"); err != nil {
		t.Fatalf("expected note node for markdown reference: %v", err)
	}

	var references int
	if err := conn.QueryRow(`
		SELECT COUNT(*)
		FROM graph_edges e
		JOIN graph_nodes src ON src.id = e.source_id
		JOIN graph_nodes dst ON dst.id = e.target_id
		WHERE src.type = 'note' AND src.name = 'notes/architecture.md'
		  AND dst.type = 'note' AND dst.name = 'notes/queue.md'
		  AND e.relationship = 'references'
	`).Scan(&references); err != nil {
		t.Fatalf("count reference edge: %v", err)
	}
	if references != 1 {
		t.Fatalf("references edge count = %d, want 1", references)
	}
}
