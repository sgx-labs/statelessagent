package store

import (
	"math"
	"math/rand"
	"testing"
)

func TestOpenMemory(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Verify sqlite-vec is loaded
	var vecVersion string
	if err := db.Conn().QueryRow("SELECT vec_version()").Scan(&vecVersion); err != nil {
		t.Fatalf("vec_version: %v", err)
	}
	t.Logf("sqlite-vec version: %s", vecVersion)
}

func TestInsertAndSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert 100 random vectors
	rng := rand.New(rand.NewSource(42))
	records := make([]NoteRecord, 100)
	embeddings := make([][]float32, 100)

	for i := 0; i < 100; i++ {
		records[i] = NoteRecord{
			Path:        "test/" + string(rune('a'+i%26)) + ".md",
			Title:       "Test Note",
			Tags:        "[]",
			Domain:      "test",
			Workstream:  "default",
			ChunkID:     i % 3,
			ChunkHeading: "(full)",
			Text:        "test content",
			Modified:    1700000000,
			ContentHash: "hash",
			ContentType: "note",
			Confidence:  0.5,
		}
		vec := make([]float32, 768)
		for j := range vec {
			vec[j] = rng.Float32()
		}
		embeddings[i] = vec
	}

	if err := db.BulkInsertNotes(records, embeddings); err != nil {
		t.Fatalf("BulkInsertNotes: %v", err)
	}

	// Verify counts
	noteCount, err := db.NoteCount()
	if err != nil {
		t.Fatalf("NoteCount: %v", err)
	}
	chunkCount, err := db.ChunkCount()
	if err != nil {
		t.Fatalf("ChunkCount: %v", err)
	}
	t.Logf("Notes: %d, Chunks: %d", noteCount, chunkCount)

	if chunkCount != 100 {
		t.Errorf("expected 100 chunks, got %d", chunkCount)
	}

	// Search with the first vector
	results, err := db.VectorSearch(embeddings[0], SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no search results")
	}
	t.Logf("Top result: %s (distance: %.1f, score: %.3f)", results[0].Path, results[0].Distance, results[0].Score)

	// The closest result should be the vector itself (distance ~0)
	if results[0].Distance > 1.0 {
		t.Errorf("expected first result to be very close, got distance %.1f", results[0].Distance)
	}
}

func TestKNNOrdering(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Create vectors with known distances
	// Vector 0: [1, 0, 0, ...] — the query
	// Vector 1: [0.9, 0.1, 0, ...] — close
	// Vector 2: [0, 1, 0, ...] — far
	dim := 768
	makeVec := func(x, y float32) []float32 {
		v := make([]float32, dim)
		v[0] = x
		v[1] = y
		return v
	}

	records := []NoteRecord{
		{Path: "close.md", Title: "Close", ChunkID: 0, ChunkHeading: "(full)", Text: "close", Modified: 1700000000, ContentHash: "a", ContentType: "note", Confidence: 0.5, Tags: "[]"},
		{Path: "far.md", Title: "Far", ChunkID: 0, ChunkHeading: "(full)", Text: "far", Modified: 1700000000, ContentHash: "b", ContentType: "note", Confidence: 0.5, Tags: "[]"},
	}
	vecs := [][]float32{
		makeVec(0.9, 0.1),
		makeVec(0, 1),
	}

	if err := db.BulkInsertNotes(records, vecs); err != nil {
		t.Fatalf("BulkInsertNotes: %v", err)
	}

	query := makeVec(1, 0)
	results, err := db.VectorSearch(query, SearchOptions{TopK: 2})
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Path != "close.md" {
		t.Errorf("expected close.md first, got %s", results[0].Path)
	}
	if results[1].Path != "far.md" {
		t.Errorf("expected far.md second, got %s", results[1].Path)
	}
}

func TestMetadataFiltering(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	dim := 768
	makeVec := func(val float32) []float32 {
		v := make([]float32, dim)
		v[0] = val
		return v
	}

	records := []NoteRecord{
		{Path: "work.md", Title: "Work", Domain: "Work", Tags: `["project"]`, ChunkID: 0, ChunkHeading: "(full)", Text: "work content", Modified: 1700000000, ContentHash: "a", ContentType: "note", Confidence: 0.5},
		{Path: "personal.md", Title: "Personal", Domain: "Home", Tags: `["hobby"]`, ChunkID: 0, ChunkHeading: "(full)", Text: "personal content", Modified: 1700000000, ContentHash: "b", ContentType: "note", Confidence: 0.5},
	}
	vecs := [][]float32{makeVec(0.5), makeVec(0.6)}

	if err := db.BulkInsertNotes(records, vecs); err != nil {
		t.Fatalf("BulkInsertNotes: %v", err)
	}

	query := makeVec(0.5)

	// Filter by domain
	results, err := db.VectorSearch(query, SearchOptions{TopK: 10, Domain: "Work"})
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(results) != 1 || results[0].Path != "work.md" {
		t.Errorf("domain filter: expected work.md only, got %v", results)
	}

	// Filter by tags
	results, err = db.VectorSearch(query, SearchOptions{TopK: 10, Tags: []string{"hobby"}})
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(results) != 1 || results[0].Path != "personal.md" {
		t.Errorf("tag filter: expected personal.md only, got %v", results)
	}
}

func TestContentHashes(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	rec := &NoteRecord{
		Path: "test.md", Title: "Test", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "content", Modified: 1700000000,
		ContentHash: "abc123", ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	hashes, err := db.GetContentHashes()
	if err != nil {
		t.Fatalf("GetContentHashes: %v", err)
	}
	if hashes["test.md"] != "abc123" {
		t.Errorf("expected hash abc123, got %s", hashes["test.md"])
	}
}

func TestSessionCRUD(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	rec := &SessionRecord{
		SessionID:    "test-session-1",
		StartedAt:    "2026-01-01T00:00:00Z",
		EndedAt:      "2026-01-01T01:00:00Z",
		HandoffPath:  "sessions/handoff.md",
		Machine:      "test-machine",
		FilesChanged: []string{"file1.md", "file2.md"},
		Summary:      "test session",
	}
	if err := db.InsertSession(rec); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	// Duplicate should not error
	if err := db.InsertSession(rec); err != nil {
		t.Fatalf("InsertSession duplicate: %v", err)
	}

	sessions, err := db.GetRecentSessions(10, "")
	if err != nil {
		t.Fatalf("GetRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].SessionID != "test-session-1" {
		t.Errorf("unexpected session ID: %s", sessions[0].SessionID)
	}
}

func TestUsageCRUD(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	rec := &UsageRecord{
		SessionID:       "s1",
		Timestamp:       "2026-01-01T00:00:00Z",
		HookName:        "context_surfacing",
		InjectedPaths:   []string{"note1.md", "note2.md"},
		EstimatedTokens: 250,
		WasReferenced:   false,
	}
	if err := db.InsertUsage(rec); err != nil {
		t.Fatalf("InsertUsage: %v", err)
	}

	records, err := db.GetUsageBySession("s1")
	if err != nil {
		t.Fatalf("GetUsageBySession: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].EstimatedTokens != 250 {
		t.Errorf("unexpected tokens: %d", records[0].EstimatedTokens)
	}
	if records[0].WasReferenced {
		t.Error("expected was_referenced=false")
	}

	// Mark as referenced
	if err := db.MarkReferenced(records[0].ID); err != nil {
		t.Fatalf("MarkReferenced: %v", err)
	}
	records, _ = db.GetUsageBySession("s1")
	if !records[0].WasReferenced {
		t.Error("expected was_referenced=true after MarkReferenced")
	}
}

func TestSessionStateCRUD(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Set a value
	if err := db.SessionStateSet("s1", "topic", "embeddings"); err != nil {
		t.Fatalf("SessionStateSet: %v", err)
	}

	// Get it back
	val, ok := db.SessionStateGet("s1", "topic")
	if !ok || val != "embeddings" {
		t.Errorf("expected 'embeddings', got %q (ok=%v)", val, ok)
	}

	// Get non-existent
	_, ok = db.SessionStateGet("s1", "missing")
	if ok {
		t.Error("expected ok=false for missing key")
	}

	// Upsert
	if err := db.SessionStateSet("s1", "topic", "ranking"); err != nil {
		t.Fatalf("SessionStateSet upsert: %v", err)
	}
	val, _ = db.SessionStateGet("s1", "topic")
	if val != "ranking" {
		t.Errorf("expected 'ranking' after upsert, got %q", val)
	}
}

func TestPinsCRUD(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Pin a note
	if err := db.PinNote("notes/important.md"); err != nil {
		t.Fatalf("PinNote: %v", err)
	}

	// Check isPinned
	pinned, err := db.IsPinned("notes/important.md")
	if err != nil || !pinned {
		t.Errorf("expected pinned=true, got %v (err=%v)", pinned, err)
	}

	// Get pinned paths
	paths, err := db.GetPinnedPaths()
	if err != nil || len(paths) != 1 {
		t.Errorf("expected 1 pinned path, got %d (err=%v)", len(paths), err)
	}

	// Unpin
	if err := db.UnpinNote("notes/important.md"); err != nil {
		t.Fatalf("UnpinNote: %v", err)
	}
	pinned, _ = db.IsPinned("notes/important.md")
	if pinned {
		t.Error("expected pinned=false after unpin")
	}
}

func TestDecisionInsert(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	rec := &DecisionRecord{
		SessionID:     "s1",
		PromptSnippet: "test prompt",
		Mode:          "exploring",
		JaccardScore:  0.42,
		Decision:      "inject",
		InjectedPaths: []string{"note1.md"},
	}
	if err := db.InsertDecision(rec); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
}

func TestSchemaMetaTable(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// schema_meta table should exist after migrate()
	var count int
	err = db.Conn().QueryRow("SELECT COUNT(*) FROM schema_meta").Scan(&count)
	if err != nil {
		t.Fatalf("schema_meta table should exist: %v", err)
	}
}

func TestGetSetMeta(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Get non-existent key
	_, ok := db.GetMeta("nonexistent")
	if ok {
		t.Error("expected ok=false for missing key")
	}

	// Set and get
	if err := db.SetMeta("test_key", "test_value"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	val, ok := db.GetMeta("test_key")
	if !ok || val != "test_value" {
		t.Errorf("expected 'test_value', got %q (ok=%v)", val, ok)
	}

	// Upsert
	if err := db.SetMeta("test_key", "updated_value"); err != nil {
		t.Fatalf("SetMeta upsert: %v", err)
	}
	val, ok = db.GetMeta("test_key")
	if !ok || val != "updated_value" {
		t.Errorf("expected 'updated_value', got %q", val)
	}
}

func TestSchemaVersion(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// After migrate(), version should be 3 (migrateV1 + migrateV2 + migrateV3 ran)
	v := db.SchemaVersion()
	if v != 3 {
		t.Errorf("expected schema version 3, got %d", v)
	}
}

func TestSchemaVersionIdempotent(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Running migrate() again should not change the version
	v1 := db.SchemaVersion()
	if err := db.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	v2 := db.SchemaVersion()
	if v1 != v2 {
		t.Errorf("version changed after re-migrate: %d -> %d", v1, v2)
	}
}

func TestEmbeddingMetaGuard(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// No stored metadata: should return nil (compatible)
	if err := db.CheckEmbeddingMeta("ollama", "nomic-embed-text", 768); err != nil {
		t.Errorf("expected nil for empty metadata, got: %v", err)
	}

	// Store metadata
	if err := db.SetEmbeddingMeta("ollama", "nomic-embed-text", 768); err != nil {
		t.Fatalf("SetEmbeddingMeta: %v", err)
	}

	// Same config: should return nil
	if err := db.CheckEmbeddingMeta("ollama", "nomic-embed-text", 768); err != nil {
		t.Errorf("expected nil for matching config, got: %v", err)
	}

	// Different dimensions: should error
	if err := db.CheckEmbeddingMeta("ollama", "nomic-embed-text", 1024); err == nil {
		t.Error("expected error for dimension mismatch")
	}

	// Different provider/model: should error
	if err := db.CheckEmbeddingMeta("openai", "text-embedding-3-small", 768); err == nil {
		t.Error("expected error for provider/model mismatch")
	}
}

func TestEmbeddingMetaGuardPartialMeta(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Store only dims (simulates partial metadata)
	if err := db.SetMeta("embed_dims", "768"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	// Matching dims, no provider/model stored: should pass
	if err := db.CheckEmbeddingMeta("ollama", "nomic-embed-text", 768); err != nil {
		t.Errorf("expected nil for partial meta with matching dims, got: %v", err)
	}

	// Mismatched dims: should error
	if err := db.CheckEmbeddingMeta("openai", "text-embedding-3-large", 1024); err == nil {
		t.Error("expected error for dimension mismatch with partial meta")
	}
}

func TestIntegrityCheck(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// A fresh database should pass integrity check
	if err := db.IntegrityCheck(); err != nil {
		t.Errorf("expected integrity check to pass, got: %v", err)
	}
}

func TestLastReindexTime(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// No reindex time initially
	if v := db.LastReindexTime(); v != "" {
		t.Errorf("expected empty, got %q", v)
	}

	// Set it
	if err := db.SetMeta("last_reindex_time", "2026-01-15T10:00:00Z"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if v := db.LastReindexTime(); v != "2026-01-15T10:00:00Z" {
		t.Errorf("expected timestamp, got %q", v)
	}
}

func TestAdjustConfidence(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	rec := &NoteRecord{
		Path: "notes/test.md", Title: "Test", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "content", Modified: 1700000000,
		ContentHash: "abc", ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// Boost confidence
	if err := db.AdjustConfidence("notes/test.md", 0.7); err != nil {
		t.Fatalf("AdjustConfidence: %v", err)
	}

	notes, err := db.GetNoteByPath("notes/test.md")
	if err != nil || len(notes) == 0 {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if notes[0].Confidence != 0.7 {
		t.Errorf("expected confidence 0.7, got %.2f", notes[0].Confidence)
	}
}

func TestSetAccessBoost(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	rec := &NoteRecord{
		Path: "notes/test.md", Title: "Test", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "content", Modified: 1700000000,
		ContentHash: "abc", ContentType: "note", Confidence: 0.5, AccessCount: 2,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// Boost access count by 5
	if err := db.SetAccessBoost("notes/test.md", 5); err != nil {
		t.Fatalf("SetAccessBoost: %v", err)
	}

	notes, err := db.GetNoteByPath("notes/test.md")
	if err != nil || len(notes) == 0 {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if notes[0].AccessCount != 7 {
		t.Errorf("expected access count 7, got %d", notes[0].AccessCount)
	}
}

func TestPruneUsageData(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert an old usage record
	if err := db.InsertUsage(&UsageRecord{
		SessionID:       "s-old",
		Timestamp:       "2020-01-01T00:00:00Z",
		HookName:        "context_surfacing",
		InjectedPaths:   []string{"old.md"},
		EstimatedTokens: 100,
	}); err != nil {
		t.Fatalf("InsertUsage: %v", err)
	}

	// Insert a recent usage record
	if err := db.InsertUsage(&UsageRecord{
		SessionID:       "s-new",
		Timestamp:       "2026-01-01T00:00:00Z",
		HookName:        "context_surfacing",
		InjectedPaths:   []string{"new.md"},
		EstimatedTokens: 200,
	}); err != nil {
		t.Fatalf("InsertUsage: %v", err)
	}

	// Prune old data (90 days)
	pruned, err := db.PruneUsageData(90)
	if err != nil {
		t.Fatalf("PruneUsageData: %v", err)
	}
	if pruned < 1 {
		t.Errorf("expected at least 1 pruned, got %d", pruned)
	}

	// Verify only recent record remains
	records, err := db.GetRecentUsage(10)
	if err != nil {
		t.Fatalf("GetRecentUsage: %v", err)
	}
	for _, r := range records {
		if r.SessionID == "s-old" {
			t.Error("old record should have been pruned")
		}
	}
}

func TestRebuildFTS(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	rec := &NoteRecord{
		Path: "notes/search-test.md", Title: "Architecture Decisions", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "We decided to use SQLite for the database layer.", Modified: 1700000000,
		ContentHash: "abc", ContentType: "decision", Confidence: 0.8,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// RebuildFTS is a no-op if FTS5 is unavailable (shouldn't error)
	if err := db.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	if !db.FTSAvailable() {
		t.Log("FTS5 not available in test environment, skipping FTS search test")
		return
	}

	// FTS5 search should find the note
	results, err := db.FTS5Search("architecture SQLite database", SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("FTS5Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one FTS5 result")
	}
	if len(results) > 0 && results[0].Path != "notes/search-test.md" {
		t.Errorf("expected search-test.md, got %s", results[0].Path)
	}
}

func TestFTS5SearchUnavailable(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if !db.FTSAvailable() {
		// FTS5 not available — FTS5Search should return error
		_, err := db.FTS5Search("test query", SearchOptions{TopK: 5})
		if err == nil {
			t.Error("expected error when FTS5 not available")
		}
		return
	}

	// FTS5 available — search on empty index should return nil
	results, err := db.FTS5Search("nonexistent query", SearchOptions{TopK: 5})
	if err != nil {
		t.Logf("FTS5Search on empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestFTSAvailableFlag(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// FTSAvailable() should return a boolean without error
	// On macOS in-memory, FTS5 may not be available
	available := db.FTSAvailable()
	t.Logf("FTS5 available: %v", available)
}

func TestMilestoneShown(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Unknown key should return false
	if db.MilestoneShown("unknown_key") {
		t.Errorf("expected MilestoneShown=false for unknown key")
	}

	// Record the milestone
	if err := db.RecordMilestone("unknown_key"); err != nil {
		t.Fatalf("RecordMilestone: %v", err)
	}

	// Now it should return true
	if !db.MilestoneShown("unknown_key") {
		t.Errorf("expected MilestoneShown=true after RecordMilestone")
	}
}

func TestRecordMilestone(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Record a milestone
	if err := db.RecordMilestone("test_milestone"); err != nil {
		t.Fatalf("RecordMilestone: %v", err)
	}

	// Record again — should be idempotent (no error)
	if err := db.RecordMilestone("test_milestone"); err != nil {
		t.Fatalf("RecordMilestone idempotent: %v", err)
	}

	// Verify it's still shown
	if !db.MilestoneShown("test_milestone") {
		t.Errorf("expected MilestoneShown=true after double RecordMilestone")
	}
}

func TestMilestoneAge(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Unknown key should return 0
	if age := db.MilestoneAge("unknown_key"); age != 0 {
		t.Errorf("expected MilestoneAge=0 for unknown key, got %v", age)
	}

	// Record the milestone
	if err := db.RecordMilestone("age_test"); err != nil {
		t.Fatalf("RecordMilestone: %v", err)
	}

	// Age should now be >= 0
	age := db.MilestoneAge("age_test")
	if age < 0 {
		t.Errorf("expected MilestoneAge >= 0, got %v", age)
	}
}

func TestClearMilestone(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Record a milestone
	if err := db.RecordMilestone("clear_test"); err != nil {
		t.Fatalf("RecordMilestone: %v", err)
	}

	// Verify it's shown
	if !db.MilestoneShown("clear_test") {
		t.Errorf("expected MilestoneShown=true after RecordMilestone")
	}

	// Clear it
	if err := db.ClearMilestone("clear_test"); err != nil {
		t.Fatalf("ClearMilestone: %v", err)
	}

	// Verify it's no longer shown
	if db.MilestoneShown("clear_test") {
		t.Errorf("expected MilestoneShown=false after ClearMilestone")
	}
}

func TestGetPinnedNotes(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	rec := &NoteRecord{
		Path: "notes/pinned.md", Title: "Pinned Note", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "pinned content", Modified: 1700000000,
		ContentHash: "hash1", ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// Pin the note
	if err := db.PinNote("notes/pinned.md"); err != nil {
		t.Fatalf("PinNote: %v", err)
	}

	// GetPinnedNotes should return the note record
	pinned, err := db.GetPinnedNotes()
	if err != nil {
		t.Fatalf("GetPinnedNotes: %v", err)
	}
	if len(pinned) != 1 {
		t.Fatalf("expected 1 pinned note, got %d", len(pinned))
	}
	if pinned[0].Path != "notes/pinned.md" {
		t.Errorf("expected path notes/pinned.md, got %s", pinned[0].Path)
	}
	if pinned[0].Title != "Pinned Note" {
		t.Errorf("expected title 'Pinned Note', got %s", pinned[0].Title)
	}
}

func TestGetLatestHandoff(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	// Insert an older handoff
	rec1 := &NoteRecord{
		Path: "sessions/handoff-old.md", Title: "Old Handoff", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "old handoff content", Modified: 1700000000,
		ContentHash: "h1", ContentType: "handoff", Confidence: 0.5,
	}
	if err := db.InsertNote(rec1, vec); err != nil {
		t.Fatalf("InsertNote old: %v", err)
	}

	// Insert a newer handoff
	rec2 := &NoteRecord{
		Path: "sessions/handoff-new.md", Title: "New Handoff", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "new handoff content", Modified: 1700001000,
		ContentHash: "h2", ContentType: "handoff", Confidence: 0.5,
	}
	if err := db.InsertNote(rec2, vec); err != nil {
		t.Fatalf("InsertNote new: %v", err)
	}

	// GetLatestHandoff should return the most recently modified one
	latest, err := db.GetLatestHandoff()
	if err != nil {
		t.Fatalf("GetLatestHandoff: %v", err)
	}
	if latest.Path != "sessions/handoff-new.md" {
		t.Errorf("expected sessions/handoff-new.md, got %s", latest.Path)
	}
}

func TestDeleteByPath(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	rec := &NoteRecord{
		Path: "notes/deleteme.md", Title: "Delete Me", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "delete content", Modified: 1700000000,
		ContentHash: "d1", ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// Verify it exists
	notes, err := db.GetNoteByPath("notes/deleteme.md")
	if err != nil {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if len(notes) == 0 {
		t.Fatalf("expected note to exist before delete")
	}

	// Delete it
	if err := db.DeleteByPath("notes/deleteme.md"); err != nil {
		t.Fatalf("DeleteByPath: %v", err)
	}

	// Verify it's gone
	notes, err = db.GetNoteByPath("notes/deleteme.md")
	if err != nil {
		t.Fatalf("GetNoteByPath after delete: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("expected 0 notes after delete, got %d", len(notes))
	}
}

func TestRecentNotes(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	// Insert several notes with different modified times
	notes := []NoteRecord{
		{Path: "notes/oldest.md", Title: "Oldest", Tags: "[]", ChunkID: 0, ChunkHeading: "(full)", Text: "oldest", Modified: 1700000000, ContentHash: "a", ContentType: "note", Confidence: 0.5},
		{Path: "notes/middle.md", Title: "Middle", Tags: "[]", ChunkID: 0, ChunkHeading: "(full)", Text: "middle", Modified: 1700001000, ContentHash: "b", ContentType: "note", Confidence: 0.5},
		{Path: "notes/newest.md", Title: "Newest", Tags: "[]", ChunkID: 0, ChunkHeading: "(full)", Text: "newest", Modified: 1700002000, ContentHash: "c", ContentType: "note", Confidence: 0.5},
	}
	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	// RecentNotes(2) should return 2, ordered most recent first
	recent, err := db.RecentNotes(2)
	if err != nil {
		t.Fatalf("RecentNotes: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent notes, got %d", len(recent))
	}
	if recent[0].Path != "notes/newest.md" {
		t.Errorf("expected newest first, got %s", recent[0].Path)
	}
	if recent[1].Path != "notes/middle.md" {
		t.Errorf("expected middle second, got %s", recent[1].Path)
	}
}

func TestIncrementAccessCount(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	rec := &NoteRecord{
		Path: "notes/access.md", Title: "Access Test", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "access content", Modified: 1700000000,
		ContentHash: "ac1", ContentType: "note", Confidence: 0.5, AccessCount: 0,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// Increment access count
	if err := db.IncrementAccessCount([]string{"notes/access.md"}); err != nil {
		t.Fatalf("IncrementAccessCount: %v", err)
	}

	// Verify it's now 1
	notes, err := db.GetNoteByPath("notes/access.md")
	if err != nil || len(notes) == 0 {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if notes[0].AccessCount != 1 {
		t.Errorf("expected access count 1, got %d", notes[0].AccessCount)
	}
}

func TestParseTags(t *testing.T) {
	// Valid JSON array
	tags := ParseTags(`["a","b"]`)
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("expected [a b], got %v", tags)
	}

	// Empty string returns empty/nil slice
	tags = ParseTags("")
	if len(tags) != 0 {
		t.Errorf("expected empty slice for empty string, got %v", tags)
	}

	// Empty JSON array returns empty slice
	tags = ParseTags("[]")
	if len(tags) != 0 {
		t.Errorf("expected empty slice for '[]', got %v", tags)
	}
}

func TestHasVectors_Empty(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Empty database should report no vectors
	if db.HasVectors() {
		t.Error("expected HasVectors=false for empty database")
	}
}

func TestHasVectors_WithVectors(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert a note with embedding
	rec := &NoteRecord{
		Path: "test.md", Title: "Test", Tags: "[]",
		ChunkID: 0, Text: "test content", Modified: 1700000000,
		ContentHash: "hash", ContentType: "note", Confidence: 0.5,
	}
	vec := make([]float32, 768)
	vec[0] = 1.0

	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	if !db.HasVectors() {
		t.Error("expected HasVectors=true after inserting a note with embedding")
	}
}

func TestHasVectors_LiteMode(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert notes via BulkInsertNotesLite (no embeddings)
	records := []NoteRecord{
		{
			Path: "lite-test.md", Title: "Lite Test", Tags: "[]",
			ChunkID: 0, Text: "lite content", Modified: 1700000000,
			ContentHash: "hash", ContentType: "note", Confidence: 0.5,
		},
	}
	if err := db.BulkInsertNotesLite(records); err != nil {
		t.Fatalf("BulkInsertNotesLite: %v", err)
	}

	// Notes exist but no vectors
	count, err := db.NoteCount()
	if err != nil {
		t.Fatalf("NoteCount: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 note, got %d", count)
	}

	if db.HasVectors() {
		t.Error("expected HasVectors=false after BulkInsertNotesLite")
	}
}

func TestBulkInsertNotesLite(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	records := []NoteRecord{
		{
			Path: "a.md", Title: "Note A", Tags: `["tag1"]`,
			Domain: "eng", Workstream: "api",
			ChunkID: 0, ChunkHeading: "(full)", Text: "content of note A",
			Modified: 1700000000, ContentHash: "hash-a",
			ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "b.md", Title: "Note B", Tags: `["tag2"]`,
			Domain: "eng", Workstream: "api",
			ChunkID: 0, ChunkHeading: "(full)", Text: "content of note B",
			Modified: 1700000001, ContentHash: "hash-b",
			ContentType: "decision", Confidence: 0.7,
		},
		{
			Path: "a.md", Title: "Note A", Tags: `["tag1"]`,
			Domain: "eng", Workstream: "api",
			ChunkID: 1, ChunkHeading: "Section 2", Text: "second chunk of note A",
			Modified: 1700000000, ContentHash: "hash-a",
			ContentType: "note", Confidence: 0.5,
		},
	}

	if err := db.BulkInsertNotesLite(records); err != nil {
		t.Fatalf("BulkInsertNotesLite: %v", err)
	}

	// Check counts
	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	if noteCount != 2 {
		t.Errorf("expected 2 unique notes, got %d", noteCount)
	}
	if chunkCount != 3 {
		t.Errorf("expected 3 chunks, got %d", chunkCount)
	}

	// Verify notes can be retrieved
	notes, err := db.GetNoteByPath("a.md")
	if err != nil {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 chunks for a.md, got %d", len(notes))
	}
	if notes[0].Title != "Note A" {
		t.Errorf("expected title 'Note A', got %q", notes[0].Title)
	}

	// Verify content hashes work for incremental mode
	hashes, err := db.GetContentHashes()
	if err != nil {
		t.Fatalf("GetContentHashes: %v", err)
	}
	if hashes["a.md"] != "hash-a" {
		t.Errorf("expected hash-a for a.md, got %q", hashes["a.md"])
	}
	if hashes["b.md"] != "hash-b" {
		t.Errorf("expected hash-b for b.md, got %q", hashes["b.md"])
	}
}

func TestBulkInsertNotesLite_Empty(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Empty insert should not error
	if err := db.BulkInsertNotesLite(nil); err != nil {
		t.Fatalf("BulkInsertNotesLite(nil): %v", err)
	}
	if err := db.BulkInsertNotesLite([]NoteRecord{}); err != nil {
		t.Fatalf("BulkInsertNotesLite(empty): %v", err)
	}
}

// Suppress unused import warnings
var _ = math.Pi
