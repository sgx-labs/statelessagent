package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordSource(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Record a single source
	if err := db.RecordSource("notes/arch.md", "internal/store/db.go", "file", "abc123"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	// Query it back
	sources, err := db.GetSourcesForNote("notes/arch.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].NotePath != "notes/arch.md" {
		t.Errorf("expected note_path 'notes/arch.md', got %q", sources[0].NotePath)
	}
	if sources[0].SourcePath != "internal/store/db.go" {
		t.Errorf("expected source_path 'internal/store/db.go', got %q", sources[0].SourcePath)
	}
	if sources[0].SourceType != "file" {
		t.Errorf("expected source_type 'file', got %q", sources[0].SourceType)
	}
	if sources[0].SourceHash != "abc123" {
		t.Errorf("expected source_hash 'abc123', got %q", sources[0].SourceHash)
	}
	if sources[0].CapturedAt == 0 {
		t.Error("expected captured_at to be set")
	}

	// Upsert same source with new hash — should update, not duplicate
	if err := db.RecordSource("notes/arch.md", "internal/store/db.go", "file", "def456"); err != nil {
		t.Fatalf("RecordSource upsert: %v", err)
	}
	sources, err = db.GetSourcesForNote("notes/arch.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote after upsert: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source after upsert, got %d", len(sources))
	}
	if sources[0].SourceHash != "def456" {
		t.Errorf("expected updated hash 'def456', got %q", sources[0].SourceHash)
	}
}

func TestRecordSources_Batch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	sources := []NoteSource{
		{SourcePath: "internal/store/db.go", SourceType: "file", SourceHash: "hash1"},
		{SourcePath: "internal/store/store.go", SourceType: "file", SourceHash: "hash2"},
		{SourcePath: "https://example.com/doc", SourceType: "url", SourceHash: "hash3"},
	}
	if err := db.RecordSources("notes/overview.md", sources); err != nil {
		t.Fatalf("RecordSources: %v", err)
	}

	got, err := db.GetSourcesForNote("notes/overview.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(got))
	}

	// Verify ordering (by source_path ASC)
	expectedPaths := []string{
		"https://example.com/doc",
		"internal/store/db.go",
		"internal/store/store.go",
	}
	for i, want := range expectedPaths {
		if got[i].SourcePath != want {
			t.Errorf("source[%d]: expected path %q, got %q", i, want, got[i].SourcePath)
		}
	}
}

func TestGetDependentNotes(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Two different notes depend on the same source file
	if err := db.RecordSource("notes/arch.md", "internal/store/db.go", "file", "hash1"); err != nil {
		t.Fatalf("RecordSource 1: %v", err)
	}
	if err := db.RecordSource("notes/design.md", "internal/store/db.go", "file", "hash1"); err != nil {
		t.Fatalf("RecordSource 2: %v", err)
	}
	if err := db.RecordSource("notes/design.md", "internal/store/store.go", "file", "hash2"); err != nil {
		t.Fatalf("RecordSource 3: %v", err)
	}

	dependents, err := db.GetDependentNotes("internal/store/db.go")
	if err != nil {
		t.Fatalf("GetDependentNotes: %v", err)
	}
	if len(dependents) != 2 {
		t.Fatalf("expected 2 dependent notes, got %d", len(dependents))
	}
	if dependents[0] != "notes/arch.md" || dependents[1] != "notes/design.md" {
		t.Errorf("unexpected dependents: %v", dependents)
	}

	// A source with no dependents
	dependents, err = db.GetDependentNotes("nonexistent.go")
	if err != nil {
		t.Fatalf("GetDependentNotes nonexistent: %v", err)
	}
	if len(dependents) != 0 {
		t.Errorf("expected 0 dependents for nonexistent source, got %d", len(dependents))
	}
}

func TestDeleteSourcesForNote(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Record sources for two different notes
	if err := db.RecordSource("notes/a.md", "src/file1.go", "file", "h1"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}
	if err := db.RecordSource("notes/a.md", "src/file2.go", "file", "h2"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}
	if err := db.RecordSource("notes/b.md", "src/file1.go", "file", "h1"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	// Delete sources for note a
	if err := db.DeleteSourcesForNote("notes/a.md"); err != nil {
		t.Fatalf("DeleteSourcesForNote: %v", err)
	}

	// Note a should have no sources
	sources, err := db.GetSourcesForNote("notes/a.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote a: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources for notes/a.md after delete, got %d", len(sources))
	}

	// Note b should still have its source
	sources, err = db.GetSourcesForNote("notes/b.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote b: %v", err)
	}
	if len(sources) != 1 {
		t.Errorf("expected 1 source for notes/b.md, got %d", len(sources))
	}
}

func TestCheckSourceDivergence(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Create a temp vault directory with source files
	vaultDir := t.TempDir()
	srcDir := filepath.Join(vaultDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	originalContent := []byte("package main\n\nfunc main() {}\n")
	srcFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(srcFile, originalContent, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	// Compute the original hash
	originalHash := fmt.Sprintf("%x", sha256.Sum256(originalContent))

	// Record source with the correct hash
	if err := db.RecordSource("notes/design.md", "src/main.go", "file", originalHash); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	// No divergence yet
	results, err := db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 divergences, got %d", len(results))
	}

	// Modify the source file
	modifiedContent := []byte("package main\n\nfunc main() { fmt.Println(\"changed\") }\n")
	if err := os.WriteFile(srcFile, modifiedContent, 0o644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	// Now divergence should be detected
	results, err = db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence after modify: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 divergence, got %d", len(results))
	}
	if results[0].NotePath != "notes/design.md" {
		t.Errorf("expected note_path 'notes/design.md', got %q", results[0].NotePath)
	}
	if results[0].SourcePath != "src/main.go" {
		t.Errorf("expected source_path 'src/main.go', got %q", results[0].SourcePath)
	}
	if results[0].StoredHash != originalHash {
		t.Errorf("expected stored_hash %q, got %q", originalHash, results[0].StoredHash)
	}
	expectedModifiedHash := fmt.Sprintf("%x", sha256.Sum256(modifiedContent))
	if results[0].CurrentHash != expectedModifiedHash {
		t.Errorf("expected current_hash %q, got %q", expectedModifiedHash, results[0].CurrentHash)
	}
}

func TestCheckSourceDivergence_DeletedFile(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vaultDir := t.TempDir()

	// Record a source for a file that doesn't exist
	if err := db.RecordSource("notes/orphan.md", "src/deleted.go", "file", "oldhash"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	results, err := db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 divergence for deleted file, got %d", len(results))
	}
	if results[0].CurrentHash != "" {
		t.Errorf("expected empty current_hash for deleted file, got %q", results[0].CurrentHash)
	}
	if results[0].StoredHash != "oldhash" {
		t.Errorf("expected stored_hash 'oldhash', got %q", results[0].StoredHash)
	}
}

func TestCheckSourceDivergence_SkipsNonFileTypes(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vaultDir := t.TempDir()

	// Record a URL source — should be skipped by divergence check
	if err := db.RecordSource("notes/ref.md", "https://example.com", "url", "urlhash"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	results, err := db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 divergences for URL source, got %d", len(results))
	}
}

func TestUpdateTrustState(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert some notes
	vec := make([]float32, 768)
	for _, path := range []string{"notes/a.md", "notes/b.md", "notes/c.md"} {
		rec := &NoteRecord{
			Path: path, Title: "Test", Tags: "[]", ChunkID: 0,
			ChunkHeading: "(full)", Text: "content", Modified: 1700000000,
			ContentHash: "hash-" + path, ContentType: "note", Confidence: 0.5,
		}
		if err := db.InsertNote(rec, vec); err != nil {
			t.Fatalf("InsertNote %s: %v", path, err)
		}
	}

	// Default trust_state should be 'unknown'
	var state string
	if err := db.Conn().QueryRow(
		"SELECT trust_state FROM vault_notes WHERE path = 'notes/a.md' AND chunk_id = 0",
	).Scan(&state); err != nil {
		t.Fatalf("query trust_state: %v", err)
	}
	if state != "unknown" {
		t.Errorf("expected default trust_state 'unknown', got %q", state)
	}

	// Update trust state for a and b
	if err := db.UpdateTrustState([]string{"notes/a.md", "notes/b.md"}, "validated"); err != nil {
		t.Fatalf("UpdateTrustState: %v", err)
	}

	// Verify a and b are validated
	for _, path := range []string{"notes/a.md", "notes/b.md"} {
		if err := db.Conn().QueryRow(
			"SELECT trust_state FROM vault_notes WHERE path = ? AND chunk_id = 0", path,
		).Scan(&state); err != nil {
			t.Fatalf("query trust_state %s: %v", path, err)
		}
		if state != "validated" {
			t.Errorf("expected 'validated' for %s, got %q", path, state)
		}
	}

	// Verify c is still unknown
	if err := db.Conn().QueryRow(
		"SELECT trust_state FROM vault_notes WHERE path = 'notes/c.md' AND chunk_id = 0",
	).Scan(&state); err != nil {
		t.Fatalf("query trust_state c: %v", err)
	}
	if state != "unknown" {
		t.Errorf("expected 'unknown' for notes/c.md, got %q", state)
	}

	// Invalid state should error
	if err := db.UpdateTrustState([]string{"notes/a.md"}, "invalid"); err == nil {
		t.Error("expected error for invalid trust state")
	}

	// Empty paths should be a no-op
	if err := db.UpdateTrustState(nil, "validated"); err != nil {
		t.Fatalf("UpdateTrustState empty: %v", err)
	}
}

func TestGetTrustStateSummary(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert notes with different trust states
	vec := make([]float32, 768)
	paths := []string{"notes/a.md", "notes/b.md", "notes/c.md", "notes/d.md", "notes/e.md"}
	for _, path := range paths {
		rec := &NoteRecord{
			Path: path, Title: "Test", Tags: "[]", ChunkID: 0,
			ChunkHeading: "(full)", Text: "content", Modified: 1700000000,
			ContentHash: "hash-" + path, ContentType: "note", Confidence: 0.5,
		}
		if err := db.InsertNote(rec, vec); err != nil {
			t.Fatalf("InsertNote %s: %v", path, err)
		}
	}

	// Also insert a chunk_id=1 for notes/a.md — should NOT be double-counted
	rec := &NoteRecord{
		Path: "notes/a.md", Title: "Test", Tags: "[]", ChunkID: 1,
		ChunkHeading: "Section 2", Text: "chunk 2 content", Modified: 1700000000,
		ContentHash: "hash-notes/a.md", ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote chunk 1: %v", err)
	}

	// Set trust states: 2 validated, 1 stale, 1 contradicted, 1 unknown (default)
	if err := db.UpdateTrustState([]string{"notes/a.md", "notes/b.md"}, "validated"); err != nil {
		t.Fatalf("UpdateTrustState validated: %v", err)
	}
	if err := db.UpdateTrustState([]string{"notes/c.md"}, "stale"); err != nil {
		t.Fatalf("UpdateTrustState stale: %v", err)
	}
	if err := db.UpdateTrustState([]string{"notes/d.md"}, "contradicted"); err != nil {
		t.Fatalf("UpdateTrustState contradicted: %v", err)
	}
	// notes/e.md stays 'unknown'

	summary, err := db.GetTrustStateSummary()
	if err != nil {
		t.Fatalf("GetTrustStateSummary: %v", err)
	}
	if summary.Validated != 2 {
		t.Errorf("expected Validated=2, got %d", summary.Validated)
	}
	if summary.Stale != 1 {
		t.Errorf("expected Stale=1, got %d", summary.Stale)
	}
	if summary.Contradicted != 1 {
		t.Errorf("expected Contradicted=1, got %d", summary.Contradicted)
	}
	if summary.Unknown != 1 {
		t.Errorf("expected Unknown=1, got %d", summary.Unknown)
	}
}

func TestMigrationV8ToV9(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Schema version should be 10 (through contradiction detail migration)
	v := db.SchemaVersion()
	if v != 10 {
		t.Errorf("expected schema version 10, got %d", v)
	}

	// note_sources table should exist
	var count int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM note_sources").Scan(&count); err != nil {
		t.Fatalf("note_sources table should exist: %v", err)
	}

	// trust_state column should exist on vault_notes
	if !db.hasColumn("vault_notes", "trust_state") {
		t.Error("expected trust_state column on vault_notes")
	}

	// Verify the indexes exist
	var idxCount int
	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_note_sources_note_path'",
	).Scan(&idxCount); err != nil {
		t.Fatalf("check idx_note_sources_note_path: %v", err)
	}
	if idxCount != 1 {
		t.Error("expected idx_note_sources_note_path index to exist")
	}

	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_note_sources_source_path'",
	).Scan(&idxCount); err != nil {
		t.Fatalf("check idx_note_sources_source_path: %v", err)
	}
	if idxCount != 1 {
		t.Error("expected idx_note_sources_source_path index to exist")
	}

	// Migration should be idempotent
	if err := db.migrate(); err != nil {
		t.Fatalf("re-migrate should succeed: %v", err)
	}
	v = db.SchemaVersion()
	if v != 10 {
		t.Errorf("expected schema version 10 after re-migrate, got %d", v)
	}
}

func TestSetContradicted(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert test notes
	for _, path := range []string{"notes/a.md", "notes/b.md", "notes/c.md"} {
		_, err := db.conn.Exec(
			`INSERT INTO vault_notes (path, title, chunk_id, chunk_heading, text, modified, content_hash)
			 VALUES (?, ?, 0, '', 'test content', 1000, 'hash1')`,
			path, path,
		)
		if err != nil {
			t.Fatalf("insert test note %s: %v", path, err)
		}
	}

	// Set contradictions of different types
	if err := db.SetContradicted("notes/a.md", "factual"); err != nil {
		t.Fatalf("SetContradicted factual: %v", err)
	}
	if err := db.SetContradicted("notes/b.md", "preference"); err != nil {
		t.Fatalf("SetContradicted preference: %v", err)
	}
	if err := db.SetContradicted("notes/c.md", "context"); err != nil {
		t.Fatalf("SetContradicted context: %v", err)
	}

	// Verify trust_state and contradiction_detail were set
	var trustState, detail string
	if err := db.conn.QueryRow(
		"SELECT trust_state, contradiction_detail FROM vault_notes WHERE path = 'notes/a.md'",
	).Scan(&trustState, &detail); err != nil {
		t.Fatalf("query notes/a.md: %v", err)
	}
	if trustState != "contradicted" {
		t.Errorf("expected trust_state 'contradicted', got %q", trustState)
	}
	if detail != "factual" {
		t.Errorf("expected contradiction_detail 'factual', got %q", detail)
	}

	// Invalid type should fail
	if err := db.SetContradicted("notes/a.md", "invalid"); err == nil {
		t.Error("expected error for invalid contradiction type")
	}
}

func TestGetContradictionSummary(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert notes with various contradiction types
	notes := []struct {
		path   string
		trust  string
		detail string
	}{
		{"notes/a.md", "contradicted", "factual"},
		{"notes/b.md", "contradicted", "factual"},
		{"notes/c.md", "contradicted", "preference"},
		{"notes/d.md", "contradicted", "context"},
		{"notes/e.md", "contradicted", ""},  // untyped
		{"notes/f.md", "validated", ""},      // not contradicted
		{"notes/g.md", "unknown", ""},        // not contradicted
	}
	for _, n := range notes {
		_, err := db.conn.Exec(
			`INSERT INTO vault_notes (path, title, chunk_id, chunk_heading, text, modified, content_hash, trust_state, contradiction_detail)
			 VALUES (?, ?, 0, '', 'test', 1000, 'hash1', ?, ?)`,
			n.path, n.path, n.trust, n.detail,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", n.path, err)
		}
	}

	breakdown, err := db.GetContradictionSummary()
	if err != nil {
		t.Fatalf("GetContradictionSummary: %v", err)
	}

	if breakdown.Factual != 2 {
		t.Errorf("expected 2 factual, got %d", breakdown.Factual)
	}
	if breakdown.Preference != 1 {
		t.Errorf("expected 1 preference, got %d", breakdown.Preference)
	}
	if breakdown.Context != 1 {
		t.Errorf("expected 1 context, got %d", breakdown.Context)
	}
	if breakdown.Untyped != 1 {
		t.Errorf("expected 1 untyped, got %d", breakdown.Untyped)
	}
}

func TestCheckSourceDivergence_AbsolutePath(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Create a temp file to serve as the absolute-path source
	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "memory.md")
	originalContent := []byte("# Original Memory\n\nOriginal content.\n")
	if err := os.WriteFile(sourceFile, originalContent, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	originalHash := fmt.Sprintf("%x", sha256.Sum256(originalContent))

	// Record source with an absolute path (as the import command would)
	if err := db.RecordSource("imported/memory.md", sourceFile, "file", originalHash); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	// No divergence yet — hash matches
	vaultDir := t.TempDir()
	results, err := db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 divergences, got %d", len(results))
	}

	// Modify the source file
	modifiedContent := []byte("# Updated Memory\n\nContent has changed.\n")
	if err := os.WriteFile(sourceFile, modifiedContent, 0o644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	// Now divergence should be detected
	results, err = db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence after modify: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 divergence, got %d", len(results))
	}
	if results[0].NotePath != "imported/memory.md" {
		t.Errorf("expected note_path 'imported/memory.md', got %q", results[0].NotePath)
	}
	if results[0].SourcePath != sourceFile {
		t.Errorf("expected source_path %q, got %q", sourceFile, results[0].SourcePath)
	}
	if results[0].StoredHash != originalHash {
		t.Errorf("expected stored_hash %q, got %q", originalHash, results[0].StoredHash)
	}
	expectedModifiedHash := fmt.Sprintf("%x", sha256.Sum256(modifiedContent))
	if results[0].CurrentHash != expectedModifiedHash {
		t.Errorf("expected current_hash %q, got %q", expectedModifiedHash, results[0].CurrentHash)
	}
}

func TestCheckSourceDivergence_AbsolutePath_Deleted(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Create and then delete a temp file
	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "memory.md")
	if err := os.WriteFile(sourceFile, []byte("temp"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	// Record source with absolute path
	if err := db.RecordSource("imported/memory.md", sourceFile, "file", "originalhash"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	// Delete the source file
	if err := os.Remove(sourceFile); err != nil {
		t.Fatalf("remove source file: %v", err)
	}

	// Should report as diverged with empty CurrentHash
	vaultDir := t.TempDir()
	results, err := db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 divergence for deleted file, got %d", len(results))
	}
	if results[0].CurrentHash != "" {
		t.Errorf("expected empty current_hash for deleted file, got %q", results[0].CurrentHash)
	}
	if results[0].StoredHash != "originalhash" {
		t.Errorf("expected stored_hash 'originalhash', got %q", results[0].StoredHash)
	}
}

func TestCheckSourceDivergence_RelativePath_StillSecure(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vaultDir := t.TempDir()

	// Record sources with traversal paths that should still be blocked
	traversalPaths := []string{
		"../../../etc/passwd",
		"../../etc/shadow",
		"..\\..\\windows\\system32\\config\\sam",
	}
	for _, p := range traversalPaths {
		if err := db.RecordSource("notes/malicious.md", p, "file", "fakehash"); err != nil {
			t.Fatalf("RecordSource %q: %v", p, err)
		}
	}

	results, err := db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 divergences (traversal paths should be skipped), got %d", len(results))
	}
}

func TestGetContradictionSummary_Empty(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	breakdown, err := db.GetContradictionSummary()
	if err != nil {
		t.Fatalf("GetContradictionSummary: %v", err)
	}

	if breakdown.Factual != 0 || breakdown.Preference != 0 || breakdown.Context != 0 || breakdown.Untyped != 0 {
		t.Errorf("expected all zeros for empty vault, got %+v", breakdown)
	}
}
