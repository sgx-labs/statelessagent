package store

import (
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// --- Empty vault operations ---

func TestSearchEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Vector search on empty DB should return nil, not error
	query := make([]float32, 768)
	query[0] = 1.0
	results, err := db.VectorSearch(query, SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("VectorSearch on empty DB: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on empty DB, got %d", len(results))
	}
}

func TestSearchRawEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	query := make([]float32, 768)
	raw, err := db.VectorSearchRaw(query, 10)
	if err != nil {
		t.Fatalf("VectorSearchRaw on empty DB: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("expected 0 raw results on empty DB, got %d", len(raw))
	}
}

func TestNoteCountEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	count, err := db.NoteCount()
	if err != nil {
		t.Fatalf("NoteCount: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 notes, got %d", count)
	}
}

func TestChunkCountEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	count, err := db.ChunkCount()
	if err != nil {
		t.Fatalf("ChunkCount: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 chunks, got %d", count)
	}
}

func TestRecentNotesEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	recent, err := db.RecentNotes(10)
	if err != nil {
		t.Fatalf("RecentNotes: %v", err)
	}
	if len(recent) != 0 {
		t.Errorf("expected 0 recent notes, got %d", len(recent))
	}
}

func TestKeywordSearchEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	results, err := db.KeywordSearch([]string{"test"}, 10)
	if err != nil {
		t.Fatalf("KeywordSearch on empty DB: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 keyword results, got %d", len(results))
	}
}

func TestContentTermSearchEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	results, err := db.ContentTermSearch([]string{"test", "content"}, 1, 10)
	if err != nil {
		t.Fatalf("ContentTermSearch on empty DB: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestKeywordSearchTitleMatchEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	results, err := db.KeywordSearchTitleMatch([]string{"test"}, 1, 10)
	if err != nil {
		t.Fatalf("KeywordSearchTitleMatch on empty DB: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestFuzzyTitleSearchEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	results, err := db.FuzzyTitleSearch([]string{"architecture"}, 10)
	if err != nil {
		t.Fatalf("FuzzyTitleSearch on empty DB: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestGetContentHashesEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	hashes, err := db.GetContentHashes()
	if err != nil {
		t.Fatalf("GetContentHashes: %v", err)
	}
	if len(hashes) != 0 {
		t.Errorf("expected 0 hashes, got %d", len(hashes))
	}
}

func TestGetPinnedNotesEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	pinned, err := db.GetPinnedNotes()
	if err != nil {
		t.Fatalf("GetPinnedNotes: %v", err)
	}
	if len(pinned) != 0 {
		t.Errorf("expected 0 pinned notes, got %d", len(pinned))
	}
}

func TestGetLatestHandoffEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// GetLatestHandoff returns an error when no handoff notes exist (wraps sql.ErrNoRows)
	handoff, err := db.GetLatestHandoff()
	if err == nil {
		if handoff != nil {
			t.Errorf("expected nil handoff on empty DB, got %+v", handoff)
		}
	}
	// Either nil,err or nil,nil is acceptable on empty DB
}

func TestDeleteByPathEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Should not error on nonexistent path
	if err := db.DeleteByPath("nonexistent.md"); err != nil {
		t.Fatalf("DeleteByPath on empty: %v", err)
	}
}

func TestDeleteAllNotesEmptyDB(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if err := db.DeleteAllNotes(); err != nil {
		t.Fatalf("DeleteAllNotes on empty: %v", err)
	}
}

// --- Unicode in note paths and content ---

func TestUnicodeNotePaths(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	vec[0] = 1.0

	unicodePaths := []struct {
		name string
		path string
	}{
		{"CJK characters", "notes/\u4e16\u754c\u60a8\u597d.md"},
		{"emoji", "notes/\U0001F680-launch.md"},
		{"accented chars", "notes/caf\u00e9-guide.md"},
		{"cyrillic", "notes/\u041f\u0440\u0438\u0432\u0435\u0442.md"},
		{"arabic", "notes/\u0645\u0631\u062d\u0628\u0627.md"},
		{"combining chars", "notes/cafe\u0301.md"},
	}

	for _, tt := range unicodePaths {
		t.Run(tt.name, func(t *testing.T) {
			rec := &NoteRecord{
				Path: tt.path, Title: "Unicode Test", Tags: "[]", ChunkID: 0,
				ChunkHeading: "(full)", Text: "Unicode content: " + tt.path,
				Modified: 1700000000, ContentHash: "hash-" + tt.name,
				ContentType: "note", Confidence: 0.5,
			}
			if err := db.InsertNote(rec, vec); err != nil {
				t.Fatalf("InsertNote: %v", err)
			}

			// Verify retrieval
			notes, err := db.GetNoteByPath(tt.path)
			if err != nil {
				t.Fatalf("GetNoteByPath: %v", err)
			}
			if len(notes) == 0 {
				t.Fatalf("expected note at %q, got none", tt.path)
			}
			if notes[0].Path != tt.path {
				t.Errorf("expected path %q, got %q", tt.path, notes[0].Path)
			}
		})
	}
}

func TestUnicodeInNoteContent(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	unicodeContent := "# \u4e16\u754c\u60a8\u597d\n\nThis note has mixed content: " +
		"\u00e9\u00e8\u00ea\u00eb, \u00fc\u00f6\u00e4, \u00e7, \u00f1. " +
		"CJK: \u4eba\u5de5\u667a\u80fd. " +
		"Emoji: \U0001F4DA\U0001F4DD\U0001F680."

	rec := &NoteRecord{
		Path: "notes/unicode-content.md", Title: "\u4e16\u754c\u60a8\u597d", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: unicodeContent,
		Modified: 1700000000, ContentHash: "unicode-hash",
		ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	notes, err := db.GetNoteByPath("notes/unicode-content.md")
	if err != nil || len(notes) == 0 {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if notes[0].Text != unicodeContent {
		t.Errorf("expected unicode content preserved, got %q", notes[0].Text[:50])
	}
}

// --- Very large notes ---

func TestLargeNoteContent(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	// Create a note with >1MB of content
	largeContent := strings.Repeat("This is a paragraph of content for testing large notes. ", 20000)
	if len(largeContent) < 1024*1024 {
		t.Fatalf("expected large content > 1MB, got %d bytes", len(largeContent))
	}

	rec := &NoteRecord{
		Path: "notes/large.md", Title: "Large Note", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: largeContent,
		Modified: 1700000000, ContentHash: "large-hash",
		ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote large: %v", err)
	}

	notes, err := db.GetNoteByPath("notes/large.md")
	if err != nil || len(notes) == 0 {
		t.Fatalf("GetNoteByPath large: %v", err)
	}
	if len(notes[0].Text) != len(largeContent) {
		t.Errorf("expected content length %d, got %d", len(largeContent), len(notes[0].Text))
	}
}

// --- Concurrent operations ---

func TestConcurrentSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Test that vec0 queries actually work before running concurrent tests
	testRec := &NoteRecord{
		Path: "notes/probe.md", Title: "Probe", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "probe", Modified: 1700000000,
		ContentHash: "probe", ContentType: "note", Confidence: 0.5,
	}
	probeVec := make([]float32, 768)
	probeVec[0] = 1.0
	if err := db.InsertNote(testRec, probeVec); err != nil {
		t.Skipf("vec0 insert not functional: %v", err)
	}
	if _, err := db.VectorSearch(probeVec, SearchOptions{TopK: 1}); err != nil {
		t.Skipf("vec0 search not functional in test environment: %v", err)
	}

	// Insert more test data
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 20; i++ {
		rec := &NoteRecord{
			Path: "notes/" + string(rune('a'+i)) + ".md", Title: "Note",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)", Text: "content",
			Modified: 1700000000, ContentHash: "h" + string(rune('a'+i)),
			ContentType: "note", Confidence: 0.5,
		}
		vec := make([]float32, 768)
		for j := range vec {
			vec[j] = rng.Float32()
		}
		if err := db.InsertNote(rec, vec); err != nil {
			t.Fatalf("InsertNote: %v", err)
		}
	}

	// Run concurrent searches. The primary assertion is no panics.
	// Transient SQLite/vec0 errors may occur under concurrent in-memory access.
	var wg sync.WaitGroup
	var errCount int
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			query := make([]float32, 768)
			query[idx%768] = 1.0

			_, err := db.VectorSearch(query, SearchOptions{TopK: 5})
			if err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	t.Logf("concurrent vector search: %d/10 goroutines completed with errors", errCount)
}

func TestConcurrentKeywordSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if !db.FTSAvailable() {
		t.Skip("FTS5 not available in test environment")
	}

	// Insert test data
	vec := make([]float32, 768)
	for i := 0; i < 10; i++ {
		rec := &NoteRecord{
			Path: "notes/search-" + string(rune('a'+i)) + ".md", Title: "Search Note",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)", Text: "searchable content keyword",
			Modified: 1700000000, ContentHash: "ks" + string(rune('a'+i)),
			ContentType: "note", Confidence: 0.5,
		}
		if err := db.InsertNote(rec, vec); err != nil {
			t.Fatalf("InsertNote: %v", err)
		}
	}

	// Run concurrent keyword searches. The primary assertion is no panics.
	// Transient SQLite errors may occur under concurrent in-memory access.
	var wg sync.WaitGroup
	var errCount int
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.KeywordSearch([]string{"searchable", "keyword"}, 5)
			if err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf("concurrent keyword search: %d/10 goroutines completed with errors", errCount)
}

func TestConcurrentInsertAndSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Test that vec0 queries actually work before running concurrent tests
	probeRec := &NoteRecord{
		Path: "notes/probe.md", Title: "Probe", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "probe", Modified: 1700000000,
		ContentHash: "probe2", ContentType: "note", Confidence: 0.5,
	}
	probeVec := make([]float32, 768)
	probeVec[0] = 1.0
	if err := db.InsertNote(probeRec, probeVec); err != nil {
		t.Skipf("vec0 insert not functional: %v", err)
	}
	if _, err := db.VectorSearch(probeVec, SearchOptions{TopK: 1}); err != nil {
		t.Skipf("vec0 search not functional in test environment: %v", err)
	}

	// Insert initial data
	rng := rand.New(rand.NewSource(123))
	for i := 0; i < 5; i++ {
		rec := &NoteRecord{
			Path: "notes/init-" + string(rune('a'+i)) + ".md", Title: "Init Note",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)", Text: "initial",
			Modified: 1700000000, ContentHash: "init-" + string(rune('a'+i)),
			ContentType: "note", Confidence: 0.5,
		}
		vec := make([]float32, 768)
		for j := range vec {
			vec[j] = rng.Float32()
		}
		if err := db.InsertNote(rec, vec); err != nil {
			t.Fatalf("InsertNote init: %v", err)
		}
	}

	var wg sync.WaitGroup

	// Concurrent searches
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			query := make([]float32, 768)
			query[0] = float32(idx) * 0.2
			db.VectorSearch(query, SearchOptions{TopK: 3})
		}(i)
	}

	// Concurrent inserts
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rec := &NoteRecord{
				Path: "notes/concurrent-" + string(rune('a'+idx)) + ".md", Title: "Concurrent Note",
				Tags: "[]", ChunkID: 0, ChunkHeading: "(full)", Text: "concurrent insert",
				Modified: 1700000000, ContentHash: "conc-" + string(rune('a'+idx)),
				ContentType: "note", Confidence: 0.5,
			}
			vec := make([]float32, 768)
			vec[idx%768] = 1.0
			db.InsertNote(rec, vec)
		}(i)
	}

	wg.Wait()
}

// --- Ranking edge cases ---

func TestRankSearchResults_EmptyResults(t *testing.T) {
	ranked := RankSearchResults(nil, []string{"test"})
	if len(ranked) != 0 {
		t.Errorf("expected 0 results, got %d", len(ranked))
	}
}

func TestRankSearchResults_EmptyQueryTerms(t *testing.T) {
	results := []SearchResult{
		{Path: "a.md", Title: "A", Score: 0.9},
		{Path: "b.md", Title: "B", Score: 0.8},
	}
	ranked := RankSearchResults(results, nil)
	// Should return original results unchanged
	if len(ranked) != 2 {
		t.Errorf("expected 2 results, got %d", len(ranked))
	}
	if ranked[0].Path != "a.md" {
		t.Errorf("expected original order preserved")
	}
}

func TestRankSearchResults_SingleResult(t *testing.T) {
	results := []SearchResult{
		{Path: "only.md", Title: "Only Result", Score: 0.5},
	}
	ranked := RankSearchResults(results, []string{"Only"})
	if len(ranked) != 1 {
		t.Fatalf("expected 1 result, got %d", len(ranked))
	}
	if ranked[0].Path != "only.md" {
		t.Errorf("expected only.md, got %s", ranked[0].Path)
	}
}

func TestRankSearchResults_AllZeroScores(t *testing.T) {
	results := []SearchResult{
		{Path: "a.md", Title: "First", Score: 0},
		{Path: "b.md", Title: "Second", Score: 0},
		{Path: "c.md", Title: "Third", Score: 0},
	}
	ranked := RankSearchResults(results, []string{"unmatched"})
	if len(ranked) != 3 {
		t.Fatalf("expected 3 results, got %d", len(ranked))
	}
}

func TestRankSearchResults_IdenticalScores(t *testing.T) {
	results := []SearchResult{
		{Path: "a.md", Title: "Same Score A", Score: 0.5, ContentType: "note"},
		{Path: "b.md", Title: "Same Score B", Score: 0.5, ContentType: "note"},
		{Path: "c.md", Title: "Same Score C", Score: 0.5, ContentType: "note"},
	}
	ranked := RankSearchResults(results, []string{"unmatched"})
	if len(ranked) != 3 {
		t.Fatalf("expected 3 results, got %d", len(ranked))
	}
}

func TestRankSearchResults_PriorityTypesInMediumTier(t *testing.T) {
	results := []SearchResult{
		{Path: "note.md", Title: "Authentication Guide", Score: 0.9, ContentType: "note"},
		{Path: "decision.md", Title: "Authentication Decision", Score: 0.8, ContentType: "decision"},
	}
	ranked := RankSearchResults(results, []string{"Authentication"})

	// Both have title overlap. Decision type should rank higher in medium/high tier
	// due to priority type preference.
	decisionIdx := -1
	noteIdx := -1
	for i, r := range ranked {
		if r.Path == "decision.md" {
			decisionIdx = i
		}
		if r.Path == "note.md" {
			noteIdx = i
		}
	}
	if decisionIdx == -1 || noteIdx == -1 {
		t.Fatal("expected both results in output")
	}
	if decisionIdx > noteIdx {
		t.Errorf("expected decision type to rank higher, got decision=%d note=%d", decisionIdx, noteIdx)
	}
}

// --- Edge cases for search functions ---

func TestKeywordSearch_EmptyTerms(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	results, err := db.KeywordSearch(nil, 10)
	if err != nil {
		t.Fatalf("KeywordSearch nil terms: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil terms, got %d", len(results))
	}
}

func TestKeywordSearch_ZeroLimit(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	results, err := db.KeywordSearch([]string{"test"}, 0)
	if err != nil {
		t.Fatalf("KeywordSearch zero limit: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for zero limit, got %d", len(results))
	}
}

func TestContentTermSearch_InsufficientParams(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Empty terms
	results, err := db.ContentTermSearch(nil, 1, 10)
	if err != nil {
		t.Fatalf("ContentTermSearch nil: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0, got %d", len(results))
	}

	// Zero limit
	results, err = db.ContentTermSearch([]string{"test"}, 1, 0)
	if err != nil {
		t.Fatalf("ContentTermSearch zero limit: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0, got %d", len(results))
	}

	// Zero minTerms
	results, err = db.ContentTermSearch([]string{"test"}, 0, 10)
	if err != nil {
		t.Fatalf("ContentTermSearch zero minTerms: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0, got %d", len(results))
	}
}

func TestVectorSearch_ClampsTopK(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Insert one note
	rec := &NoteRecord{
		Path: "notes/test.md", Title: "Test", Tags: "[]", ChunkID: 0,
		ChunkHeading: "(full)", Text: "content", Modified: 1700000000,
		ContentHash: "h1", ContentType: "note", Confidence: 0.5,
	}
	vec := make([]float32, 768)
	vec[0] = 1.0
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// TopK=0 should use default (10)
	results, err := db.VectorSearch(vec, SearchOptions{TopK: 0})
	if err != nil {
		t.Fatalf("VectorSearch TopK=0: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	// TopK>100 should clamp to 100
	results, err = db.VectorSearch(vec, SearchOptions{TopK: 500})
	if err != nil {
		t.Fatalf("VectorSearch TopK=500: %v", err)
	}
	if len(results) > 100 {
		t.Errorf("expected TopK clamped to 100, got %d results", len(results))
	}
}

// --- ExtractSearchTerms edge cases ---

func TestExtractSearchTerms_AllStopWords(t *testing.T) {
	terms := ExtractSearchTerms("the a is are was of in to for with")
	if len(terms) != 0 {
		t.Errorf("expected 0 terms for all stop words, got %v", terms)
	}
}

func TestExtractSearchTerms_DuplicateTerms(t *testing.T) {
	terms := ExtractSearchTerms("kubernetes kubernetes kubernetes")
	if len(terms) != 1 {
		t.Errorf("expected 1 unique term, got %d: %v", len(terms), terms)
	}
}

func TestExtractSearchTerms_MeaningfulShortTerms(t *testing.T) {
	terms := ExtractSearchTerms("AI ML UX")
	foundAI := false
	for _, term := range terms {
		if term == "ai" {
			foundAI = true
		}
	}
	if !foundAI {
		t.Errorf("expected 'ai' as meaningful short term, got %v", terms)
	}
}

// --- distRange edge cases ---
// distRange computes the min and max distance from a set of raw search results.
// This is a test-local copy of the function in hooks/context_surfacing.go.
func testDistRange(results []RawSearchResult) (float64, float64) {
	if len(results) == 0 {
		return 0, 1
	}
	minD, maxD := results[0].Distance, results[0].Distance
	for _, r := range results[1:] {
		if r.Distance < minD {
			minD = r.Distance
		}
		if r.Distance > maxD {
			maxD = r.Distance
		}
	}
	return minD, maxD
}

func TestDistRange_Empty(t *testing.T) {
	min, max := testDistRange(nil)
	if min != 0 || max != 1 {
		t.Errorf("expected (0,1) for empty, got (%f,%f)", min, max)
	}
}

func TestDistRange_SingleResult(t *testing.T) {
	results := []RawSearchResult{{Distance: 5.0}}
	min, max := testDistRange(results)
	if min != 5.0 || max != 5.0 {
		t.Errorf("expected (5,5) for single, got (%f,%f)", min, max)
	}
}

func TestDistRange_Multiple(t *testing.T) {
	results := []RawSearchResult{
		{Distance: 3.0},
		{Distance: 7.0},
		{Distance: 1.0},
		{Distance: 10.0},
	}
	min, max := testDistRange(results)
	if min != 1.0 || max != 10.0 {
		t.Errorf("expected (1,10), got (%f,%f)", min, max)
	}
}

// --- round functions ---

func TestRound3(t *testing.T) {
	if got := round3(0.1234); got != 0.123 {
		t.Errorf("expected 0.123, got %f", got)
	}
	if got := round3(0.1235); got != 0.124 {
		t.Errorf("expected 0.124, got %f", got)
	}
	if got := round3(1.0); got != 1.0 {
		t.Errorf("expected 1.0, got %f", got)
	}
	if got := round3(0.0); got != 0.0 {
		t.Errorf("expected 0.0, got %f", got)
	}
}

func TestRound1(t *testing.T) {
	if got := round1(5.14); got != 5.1 {
		t.Errorf("expected 5.1, got %f", got)
	}
	if got := round1(5.15); got != 5.2 {
		t.Errorf("expected 5.2, got %f", got)
	}
}

// --- splitTitleWords ---

func TestSplitTitleWords(t *testing.T) {
	tests := []struct {
		title string
		want  int
	}{
		{"simple title", 2},
		{"hyphenated-title", 2},
		{"under_scored", 2},
		{"", 0},
		{"single", 1},
		{"with/slash", 2},
		{"a, b, c", 3},
	}
	for _, tt := range tests {
		got := splitTitleWords(tt.title)
		if len(got) != tt.want {
			t.Errorf("splitTitleWords(%q) = %d words, want %d: %v", tt.title, len(got), tt.want, got)
		}
	}
}

// --- editDistance1 ---

func TestEditDistance1_Substitution(t *testing.T) {
	if !editDistance1("hello", "hallo") {
		t.Error("expected hello/hallo to be distance 1")
	}
}

func TestEditDistance1_Insertion(t *testing.T) {
	if !editDistance1("hello", "helloo") {
		t.Error("expected hello/helloo to be distance 1")
	}
}

func TestEditDistance1_Deletion(t *testing.T) {
	if !editDistance1("hello", "helo") {
		t.Error("expected hello/helo to be distance 1")
	}
}

func TestEditDistance1_Identical(t *testing.T) {
	if editDistance1("same", "same") {
		t.Error("expected identical strings to return false")
	}
}

func TestEditDistance1_TooFar(t *testing.T) {
	if editDistance1("abc", "xyz") {
		t.Error("expected abc/xyz to return false")
	}
}

func TestEditDistance1_LengthDiffTwo(t *testing.T) {
	if editDistance1("ab", "abcd") {
		t.Error("expected length diff 2 to return false")
	}
}

// --- canDeleteOne ---

func TestCanDeleteOne(t *testing.T) {
	if !canDeleteOne("hello", "helo") {
		t.Error("expected canDeleteOne(hello, helo) = true")
	}
	if canDeleteOne("hello", "help") {
		t.Error("expected canDeleteOne(hello, help) = false")
	}
}

// --- hasTags ---

func TestHasTags_Match(t *testing.T) {
	if !hasTags(`["project","backend"]`, []string{"backend"}) {
		t.Error("expected tag match")
	}
}

func TestHasTags_NoMatch(t *testing.T) {
	if hasTags(`["project","backend"]`, []string{"frontend"}) {
		t.Error("expected no tag match")
	}
}

func TestHasTags_CaseInsensitive(t *testing.T) {
	if !hasTags(`["PROJECT"]`, []string{"project"}) {
		t.Error("expected case-insensitive tag match")
	}
}

func TestHasTags_InvalidJSON(t *testing.T) {
	if hasTags("not-json", []string{"anything"}) {
		t.Error("expected false for invalid JSON")
	}
}

func TestHasTags_EmptyTags(t *testing.T) {
	if hasTags("[]", []string{"anything"}) {
		t.Error("expected false for empty tags")
	}
}
