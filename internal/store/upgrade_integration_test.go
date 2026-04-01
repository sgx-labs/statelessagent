package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// TestUpgradeV9ToV10 verifies the v0.12.0 → v0.12.1 upgrade path.
// A v9 database (the final schema for v0.12.0) is created with sample data,
// then opened with the current binary which migrates it to v10. The test
// verifies that all data survives, the new contradiction_detail column exists
// and defaults correctly, and that post-migration operations work.
func TestUpgradeV9ToV10(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v9-to-v10.db")
	if err := createV9Fixture(dbPath); err != nil {
		t.Fatalf("create v9 fixture: %v", err)
	}

	// Open triggers migrate() which should run migrateV10
	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath v9→v10: %v", err)
	}
	defer db.Close()

	// --- Schema version should now be 11 ---
	if got := db.SchemaVersion(); got != 11 {
		t.Fatalf("schema version = %d, want 11", got)
	}

	// --- All 5 notes should still exist ---
	var noteCount int
	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0",
	).Scan(&noteCount); err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if noteCount != 5 {
		t.Fatalf("note count = %d, want 5", noteCount)
	}

	// --- trust_state preserved for all notes ---
	trustChecks := []struct {
		path string
		want string
	}{
		{"notes/alpha.md", "validated"},
		{"notes/beta.md", "stale"},
		{"notes/gamma.md", "contradicted"},
		{"notes/delta.md", "unknown"},
		{"notes/epsilon.md", "validated"},
	}
	for _, tc := range trustChecks {
		var got string
		if err := db.Conn().QueryRow(
			"SELECT trust_state FROM vault_notes WHERE path = ? AND chunk_id = 0",
			tc.path,
		).Scan(&got); err != nil {
			t.Fatalf("query trust_state for %s: %v", tc.path, err)
		}
		if got != tc.want {
			t.Errorf("trust_state for %s = %q, want %q", tc.path, got, tc.want)
		}
	}

	// --- Provenance (note_sources) preserved ---
	sources, err := db.GetSourcesForNote("notes/alpha.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote alpha: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source for alpha, got %d", len(sources))
	}
	if sources[0].SourcePath != "internal/store/db.go" {
		t.Errorf("alpha source path = %q, want %q", sources[0].SourcePath, "internal/store/db.go")
	}

	sources, err = db.GetSourcesForNote("notes/beta.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote beta: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source for beta, got %d", len(sources))
	}
	if sources[0].SourcePath != "https://example.com/api-docs" {
		t.Errorf("beta source path = %q, want %q", sources[0].SourcePath, "https://example.com/api-docs")
	}
	if sources[0].SourceType != "url" {
		t.Errorf("beta source type = %q, want %q", sources[0].SourceType, "url")
	}

	// --- contradiction_detail column exists and defaults to empty string ---
	if !db.hasColumn("vault_notes", "contradiction_detail") {
		t.Fatal("expected contradiction_detail column to exist after migration")
	}
	var detail string
	if err := db.Conn().QueryRow(
		"SELECT contradiction_detail FROM vault_notes WHERE path = 'notes/alpha.md' AND chunk_id = 0",
	).Scan(&detail); err != nil {
		t.Fatalf("query contradiction_detail: %v", err)
	}
	if detail != "" {
		t.Errorf("contradiction_detail default = %q, want empty string", detail)
	}

	// --- SetContradicted works on migrated DB ---
	if err := db.SetContradicted("notes/delta.md", "factual"); err != nil {
		t.Fatalf("SetContradicted on migrated DB: %v", err)
	}
	var trustState, cDetail string
	if err := db.Conn().QueryRow(
		"SELECT trust_state, contradiction_detail FROM vault_notes WHERE path = 'notes/delta.md' AND chunk_id = 0",
	).Scan(&trustState, &cDetail); err != nil {
		t.Fatalf("query after SetContradicted: %v", err)
	}
	if trustState != "contradicted" {
		t.Errorf("trust_state after SetContradicted = %q, want %q", trustState, "contradicted")
	}
	if cDetail != "factual" {
		t.Errorf("contradiction_detail after SetContradicted = %q, want %q", cDetail, "factual")
	}

	// --- KeywordSearch still works after migration ---
	results, err := db.KeywordSearch(ExtractSearchTerms("architecture migration"), 10)
	if err != nil {
		t.Fatalf("KeywordSearch after migration: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected keyword search results after migration, got 0")
	}

	// --- GetContradictionSummary returns correct counts ---
	// At this point: gamma was contradicted from fixture, delta just got set to
	// contradicted with "factual". gamma has no contradiction_detail (empty).
	breakdown, err := db.GetContradictionSummary()
	if err != nil {
		t.Fatalf("GetContradictionSummary: %v", err)
	}
	if breakdown.Factual != 1 {
		t.Errorf("contradiction factual = %d, want 1", breakdown.Factual)
	}
	if breakdown.Untyped != 1 {
		t.Errorf("contradiction untyped = %d, want 1 (gamma has no detail)", breakdown.Untyped)
	}

	// --- GetTrustStateSummary also works correctly ---
	summary, err := db.GetTrustStateSummary()
	if err != nil {
		t.Fatalf("GetTrustStateSummary: %v", err)
	}
	// alpha=validated, beta=stale, gamma=contradicted, delta=contradicted (just set), epsilon=validated
	if summary.Validated != 2 {
		t.Errorf("validated count = %d, want 2", summary.Validated)
	}
	if summary.Stale != 1 {
		t.Errorf("stale count = %d, want 1", summary.Stale)
	}
	if summary.Contradicted != 2 {
		t.Errorf("contradicted count = %d, want 2", summary.Contradicted)
	}
	if summary.Unknown != 0 {
		t.Errorf("unknown count = %d, want 0", summary.Unknown)
	}
}

// TestUpgradeV10OpenedByV9Binary verifies what happens if a v10 database
// (with the contradiction_detail column) is opened by code that only knows
// schema v9. The migration system should reject it with a clear error because
// the schema version exceeds what the binary supports.
func TestUpgradeV10OpenedByV9Binary(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v10-future.db")
	if err := createV10Fixture(dbPath); err != nil {
		t.Fatalf("create v10 fixture: %v", err)
	}

	// Simulate a "v9-only" binary by temporarily setting maxSchemaVersion to 9.
	// Since maxSchemaVersion is a package-level const, we simulate this by
	// opening the DB directly and checking the version guard logic.
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	// Verify the DB is at version 10
	var versionStr string
	if err := conn.QueryRow(
		"SELECT value FROM schema_meta WHERE key = 'schema_version'",
	).Scan(&versionStr); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if versionStr != "11" {
		t.Fatalf("fixture schema version = %s, want 11", versionStr)
	}

	// Now set it to version 99 to simulate a future version
	if _, err := conn.Exec(
		"UPDATE schema_meta SET value = '99' WHERE key = 'schema_version'",
	); err != nil {
		t.Fatalf("set future version: %v", err)
	}
	conn.Close()

	// OpenPath should fail with a clear version error
	db, err := OpenPath(dbPath)
	if err == nil {
		db.Close()
		t.Fatal("expected error opening DB with schema version 99, got nil")
	}

	// The error should mention the version mismatch
	errMsg := err.Error()
	if !(contains(errMsg, "newer") || contains(errMsg, "version") || contains(errMsg, "upgrade")) {
		t.Errorf("error should mention version mismatch, got: %s", errMsg)
	}
}

// TestUpgradeV9ToV10_MigrationIdempotent verifies that running the migration
// twice does not corrupt data or produce errors.
func TestUpgradeV9ToV10_MigrationIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v9-idempotent.db")
	if err := createV9Fixture(dbPath); err != nil {
		t.Fatalf("create v9 fixture: %v", err)
	}

	// First open: migrates from v9 to v10
	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("first OpenPath: %v", err)
	}

	// Insert some post-migration data
	if err := db.SetContradicted("notes/alpha.md", "preference"); err != nil {
		t.Fatalf("SetContradicted: %v", err)
	}
	db.Close()

	// Second open: should detect v10 and skip migrateV10
	db, err = OpenPath(dbPath)
	if err != nil {
		t.Fatalf("second OpenPath: %v", err)
	}
	defer db.Close()

	if got := db.SchemaVersion(); got != 11 {
		t.Fatalf("schema version after second open = %d, want 11", got)
	}

	// Data from first session should survive
	var cDetail string
	if err := db.Conn().QueryRow(
		"SELECT contradiction_detail FROM vault_notes WHERE path = 'notes/alpha.md' AND chunk_id = 0",
	).Scan(&cDetail); err != nil {
		t.Fatalf("query after reopen: %v", err)
	}
	if cDetail != "preference" {
		t.Errorf("contradiction_detail after reopen = %q, want %q", cDetail, "preference")
	}

	// Note count unchanged
	var noteCount int
	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0",
	).Scan(&noteCount); err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if noteCount != 5 {
		t.Fatalf("note count = %d, want 5 (data loss on reopen)", noteCount)
	}
}

// contains is a helper to check substring presence.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// createV9Fixture builds a v9-era database (v0.12.0 final schema) with sample data.
// This includes: schema_meta, vault_notes with trust_state, note_sources,
// suppressed column, graph tables, claims, session_log with v7 columns, and
// all indexes that would exist after a full v1-v9 migration chain.
func createV9Fixture(path string) error {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer conn.Close()

	stmts := []string{
		// --- Core tables ---
		`CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,

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
			access_count INTEGER DEFAULT 0,
			suppressed INTEGER DEFAULT 0,
			trust_state TEXT DEFAULT 'unknown'
		)`,
		`CREATE INDEX idx_vault_notes_path ON vault_notes(path)`,
		`CREATE INDEX idx_vault_notes_content_hash ON vault_notes(content_hash)`,
		`CREATE INDEX idx_vault_notes_content_type ON vault_notes(content_type)`,
		`CREATE INDEX idx_vault_notes_domain ON vault_notes(domain)`,
		`CREATE INDEX idx_vault_notes_workstream ON vault_notes(workstream)`,
		`CREATE INDEX idx_vault_notes_chunk0_modified ON vault_notes(chunk_id, modified DESC)`,
		`CREATE INDEX idx_vault_notes_path_hash ON vault_notes(path, content_hash)`,
		`CREATE INDEX idx_vault_notes_chunk0_path_hash ON vault_notes(chunk_id, path, content_hash)`,
		`CREATE INDEX idx_vault_notes_agent ON vault_notes(agent)`,

		// --- note_sources (v9) ---
		`CREATE TABLE note_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			note_path TEXT NOT NULL,
			source_path TEXT NOT NULL,
			source_type TEXT NOT NULL DEFAULT 'file',
			source_hash TEXT DEFAULT '',
			captured_at INTEGER NOT NULL DEFAULT (unixepoch()),
			UNIQUE(note_path, source_path)
		)`,
		`CREATE INDEX idx_note_sources_note_path ON note_sources(note_path)`,
		`CREATE INDEX idx_note_sources_source_path ON note_sources(source_path)`,

		// --- session_log with v7 columns ---
		`CREATE TABLE session_log (
			session_id TEXT PRIMARY KEY,
			started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL,
			handoff_path TEXT DEFAULT '',
			machine TEXT DEFAULT '',
			files_changed TEXT DEFAULT '[]',
			summary TEXT DEFAULT '',
			entry_kind TEXT NOT NULL DEFAULT 'session',
			hook_timestamp INTEGER NOT NULL DEFAULT 0,
			hook_name TEXT DEFAULT '',
			hook_status TEXT DEFAULT '',
			surfaced_notes INTEGER DEFAULT 0,
			estimated_tokens INTEGER DEFAULT 0,
			error_message TEXT DEFAULT '',
			note_paths TEXT DEFAULT '[]',
			detail TEXT DEFAULT '',
			hook_session_id TEXT DEFAULT ''
		)`,
		`CREATE INDEX idx_session_log_entry_kind_time ON session_log(entry_kind, hook_timestamp DESC)`,
		`CREATE INDEX idx_session_log_hook_session ON session_log(hook_session_id)`,

		// --- Other tables ---
		`CREATE TABLE context_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			hook_name TEXT NOT NULL,
			injected_paths TEXT DEFAULT '[]',
			estimated_tokens INTEGER DEFAULT 0,
			was_referenced INTEGER DEFAULT 0
		)`,
		`CREATE INDEX idx_context_usage_session ON context_usage(session_id)`,

		`CREATE TABLE session_state (
			session_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (session_id, key)
		)`,
		`CREATE INDEX idx_session_state_updated ON session_state(updated_at)`,

		`CREATE TABLE context_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			prompt_snippet TEXT NOT NULL,
			mode TEXT NOT NULL,
			jaccard_score REAL DEFAULT -1,
			decision TEXT NOT NULL,
			injected_paths TEXT DEFAULT '[]'
		)`,
		`CREATE INDEX idx_context_decisions_session ON context_decisions(session_id)`,

		`CREATE TABLE pinned_notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL UNIQUE,
			pinned_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,

		`CREATE TABLE milestones (
			key TEXT PRIMARY KEY,
			shown_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,

		`CREATE TABLE claims (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			agent TEXT NOT NULL,
			type TEXT NOT NULL CHECK(type IN ('read', 'write')),
			claimed_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			UNIQUE(path, agent, type)
		)`,
		`CREATE INDEX idx_claims_expires_at ON claims(expires_at)`,
		`CREATE INDEX idx_claims_path ON claims(path)`,

		`CREATE TABLE session_recovery (
			session_id TEXT PRIMARY KEY,
			recovered_from_session TEXT NOT NULL DEFAULT '',
			recovery_source TEXT NOT NULL,
			completeness REAL NOT NULL,
			recovered_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,

		// --- Graph tables (v6) ---
		`CREATE TABLE graph_nodes (
			id INTEGER PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			note_id INTEGER,
			properties TEXT DEFAULT '{}',
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE UNIQUE INDEX idx_graph_nodes_type_name ON graph_nodes(type, name)`,
		`CREATE TABLE graph_edges (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL,
			target_id INTEGER NOT NULL,
			relationship TEXT NOT NULL,
			weight REAL DEFAULT 1.0,
			properties TEXT DEFAULT '{}',
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("exec fixture stmt: %w — SQL: %s", err, stmt)
		}
	}

	// Set schema version to 9
	if _, err := conn.Exec(
		`INSERT INTO schema_meta (key, value) VALUES ('schema_version', '9')`,
	); err != nil {
		return err
	}

	// --- Insert 5 notes with different trust states ---
	notes := []struct {
		id    int
		path  string
		title string
		text  string
		trust string
		agent string
	}{
		{1, "notes/alpha.md", "Architecture Overview", "Architecture decisions and migration notes for the storage layer.", "validated", "woody"},
		{2, "notes/beta.md", "API Documentation", "REST API endpoints for the memory service.", "stale", "buzz"},
		{3, "notes/gamma.md", "Config Defaults", "Default configuration values that may conflict.", "contradicted", "woody"},
		{4, "notes/delta.md", "Build Notes", "Build system setup and dependency management.", "unknown", ""},
		{5, "notes/epsilon.md", "Test Patterns", "Testing patterns and conventions used in the project.", "validated", "rex"},
	}
	for _, n := range notes {
		agentVal := sql.NullString{String: n.agent, Valid: n.agent != ""}
		if _, err := conn.Exec(
			`INSERT INTO vault_notes (id, path, title, tags, domain, workstream, agent, chunk_id, chunk_heading, text, modified, content_hash, content_type, confidence, access_count, suppressed, trust_state)
			 VALUES (?, ?, ?, '["test"]', 'engineering', 'core', ?, 0, '(full)', ?, 1700000000, ?, 'note', 0.7, 0, 0, ?)`,
			n.id, n.path, n.title, agentVal, n.text, "hash-"+n.path, n.trust,
		); err != nil {
			return err
		}
	}

	// --- Insert provenance sources for 2 notes ---
	if _, err := conn.Exec(
		`INSERT INTO note_sources (note_path, source_path, source_type, source_hash)
		 VALUES ('notes/alpha.md', 'internal/store/db.go', 'file', 'abc123')`,
	); err != nil {
		return err
	}
	if _, err := conn.Exec(
		`INSERT INTO note_sources (note_path, source_path, source_type, source_hash)
		 VALUES ('notes/beta.md', 'https://example.com/api-docs', 'url', 'url-hash-789')`,
	); err != nil {
		return err
	}

	return nil
}

// createV10Fixture builds a fully migrated v10 database for testing the
// "future version" error path.
func createV10Fixture(path string) error {
	// Start from v9 and let OpenPath migrate it
	if err := createV9Fixture(path); err != nil {
		return err
	}
	db, err := OpenPath(path)
	if err != nil {
		return err
	}
	return db.Close()
}
