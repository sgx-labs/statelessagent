package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestOpenPath_MigratesLegacyV5ToV6 ensures real on-disk upgrade behavior:
// a pre-v6 database opens cleanly, migrates to v6, and preserves note/search utility.
func TestOpenPath_MigratesLegacyV5ToV6(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-v5.db")
	if err := createLegacyV5Fixture(dbPath); err != nil {
		t.Fatalf("create legacy fixture: %v", err)
	}

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath upgrade: %v", err)
	}
	defer db.Close()

	if got := db.SchemaVersion(); got != 6 {
		t.Fatalf("schema version = %d, want 6", got)
	}

	var noteNodeCount int
	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM graph_nodes WHERE type = 'note' AND name = 'notes/legacy.md'",
	).Scan(&noteNodeCount); err != nil {
		t.Fatalf("count note nodes: %v", err)
	}
	if noteNodeCount != 1 {
		t.Fatalf("note node count = %d, want 1", noteNodeCount)
	}

	var agentNodeCount int
	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM graph_nodes WHERE type = 'agent' AND name = 'woody'",
	).Scan(&agentNodeCount); err != nil {
		t.Fatalf("count agent nodes: %v", err)
	}
	if agentNodeCount != 1 {
		t.Fatalf("agent node count = %d, want 1", agentNodeCount)
	}

	var producedEdgeCount int
	if err := db.Conn().QueryRow(`
		SELECT COUNT(*)
		FROM graph_edges e
		JOIN graph_nodes src ON src.id = e.source_id
		JOIN graph_nodes dst ON dst.id = e.target_id
		WHERE src.type = 'agent' AND src.name = 'woody'
		  AND dst.type = 'note' AND dst.name = 'notes/legacy.md'
		  AND e.relationship = 'produced'`,
	).Scan(&producedEdgeCount); err != nil {
		t.Fatalf("count produced edges: %v", err)
	}
	if producedEdgeCount != 1 {
		t.Fatalf("produced edge count = %d, want 1", producedEdgeCount)
	}

	notes, err := db.GetNoteByPath("notes/legacy.md")
	if err != nil {
		t.Fatalf("GetNoteByPath legacy note: %v", err)
	}
	if len(notes) == 0 {
		t.Fatal("expected legacy note rows after migration")
	}

	results, err := db.KeywordSearch(ExtractSearchTerms("legacy storage"), 5)
	if err != nil {
		t.Fatalf("KeywordSearch after migration: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected keyword search results after migration")
	}
}

func createLegacyV5Fixture(path string) error {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer conn.Close()

	stmts := []string{
		`CREATE TABLE schema_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE vault_notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			title TEXT NOT NULL,
			tags TEXT DEFAULT '[]',
			domain TEXT DEFAULT '',
			workstream TEXT DEFAULT '',
			agent TEXT,
			chunk_id INTEGER NOT NULL,
			chunk_heading TEXT NOT NULL,
			text TEXT NOT NULL,
			modified REAL NOT NULL,
			content_hash TEXT NOT NULL,
			content_type TEXT DEFAULT 'note',
			review_by TEXT DEFAULT '',
			confidence REAL DEFAULT 0.5,
			access_count INTEGER DEFAULT 0
		)`,
		`CREATE INDEX idx_vault_notes_path ON vault_notes(path)`,
		`CREATE TABLE claims (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			agent TEXT NOT NULL,
			type TEXT NOT NULL,
			claimed_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			return err
		}
	}

	if _, err := conn.Exec(`INSERT INTO schema_meta (key, value) VALUES ('schema_version', '5')`); err != nil {
		return err
	}

	if _, err := conn.Exec(`
		INSERT INTO vault_notes (id, path, title, tags, domain, workstream, agent, chunk_id, chunk_heading, text, modified, content_hash, content_type, confidence, access_count)
		VALUES
			(1, 'notes/legacy.md', 'Legacy Note', '["upgrade"]', 'engineering', 'core', 'woody', 0, '(full)', 'Legacy storage design and migration notes.', 1700000000, 'hash-legacy', 'note', 0.6, 0),
			(2, 'notes/legacy.md', 'Legacy Note', '["upgrade"]', 'engineering', 'core', 'woody', 1, 'Details', 'Details mention internal/store/db.go', 1700000000, 'hash-legacy', 'note', 0.6, 0),
			(3, 'notes/null-agent.md', 'Null Agent', '[]', '', '', NULL, 0, '(full)', 'Legacy row with NULL agent.', 1700000000, 'hash-null', 'note', 0.5, 0)
	`); err != nil {
		return err
	}

	return nil
}
