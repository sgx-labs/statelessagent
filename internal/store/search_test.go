package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSearchTerms(t *testing.T) {
	t.Run("normal query", func(t *testing.T) {
		terms := ExtractSearchTerms("how does authentication work")
		if len(terms) == 0 {
			t.Fatal("expected multiple terms, got none")
		}
		// None of the returned terms should be stop words
		for _, term := range terms {
			if searchStopWords[term] {
				t.Errorf("stop word %q should have been filtered", term)
			}
		}
		// "authentication" must be present
		found := false
		for _, term := range terms {
			if term == "authentication" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'authentication' in terms, got %v", terms)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		terms := ExtractSearchTerms("")
		if len(terms) != 0 {
			t.Errorf("expected empty result for empty input, got %v", terms)
		}
	})

	t.Run("special characters", func(t *testing.T) {
		terms := ExtractSearchTerms("JWT (tokens)")
		if len(terms) == 0 {
			t.Fatal("expected terms from query with special chars, got none")
		}
		// Should extract "jwt" and/or "tokens"
		foundMeaningful := false
		for _, term := range terms {
			if term == "jwt" || term == "tokens" {
				foundMeaningful = true
				break
			}
		}
		if !foundMeaningful {
			t.Errorf("expected meaningful terms like 'jwt' or 'tokens', got %v", terms)
		}
	})

	t.Run("single word", func(t *testing.T) {
		terms := ExtractSearchTerms("architecture")
		if len(terms) != 1 {
			t.Fatalf("expected exactly 1 term, got %d: %v", len(terms), terms)
		}
		if terms[0] != "architecture" {
			t.Errorf("expected 'architecture', got %q", terms[0])
		}
	})
}

func TestKeywordSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	notes := []NoteRecord{
		{
			Path: "notes/auth-guide.md", Title: "Authentication Guide",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "This guide covers JWT authentication and OAuth2 flows.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/database-setup.md", Title: "Database Setup",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Instructions for setting up PostgreSQL and running migrations.",
			Modified: 1700000001, ContentHash: "h2", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/deploy-checklist.md", Title: "Deployment Checklist",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Steps to deploy the application to production servers.",
			Modified: 1700000002, ContentHash: "h3", ContentType: "note", Confidence: 0.5,
		},
	}

	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	results, err := db.KeywordSearch([]string{"authentication", "jwt"}, 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result from KeywordSearch")
	}

	foundAuth := false
	for _, r := range results {
		if r.Path == "" {
			t.Error("result has empty Path")
		}
		if r.Title == "" {
			t.Error("result has empty Title")
		}
		if r.Path == "notes/auth-guide.md" {
			foundAuth = true
		}
	}
	if !foundAuth {
		t.Error("expected notes/auth-guide.md in keyword search results")
	}
}

func TestSearchAndNoteReadsHandleNullAgent(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)
	note := &NoteRecord{
		Path: "notes/null-agent.md", Title: "Null Agent",
		Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
		Text:     "Keyword fallback should still work.",
		Modified: 1700000000, ContentHash: "null-agent", ContentType: "note", Confidence: 0.5,
	}
	if err := db.InsertNote(note, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// Simulate legacy rows from pre-agent schema migration.
	if _, err := db.conn.Exec(`UPDATE vault_notes SET agent = NULL WHERE path = ?`, note.Path); err != nil {
		t.Fatalf("set NULL agent: %v", err)
	}

	results, err := db.KeywordSearch([]string{"fallback"}, 5)
	if err != nil {
		t.Fatalf("KeywordSearch with NULL agent: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Agent != "" {
		t.Fatalf("expected empty agent for NULL value, got %q", results[0].Agent)
	}

	notes, err := db.AllNotes()
	if err != nil {
		t.Fatalf("AllNotes with NULL agent: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].Agent != "" {
		t.Fatalf("expected empty note agent for NULL value, got %q", notes[0].Agent)
	}

	if err := db.PinNote(note.Path); err != nil {
		t.Fatalf("PinNote: %v", err)
	}
	pinned, err := db.GetPinnedNotes()
	if err != nil {
		t.Fatalf("GetPinnedNotes with NULL agent: %v", err)
	}
	if len(pinned) != 1 {
		t.Fatalf("expected 1 pinned note, got %d", len(pinned))
	}
	if pinned[0].Agent != "" {
		t.Fatalf("expected empty pinned agent for NULL value, got %q", pinned[0].Agent)
	}
}

func TestContentTermSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	notes := []NoteRecord{
		{
			Path: "notes/embedding-overview.md", Title: "Embedding Overview",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Embeddings convert text into vector representations for semantic search.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/embedding-overview.md", Title: "Embedding Overview",
			Tags: "[]", ChunkID: 1, ChunkHeading: "Models",
			Text:     "Popular embedding models include nomic-embed-text and text-embedding-3-small.",
			Modified: 1700000000, ContentHash: "h1c1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/api-reference.md", Title: "API Reference",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "The REST API exposes endpoints for CRUD operations.",
			Modified: 1700000001, ContentHash: "h2", ContentType: "note", Confidence: 0.5,
		},
	}

	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	// Search for terms that appear across chunks of the embedding note
	results, err := db.ContentTermSearch([]string{"embeddings", "vector", "semantic"}, 1, 10)
	if err != nil {
		t.Fatalf("ContentTermSearch: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result from ContentTermSearch")
	}

	foundEmbedding := false
	for _, r := range results {
		if r.Path == "notes/embedding-overview.md" {
			foundEmbedding = true
			break
		}
	}
	if !foundEmbedding {
		t.Error("expected notes/embedding-overview.md in ContentTermSearch results")
	}
}

func TestFuzzyTitleSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	rec := &NoteRecord{
		Path: "notes/arch-decisions.md", Title: "Architecture Decisions",
		Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
		Text:     "Key architectural decisions made for the project.",
		Modified: 1700000000, ContentHash: "h1", ContentType: "decision", Confidence: 0.8,
	}
	if err := db.InsertNote(rec, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	// Search with a typo: "architeture" (missing 'c') has edit distance 1
	// from "architecture". The function requires terms >= 5 chars.
	results, err := db.FuzzyTitleSearch([]string{"architeture"}, 10)
	if err != nil {
		t.Fatalf("FuzzyTitleSearch: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected FuzzyTitleSearch to find note despite typo")
	}

	found := false
	for _, r := range results {
		if r.Path == "notes/arch-decisions.md" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected notes/arch-decisions.md in fuzzy title search results")
	}
}

func TestKeywordSearchTitleMatch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	notes := []NoteRecord{
		{
			Path: "notes/security-audit.md", Title: "Security Audit Report",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Findings from the quarterly security review.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/performance-tuning.md", Title: "Performance Tuning",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Guidelines for optimizing query performance.",
			Modified: 1700000001, ContentHash: "h2", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/onboarding.md", Title: "Developer Onboarding",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Steps for new team members to get started.",
			Modified: 1700000002, ContentHash: "h3", ContentType: "note", Confidence: 0.5,
		},
	}

	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	// Search for terms matching the "Security Audit Report" title
	results, err := db.KeywordSearchTitleMatch([]string{"security", "audit"}, 1, 10)
	if err != nil {
		t.Fatalf("KeywordSearchTitleMatch: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result from KeywordSearchTitleMatch")
	}

	foundSecurity := false
	for _, r := range results {
		if r.Path == "notes/security-audit.md" {
			foundSecurity = true
			break
		}
	}
	if !foundSecurity {
		t.Error("expected notes/security-audit.md in KeywordSearchTitleMatch results")
	}

	// Test titleOnly variant
	resultsTitle, err := db.KeywordSearchTitleMatch([]string{"security", "audit"}, 1, 10, true)
	if err != nil {
		t.Fatalf("KeywordSearchTitleMatch (titleOnly): %v", err)
	}
	if len(resultsTitle) < 1 {
		t.Fatal("expected at least 1 result from KeywordSearchTitleMatch with titleOnly=true")
	}

	foundSecurityTitle := false
	for _, r := range resultsTitle {
		if r.Path == "notes/security-audit.md" {
			foundSecurityTitle = true
			break
		}
	}
	if !foundSecurityTitle {
		t.Error("expected notes/security-audit.md in titleOnly KeywordSearchTitleMatch results")
	}
}

func TestHybridSearch(t *testing.T) {
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

	notes := []NoteRecord{
		{
			Path: "notes/caching-strategy.md", Title: "Caching Strategy",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Redis-based caching layer for API responses.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/error-handling.md", Title: "Error Handling Patterns",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Centralized error handling with structured logging.",
			Modified: 1700000001, ContentHash: "h2", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/testing-guide.md", Title: "Testing Guide",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Unit testing and integration testing best practices.",
			Modified: 1700000002, ContentHash: "h3", ContentType: "note", Confidence: 0.5,
		},
	}

	vecs := [][]float32{makeVec(0.1), makeVec(0.5), makeVec(0.9)}
	for i := range notes {
		if err := db.InsertNote(&notes[i], vecs[i]); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	// Use a zero vector for the query — vector path will still execute
	// without panic. The keyword path should contribute results via title matching.
	queryVec := make([]float32, dim)
	results, err := db.HybridSearch(queryVec, "caching strategy", SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}

	// HybridSearch should not panic and should return some results
	// (at minimum from the vector path, which returns all 3 notes for any query)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result from HybridSearch")
	}

	// Verify results have non-empty fields
	for _, r := range results {
		if r.Path == "" {
			t.Error("result has empty Path")
		}
		if r.Title == "" {
			t.Error("result has empty Title")
		}
	}
}

// createTestVaultDB creates a temporary vault DB with notes for testing.
// Returns the DB path and a cleanup function.
func createTestVaultDB(t *testing.T, alias string, notes []NoteRecord) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, alias+".db")

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath(%s): %v", dbPath, err)
	}

	vec := make([]float32, 768)
	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d for %s: %v", i, alias, err)
		}
	}
	db.Close()
	return dbPath
}

func TestFederatedSearch_MultipleVaults(t *testing.T) {
	// Create two vault DBs with different notes
	devDBPath := createTestVaultDB(t, "dev", []NoteRecord{
		{
			Path: "notes/auth-design.md", Title: "Authentication Design",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "JWT-based authentication with refresh tokens.",
			Modified: 1700000000, ContentHash: "d1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/database-schema.md", Title: "Database Schema",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "PostgreSQL schema with user and session tables.",
			Modified: 1700000001, ContentHash: "d2", ContentType: "note", Confidence: 0.5,
		},
	})

	mktDBPath := createTestVaultDB(t, "marketing", []NoteRecord{
		{
			Path: "notes/launch-plan.md", Title: "Launch Plan",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Product launch timeline and marketing strategy.",
			Modified: 1700000000, ContentHash: "m1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/auth-messaging.md", Title: "Authentication Messaging",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "How to communicate our authentication security story.",
			Modified: 1700000001, ContentHash: "m2", ContentType: "note", Confidence: 0.5,
		},
	})

	vaultDBPaths := map[string]string{
		"dev":       devDBPath,
		"marketing": mktDBPath,
	}

	// Search without vectors (FTS5 fallback)
	results, err := FederatedSearch(vaultDBPaths, nil, "authentication", SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}

	// Should find results from both vaults
	foundDev := false
	foundMkt := false
	for _, r := range results {
		if r.Vault == "dev" {
			foundDev = true
		}
		if r.Vault == "marketing" {
			foundMkt = true
		}
		if r.Vault == "" {
			t.Error("result has empty Vault field")
		}
	}
	if !foundDev {
		t.Error("expected results from dev vault")
	}
	if !foundMkt {
		t.Error("expected results from marketing vault")
	}
}

func TestFederatedSearch_EmptyVaults(t *testing.T) {
	results, err := FederatedSearch(nil, nil, "test", SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("FederatedSearch with nil map: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil map, got %d", len(results))
	}

	results, err = FederatedSearch(map[string]string{}, nil, "test", SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("FederatedSearch with empty map: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty map, got %d", len(results))
	}
}

func TestFederatedSearch_InvalidDBPath(t *testing.T) {
	vaultDBPaths := map[string]string{
		"nonexistent": "/tmp/nonexistent-vault-db-that-does-not-exist-12345/vault.db",
	}
	// Should not error out, just skip the bad vault
	results, err := FederatedSearch(vaultDBPaths, nil, "test", SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("FederatedSearch with bad path: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent DB, got %d", len(results))
	}
}

func TestFederatedSearch_TopKLimit(t *testing.T) {
	// Create a vault with many notes
	var notes []NoteRecord
	for i := 0; i < 20; i++ {
		notes = append(notes, NoteRecord{
			Path: "notes/note-" + string(rune('a'+i)) + ".md", Title: "Note " + string(rune('A'+i)),
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "Content about authentication and security patterns.",
			Modified: float64(1700000000 + i), ContentHash: "h" + string(rune('a'+i)),
			ContentType: "note", Confidence: 0.5,
		})
	}
	dbPath := createTestVaultDB(t, "big", notes)

	vaultDBPaths := map[string]string{"big": dbPath}

	results, err := FederatedSearch(vaultDBPaths, nil, "authentication security", SearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}
}

func TestFederatedSearch_VaultAnnotation(t *testing.T) {
	dbPath := createTestVaultDB(t, "test-vault", []NoteRecord{
		{
			Path: "notes/test.md", Title: "Test Note",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text:     "This is a test note about architecture decisions.",
			Modified: 1700000000, ContentHash: "t1", ContentType: "note", Confidence: 0.5,
		},
	})

	results, err := FederatedSearch(
		map[string]string{"test-vault": dbPath},
		nil, "architecture", SearchOptions{TopK: 5},
	)
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].Vault != "test-vault" {
		t.Errorf("expected vault 'test-vault', got %q", results[0].Vault)
	}
}

func TestAllNotes(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	notes := []NoteRecord{
		{
			Path: "notes/public.md", Title: "Public Note",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Public content.", Modified: 1700000000,
			ContentHash: "p1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "_PRIVATE/secret.md", Title: "Secret Note",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Private content.", Modified: 1700000001,
			ContentHash: "s1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/public.md", Title: "Public Note",
			Tags: "[]", ChunkID: 1, ChunkHeading: "Section 2",
			Text: "Second chunk.", Modified: 1700000000,
			ContentHash: "p1c1", ContentType: "note", Confidence: 0.5,
		},
	}

	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	allNotes, err := db.AllNotes()
	if err != nil {
		t.Fatalf("AllNotes: %v", err)
	}

	// Should return only 1 note: the public one at chunk_id=0
	// _PRIVATE should be excluded, and chunk_id=1 should be excluded
	if len(allNotes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(allNotes))
	}
	if allNotes[0].Path != "notes/public.md" {
		t.Errorf("expected notes/public.md, got %s", allNotes[0].Path)
	}
}

func TestFederatedSearch_NoFTSNoVectors(t *testing.T) {
	// Create a DB but don't insert any notes — it won't have FTS or vectors
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	db.Close()

	// Delete any FTS tables that might have been created
	_ = os.Remove(dbPath)

	results, err := FederatedSearch(
		map[string]string{"empty": dbPath},
		nil, "test", SearchOptions{TopK: 5},
	)
	// Should gracefully skip the vault that can't be searched
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	// Results should be empty since the DB was removed
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestFederatedSearch_EmptyQuery(t *testing.T) {
	dbPath := createTestVaultDB(t, "q", []NoteRecord{
		{
			Path: "notes/test.md", Title: "Test", Tags: "[]",
			ChunkID: 0, ChunkHeading: "(full)", Text: "content",
			Modified: 1700000000, ContentHash: "t1", ContentType: "note", Confidence: 0.5,
		},
	})
	results, err := FederatedSearch(
		map[string]string{"q": dbPath},
		nil, "", SearchOptions{TopK: 5},
	)
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}

	// Whitespace-only query
	results, err = FederatedSearch(
		map[string]string{"q": dbPath},
		nil, "   ", SearchOptions{TopK: 5},
	)
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for whitespace query, got %d", len(results))
	}
}

func TestFederatedSearch_TooManyVaults(t *testing.T) {
	vaults := make(map[string]string)
	for i := 0; i < MaxFederatedVaults+1; i++ {
		vaults[fmt.Sprintf("vault%d", i)] = "/nonexistent"
	}
	_, err := FederatedSearch(vaults, nil, "test", SearchOptions{TopK: 5})
	if err == nil {
		t.Fatal("expected error for too many vaults")
	}
	if !strings.Contains(err.Error(), "too many vaults") {
		t.Errorf("expected 'too many vaults' error, got: %v", err)
	}
}

func TestFederatedSearch_PrivateNotesExcluded(t *testing.T) {
	// Notes with _PRIVATE paths should already be excluded by AllNotes/search
	// but verify they don't leak through federated search
	dbPath := createTestVaultDB(t, "priv", []NoteRecord{
		{
			Path: "notes/public.md", Title: "Public Note", Tags: "[]",
			ChunkID: 0, ChunkHeading: "(full)", Text: "public authentication content",
			Modified: 1700000000, ContentHash: "p1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "_PRIVATE/secret.md", Title: "Secret Auth Details", Tags: "[]",
			ChunkID: 0, ChunkHeading: "(full)", Text: "secret authentication keys",
			Modified: 1700000001, ContentHash: "s1", ContentType: "note", Confidence: 0.5,
		},
	})

	results, err := FederatedSearch(
		map[string]string{"priv": dbPath},
		nil, "authentication", SearchOptions{TopK: 10},
	)
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}

	for _, r := range results {
		upper := strings.ToUpper(r.Path)
		if strings.HasPrefix(upper, "_PRIVATE/") {
			t.Errorf("private note leaked through federated search: %s", r.Path)
		}
	}
}

func TestFederatedSearch_MixedVaultHealth(t *testing.T) {
	// One healthy vault + one broken vault = results from healthy one only
	goodPath := createTestVaultDB(t, "good", []NoteRecord{
		{
			Path: "notes/good.md", Title: "Good Note", Tags: "[]",
			ChunkID: 0, ChunkHeading: "(full)", Text: "healthy vault content test",
			Modified: 1700000000, ContentHash: "g1", ContentType: "note", Confidence: 0.5,
		},
	})

	results, err := FederatedSearch(
		map[string]string{
			"good":   goodPath,
			"broken": "/nonexistent/path/vault.db",
		},
		nil, "content test", SearchOptions{TopK: 10},
	)
	if err != nil {
		t.Fatalf("FederatedSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from the healthy vault")
	}
	for _, r := range results {
		if r.Vault != "good" {
			t.Errorf("expected results only from 'good' vault, got %q", r.Vault)
		}
	}
}

func TestMetadataFilterSearch(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	notes := []NoteRecord{
		{
			Path: "notes/auth-decision.md", Title: "Auth Decision",
			Tags: `["security","auth"]`, Domain: "engineering",
			ChunkID: 0, ChunkHeading: "(full)",
			Text: "We decided to use JWT for authentication.",
			Modified: 1700000001, ContentHash: "h1", ContentType: "decision", Confidence: 0.9,
		},
		{
			Path: "notes/deploy-handoff.md", Title: "Deploy Handoff",
			Tags: `["devops"]`, Domain: "operations",
			ChunkID: 0, ChunkHeading: "(full)",
			Text: "Handing off deployment pipeline work.",
			Modified: 1700000002, ContentHash: "h2", ContentType: "handoff", Confidence: 0.8,
		},
		{
			Path: "notes/api-research.md", Title: "API Research",
			Tags: `["api","research"]`, Domain: "engineering",
			ChunkID: 0, ChunkHeading: "(full)",
			Text: "Research on REST vs GraphQL approaches.",
			Modified: 1700000003, ContentHash: "h3", ContentType: "research", Confidence: 0.7,
		},
		{
			Path: "notes/old-note.md", Title: "Old Note",
			Tags: `["legacy"]`, Domain: "engineering",
			ChunkID: 0, ChunkHeading: "(full)",
			Text: "This note is outdated and stale.",
			Modified: 1700000000, ContentHash: "h4", ContentType: "note", Confidence: 0.5,
		},
	}

	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	// Set trust states
	if err := db.UpdateTrustState([]string{"notes/auth-decision.md"}, "validated"); err != nil {
		t.Fatalf("UpdateTrustState: %v", err)
	}
	if err := db.UpdateTrustState([]string{"notes/old-note.md"}, "stale"); err != nil {
		t.Fatalf("UpdateTrustState: %v", err)
	}
	if err := db.UpdateTrustState([]string{"notes/api-research.md"}, "contradicted"); err != nil {
		t.Fatalf("UpdateTrustState: %v", err)
	}

	t.Run("filter by trust_state stale", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK:       10,
			TrustState: "stale",
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 stale result, got %d", len(results))
		}
		if results[0].Path != "notes/old-note.md" {
			t.Errorf("expected old-note.md, got %s", results[0].Path)
		}
	})

	t.Run("filter by trust_state validated", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK:       10,
			TrustState: "validated",
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 validated result, got %d", len(results))
		}
		if results[0].Path != "notes/auth-decision.md" {
			t.Errorf("expected auth-decision.md, got %s", results[0].Path)
		}
	})

	t.Run("filter by content_type decision", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK:        10,
			ContentType: "decision",
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 decision result, got %d", len(results))
		}
		if results[0].Path != "notes/auth-decision.md" {
			t.Errorf("expected auth-decision.md, got %s", results[0].Path)
		}
	})

	t.Run("filter by domain engineering", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK:   10,
			Domain: "engineering",
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 engineering results, got %d", len(results))
		}
	})

	t.Run("filter by tag", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK: 10,
			Tags: []string{"security"},
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result with security tag, got %d", len(results))
		}
		if results[0].Path != "notes/auth-decision.md" {
			t.Errorf("expected auth-decision.md, got %s", results[0].Path)
		}
	})

	t.Run("combined filters trust_state + domain", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK:       10,
			TrustState: "contradicted",
			Domain:     "engineering",
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 contradicted+engineering result, got %d", len(results))
		}
		if results[0].Path != "notes/api-research.md" {
			t.Errorf("expected api-research.md, got %s", results[0].Path)
		}
	})

	t.Run("no matching filters", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK:        10,
			TrustState:  "stale",
			ContentType: "decision",
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 results for impossible combination, got %d", len(results))
		}
	})

	t.Run("case insensitive filters", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK:       10,
			TrustState: "STALE",
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 stale result with uppercase filter, got %d", len(results))
		}
	})

	t.Run("no filters returns all notes", func(t *testing.T) {
		results, err := db.MetadataFilterSearch(SearchOptions{
			TopK: 50,
		})
		if err != nil {
			t.Fatalf("MetadataFilterSearch: %v", err)
		}
		if len(results) != 4 {
			t.Fatalf("expected 4 results with no filters, got %d", len(results))
		}
	})
}

func TestRawToSearchResult(t *testing.T) {
	raw := RawSearchResult{
		NoteID:      42,
		Distance:    5.5,
		Path:        "notes/test.md",
		Title:       "Test Note",
		Heading:     "Section 1",
		Text:        "Some content here",
		Domain:      "engineering",
		Workstream:  "api-redesign",
		Agent:       "claude",
		Tags:        `["test","demo"]`,
		ContentType: "decision",
		Confidence:  0.8567,
		Modified:    1700000000,
		AccessCount: 5,
		TrustState:  "validated",
	}

	result := RawToSearchResult(raw, 0.75)

	if result.Path != raw.Path {
		t.Errorf("Path: got %q, want %q", result.Path, raw.Path)
	}
	if result.Title != raw.Title {
		t.Errorf("Title: got %q, want %q", result.Title, raw.Title)
	}
	if result.ChunkHeading != raw.Heading {
		t.Errorf("ChunkHeading: got %q, want %q", result.ChunkHeading, raw.Heading)
	}
	if result.Score != 0.75 {
		t.Errorf("Score: got %f, want 0.75", result.Score)
	}
	if result.Distance != 0 {
		t.Errorf("Distance: got %f, want 0 (RawToSearchResult always sets 0)", result.Distance)
	}
	if result.Snippet != raw.Text {
		t.Errorf("Snippet: got %q, want %q", result.Snippet, raw.Text)
	}
	if result.Domain != raw.Domain {
		t.Errorf("Domain: got %q, want %q", result.Domain, raw.Domain)
	}
	if result.Workstream != raw.Workstream {
		t.Errorf("Workstream: got %q, want %q", result.Workstream, raw.Workstream)
	}
	if result.Agent != raw.Agent {
		t.Errorf("Agent: got %q, want %q", result.Agent, raw.Agent)
	}
	if result.Tags != raw.Tags {
		t.Errorf("Tags: got %q, want %q", result.Tags, raw.Tags)
	}
	if result.ContentType != raw.ContentType {
		t.Errorf("ContentType: got %q, want %q", result.ContentType, raw.ContentType)
	}
	if result.Confidence != 0.857 { // round3(0.8567)
		t.Errorf("Confidence: got %f, want 0.857", result.Confidence)
	}
	if result.TrustState != raw.TrustState {
		t.Errorf("TrustState: got %q, want %q", result.TrustState, raw.TrustState)
	}
}

func TestRawToSearchResult_SnippetTruncation(t *testing.T) {
	longText := strings.Repeat("a", 600)
	raw := RawSearchResult{
		Path:       "notes/long.md",
		Title:      "Long Note",
		Text:       longText,
		TrustState: "unknown",
	}

	result := RawToSearchResult(raw, 0.5)
	if len(result.Snippet) != 500 {
		t.Errorf("Snippet length: got %d, want 500", len(result.Snippet))
	}
}

func TestApplyQueryTypeBoosts(t *testing.T) {
	t.Run("handoff boost", func(t *testing.T) {
		results := []SearchResult{
			{Path: "notes/handoff.md", ContentType: "handoff", Score: 0.6, TrustState: "unknown"},
			{Path: "notes/decision.md", ContentType: "decision", Score: 0.65, TrustState: "unknown"},
			{Path: "notes/note.md", ContentType: "note", Score: 0.7, TrustState: "unknown"},
		}
		boosts := map[string]float64{"handoff": 1.3}
		boosted := ApplyQueryTypeBoosts(results, boosts)

		// Handoff result should be boosted: 0.6 * 1.3 = 0.78
		if boosted[0].ContentType != "note" && boosted[0].ContentType != "handoff" {
			// After boosting, handoff (0.78) should rank above note (0.7) and decision (0.65)
		}
		// Find the handoff result
		var handoffScore float64
		for _, r := range boosted {
			if r.ContentType == "handoff" {
				handoffScore = r.Score
				break
			}
		}
		if handoffScore < 0.77 || handoffScore > 0.79 {
			t.Errorf("expected handoff score ~0.78, got %f", handoffScore)
		}
	})

	t.Run("no boost for unmatched types", func(t *testing.T) {
		results := []SearchResult{
			{Path: "notes/note.md", ContentType: "note", Score: 0.7, TrustState: "unknown"},
		}
		boosts := map[string]float64{"handoff": 1.3}
		boosted := ApplyQueryTypeBoosts(results, boosts)
		if boosted[0].Score != 0.7 {
			t.Errorf("expected no change to note score, got %f", boosted[0].Score)
		}
	})

	t.Run("stale penalty suppression", func(t *testing.T) {
		results := []SearchResult{
			{Path: "notes/stale.md", ContentType: "note", Score: 0.5, TrustState: "stale"},
			{Path: "notes/fresh.md", ContentType: "note", Score: 0.6, TrustState: "unknown"},
		}
		boosts := map[string]float64{"_suppress_stale_penalty": 1.0}
		boosted := ApplyQueryTypeBoosts(results, boosts)

		// Stale result should have its penalty reversed: 0.5 / 0.75 ≈ 0.667
		var staleScore float64
		for _, r := range boosted {
			if r.TrustState == "stale" {
				staleScore = r.Score
				break
			}
		}
		if staleScore < 0.66 || staleScore > 0.67 {
			t.Errorf("expected stale score ~0.667 after suppression, got %f", staleScore)
		}
	})

	t.Run("empty boosts is no-op", func(t *testing.T) {
		results := []SearchResult{
			{Path: "notes/a.md", Score: 0.8, TrustState: "unknown"},
		}
		boosted := ApplyQueryTypeBoosts(results, map[string]float64{})
		if boosted[0].Score != 0.8 {
			t.Errorf("expected no change, got %f", boosted[0].Score)
		}
	})

	t.Run("score capped at 1.0", func(t *testing.T) {
		results := []SearchResult{
			{Path: "notes/high.md", ContentType: "handoff", Score: 0.9, TrustState: "unknown"},
		}
		boosts := map[string]float64{"handoff": 1.3}
		boosted := ApplyQueryTypeBoosts(results, boosts)
		if boosted[0].Score > 1.0 {
			t.Errorf("score should be capped at 1.0, got %f", boosted[0].Score)
		}
	})

	t.Run("results re-sorted after boost", func(t *testing.T) {
		results := []SearchResult{
			{Path: "notes/note.md", ContentType: "note", Score: 0.7, TrustState: "unknown"},
			{Path: "notes/handoff.md", ContentType: "handoff", Score: 0.6, TrustState: "unknown"},
		}
		boosts := map[string]float64{"handoff": 1.3}
		boosted := ApplyQueryTypeBoosts(results, boosts)
		// After boost: handoff=0.78, note=0.7. Handoff should be first.
		if boosted[0].Path != "notes/handoff.md" {
			t.Errorf("expected handoff first after boost, got %s", boosted[0].Path)
		}
	})
}

func TestSearchOptions_TrustStateFilter_VectorSearch(t *testing.T) {
	// Verify that trust_state and content_type filters work in VectorSearch post-filtering.
	// This test uses FTS5Search since vector search requires real embeddings.
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vec := make([]float32, 768)

	notes := []NoteRecord{
		{
			Path: "notes/decision-a.md", Title: "Decision A",
			Tags: `["auth"]`, Domain: "engineering",
			ChunkID: 0, ChunkHeading: "(full)",
			Text: "Auth decision content searchterm-xyz",
			Modified: 1700000001, ContentHash: "ha", ContentType: "decision", Confidence: 0.9,
		},
		{
			Path: "notes/note-b.md", Title: "Note B",
			Tags: `["general"]`, Domain: "engineering",
			ChunkID: 0, ChunkHeading: "(full)",
			Text: "General note content searchterm-xyz",
			Modified: 1700000002, ContentHash: "hb", ContentType: "note", Confidence: 0.7,
		},
	}

	for i := range notes {
		if err := db.InsertNote(&notes[i], vec); err != nil {
			t.Fatalf("InsertNote %d: %v", i, err)
		}
	}

	if err := db.UpdateTrustState([]string{"notes/decision-a.md"}, "validated"); err != nil {
		t.Fatalf("UpdateTrustState: %v", err)
	}

	// FTS5Search should respect trust_state filter
	if db.FTSAvailable() {
		results, err := db.FTS5Search("searchterm-xyz", SearchOptions{
			TopK:       10,
			TrustState: "validated",
		})
		if err != nil {
			t.Fatalf("FTS5Search: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 validated FTS5 result, got %d", len(results))
		}
		if results[0].Path != "notes/decision-a.md" {
			t.Errorf("expected decision-a.md, got %s", results[0].Path)
		}

		// ContentType filter
		results, err = db.FTS5Search("searchterm-xyz", SearchOptions{
			TopK:        10,
			ContentType: "note",
		})
		if err != nil {
			t.Fatalf("FTS5Search content_type: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 note-type FTS5 result, got %d", len(results))
		}
		if results[0].Path != "notes/note-b.md" {
			t.Errorf("expected note-b.md, got %s", results[0].Path)
		}
	}
}
