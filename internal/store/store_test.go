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

// Suppress unused import warnings
var _ = math.Pi
