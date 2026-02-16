// Package store provides the SQLite + sqlite-vec storage layer.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/graph"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

// DB wraps a SQLite connection with sqlite-vec support.
type DB struct {
	conn         *sql.DB
	mu           sync.Mutex // serialize writes
	ftsAvailable bool       // true if FTS5 module is available
}

// Open opens or creates the database at the configured path.
func Open() (*DB, error) {
	return OpenPath(config.DBPath())
}

// OpenPath opens or creates the database at the given path.
func OpenPath(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	conn, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Verify sqlite-vec is loaded
	var vecVersion string
	if err := conn.QueryRow("SELECT vec_version()").Scan(&vecVersion); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sqlite-vec not available: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

// OpenMemory opens an in-memory database for testing.
func OpenMemory() (*DB, error) {
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying sql.DB for direct queries.
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// SessionStateGet retrieves a value from session_state by session ID and key.
// Returns empty string and false if not found.
func (db *DB) SessionStateGet(sessionID, key string) (string, bool) {
	var value string
	err := db.conn.QueryRow(
		`SELECT value FROM session_state WHERE session_id = ? AND key = ?`,
		sessionID, key,
	).Scan(&value)
	if err != nil {
		return "", false
	}
	return value, true
}

// SessionStateSet upserts a value in session_state.
func (db *DB) SessionStateSet(sessionID, key, value string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(
		`INSERT INTO session_state (session_id, key, value, updated_at)
		 VALUES (?, ?, ?, unixepoch())
		 ON CONFLICT(session_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		sessionID, key, value,
	)
	return err
}

// SessionStateCleanup removes session_state rows older than maxAge seconds.
func (db *DB) SessionStateCleanup(maxAgeSeconds int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(
		`DELETE FROM session_state WHERE updated_at < unixepoch() - ?`,
		maxAgeSeconds,
	)
	return err
}

func (db *DB) migrate() error {
	migrations := []string{
		// Schema metadata table — stores version, embedding info, etc.
		`CREATE TABLE IF NOT EXISTS schema_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS vault_notes (
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
		`CREATE INDEX IF NOT EXISTS idx_vault_notes_path ON vault_notes(path)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_notes_content_hash ON vault_notes(content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_notes_content_type ON vault_notes(content_type)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_notes_domain ON vault_notes(domain)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_notes_workstream ON vault_notes(workstream)`,
		// Composite indexes for common search query patterns:
		// FuzzyTitleSearch, KeywordSearch, RecentNotes all filter on chunk_id=0 and sort by modified DESC.
		`CREATE INDEX IF NOT EXISTS idx_vault_notes_chunk0_modified ON vault_notes(chunk_id, modified DESC)`,
		// GetContentHashes needs DISTINCT path, content_hash — covering index avoids full table scan.
		`CREATE INDEX IF NOT EXISTS idx_vault_notes_path_hash ON vault_notes(path, content_hash)`,

		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vault_notes_vec USING vec0(
			note_id INTEGER PRIMARY KEY,
			embedding float[%d]
		)`, config.EmbeddingDim()),

		`CREATE TABLE IF NOT EXISTS session_log (
			session_id TEXT PRIMARY KEY,
			started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL,
			handoff_path TEXT DEFAULT '',
			machine TEXT DEFAULT '',
			files_changed TEXT DEFAULT '[]',
			summary TEXT DEFAULT ''
		)`,

		`CREATE TABLE IF NOT EXISTS context_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			hook_name TEXT NOT NULL,
			injected_paths TEXT DEFAULT '[]',
			estimated_tokens INTEGER DEFAULT 0,
			was_referenced INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_context_usage_session ON context_usage(session_id)`,

		`CREATE TABLE IF NOT EXISTS session_state (
			session_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (session_id, key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_state_updated ON session_state(updated_at)`,

		`CREATE TABLE IF NOT EXISTS context_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			prompt_snippet TEXT NOT NULL,
			mode TEXT NOT NULL,
			jaccard_score REAL DEFAULT -1,
			decision TEXT NOT NULL,
			injected_paths TEXT DEFAULT '[]'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_context_decisions_session ON context_decisions(session_id)`,

		`CREATE TABLE IF NOT EXISTS pinned_notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL UNIQUE,
			pinned_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,

		// Milestones track user progress and which tips have been shown
		`CREATE TABLE IF NOT EXISTS milestones (
			key TEXT PRIMARY KEY,
			shown_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}

	for _, m := range migrations {
		if _, err := db.conn.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	// Version-gated migrations (run once, tracked in schema_meta)
	currentVersion := db.SchemaVersion()
	versionedMigrations := []struct {
		version int
		fn      func() error
	}{
		{1, db.migrateV1}, // establishes version tracking baseline
		{2, db.migrateV2}, // FTS5 full-text search table
		{3, db.migrateV3}, // session recovery tracking
		{4, db.migrateV4}, // agent attribution metadata
		{5, db.migrateV5}, // multi-agent claims table
		{6, db.migrateV6}, // knowledge graph tables
	}
	for _, m := range versionedMigrations {
		if currentVersion < m.version {
			if err := m.fn(); err != nil {
				return fmt.Errorf("migration v%d: %w", m.version, err)
			}
			if err := db.SetMeta("schema_version", strconv.Itoa(m.version)); err != nil {
				return fmt.Errorf("record migration v%d: %w", m.version, err)
			}
		}
	}

	return nil
}

// migrateV1 is a no-op that establishes version 1 as the baseline.
func (db *DB) migrateV1() error {
	return nil
}

// migrateV2 creates an FTS5 virtual table for keyword fallback search.
// Uses content sync (content=vault_notes) so the FTS index stores only
// tokens, not full text — no storage duplication.
// FTS5 may not be available on all SQLite builds (e.g., macOS system SQLite
// in-memory databases). Migration is best-effort — failure is non-fatal.
func (db *DB) migrateV2() error {
	_, err := db.conn.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS vault_notes_fts USING fts5(
		path, title, text,
		content=vault_notes, content_rowid=id
	)`)
	if err != nil {
		// FTS5 not available — skip silently, keyword fallback will use LIKE-based search
		db.ftsAvailable = false
		return nil
	}
	db.ftsAvailable = true
	// Populate from existing data
	_, _ = db.conn.Exec(`INSERT INTO vault_notes_fts(vault_notes_fts) VALUES('rebuild')`)
	return nil
}

// migrateV3 creates a session_recovery table for tracking how session context is recovered.
func (db *DB) migrateV3() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS session_recovery (
		session_id TEXT PRIMARY KEY,
		recovered_from_session TEXT NOT NULL DEFAULT '',
		recovery_source TEXT NOT NULL,
		completeness REAL NOT NULL,
		recovered_at INTEGER NOT NULL DEFAULT (unixepoch())
	)`)
	return err
}

// migrateV4 adds optional agent attribution to notes.
func (db *DB) migrateV4() error {
	if !db.hasColumn("vault_notes", "agent") {
		if _, err := db.conn.Exec(`ALTER TABLE vault_notes ADD COLUMN agent TEXT`); err != nil {
			return err
		}
	}
	_, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_vault_notes_agent ON vault_notes(agent)`)
	return err
}

// migrateV5 creates advisory multi-agent file claims.
func (db *DB) migrateV5() error {
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS claims (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL,
		agent TEXT NOT NULL,
		type TEXT NOT NULL CHECK(type IN ('read', 'write')),
		claimed_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		UNIQUE(path, agent, type)
	)`); err != nil {
		return err
	}
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_claims_expires_at ON claims(expires_at)`); err != nil {
		return err
	}
	_, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_claims_path ON claims(path)`)
	return err
}

// SchemaVersion returns the current schema version (0 if unset).
func (db *DB) SchemaVersion() int {
	v, ok := db.GetMeta("schema_version")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// GetMeta reads a value from the schema_meta table. Returns ("", false) if not found.
func (db *DB) GetMeta(key string) (string, bool) {
	var value string
	err := db.conn.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", false
	}
	return value, true
}

// SetMeta writes a key-value pair to the schema_meta table.
func (db *DB) SetMeta(key, value string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(
		`INSERT INTO schema_meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// hasColumn reports whether a table currently has a column.
func (db *DB) hasColumn(table, column string) bool {
	rows, err := db.conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid      int
			name     string
			colType  string
			notNull  int
			defaultV sql.NullString
			primaryK int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultV, &primaryK); err != nil {
			continue
		}
		if strings.EqualFold(name, column) {
			return true
		}
	}
	return false
}

// SetEmbeddingMeta records the current embedding provider, model, and dimensions.
// Called after a successful reindex to track what was used.
func (db *DB) SetEmbeddingMeta(provider, model string, dims int) error {
	if err := db.SetMeta("embed_provider", provider); err != nil {
		return err
	}
	if err := db.SetMeta("embed_model", model); err != nil {
		return err
	}
	return db.SetMeta("embed_dims", strconv.Itoa(dims))
}

// FTSAvailable returns true if the FTS5 module is available.
func (db *DB) FTSAvailable() bool {
	return db.ftsAvailable
}

// RebuildFTS rebuilds the FTS5 index from the vault_notes table.
// Called after bulk inserts during reindex. No-op if FTS5 is unavailable.
func (db *DB) RebuildFTS() error {
	if !db.ftsAvailable {
		return nil
	}
	_, err := db.conn.Exec(`INSERT INTO vault_notes_fts(vault_notes_fts) VALUES('rebuild')`)
	return err
}

// IntegrityCheck runs SQLite PRAGMA integrity_check and returns an error if corruption is detected.
func (db *DB) IntegrityCheck() error {
	var result string
	err := db.conn.QueryRow("PRAGMA integrity_check").Scan(&result)
	if err != nil {
		return fmt.Errorf("integrity check query failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	return nil
}

// LastReindexTime returns the timestamp of the last reindex, or empty string if unknown.
func (db *DB) LastReindexTime() string {
	v, ok := db.GetMeta("last_reindex_time")
	if !ok {
		return ""
	}
	return v
}

// CheckEmbeddingMeta compares the given embedding config against what was used
// at last reindex. Returns an error if there's a mismatch. Returns nil if no
// stored metadata exists (pre-migration DB or first index).
func (db *DB) CheckEmbeddingMeta(provider, model string, dims int) error {
	storedProvider, hasProvider := db.GetMeta("embed_provider")
	storedModel, hasModel := db.GetMeta("embed_model")
	storedDimsStr, hasDims := db.GetMeta("embed_dims")

	// No stored metadata = compatible (never block on upgrade or first use)
	if !hasProvider && !hasModel && !hasDims {
		return nil
	}

	storedDims, _ := strconv.Atoi(storedDimsStr)

	// Check for dimension mismatch (most critical — causes garbage results)
	if hasDims && dims > 0 && storedDims > 0 && storedDims != dims {
		return fmt.Errorf("embedding dimensions changed from %d to %d — run 'same reindex --force' to rebuild", storedDims, dims)
	}

	// Check for provider/model mismatch
	if hasProvider && hasModel && (storedProvider != provider || storedModel != model) {
		return fmt.Errorf("embedding model changed from %s/%s to %s/%s — run 'same reindex --force' to rebuild",
			storedProvider, storedModel, provider, model)
	}

	return nil
}

// RecordRecovery logs how a session's context was recovered for reliability monitoring.
func (db *DB) RecordRecovery(sessionID, recoveredFromSession, source string, completeness float64) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO session_recovery (session_id, recovered_from_session, recovery_source, completeness)
		 VALUES (?, ?, ?, ?)`,
		sessionID, recoveredFromSession, source, completeness,
	)
	return err
}

// migrateV6 initializes the knowledge graph tables and populates them from existing notes.
func (db *DB) migrateV6() error {
	for _, stmt := range graph.GraphSchemaSQL() {
		if _, err := db.conn.Exec(stmt); err != nil {
			return fmt.Errorf("graph schema: %w", err)
		}
	}
	return graph.PopulateFromExistingNotes(db.conn)
}
