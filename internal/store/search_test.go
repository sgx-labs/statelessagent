package store

import (
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
			Text: "This guide covers JWT authentication and OAuth2 flows.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/database-setup.md", Title: "Database Setup",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Instructions for setting up PostgreSQL and running migrations.",
			Modified: 1700000001, ContentHash: "h2", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/deploy-checklist.md", Title: "Deployment Checklist",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Steps to deploy the application to production servers.",
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
			Text: "Embeddings convert text into vector representations for semantic search.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/embedding-overview.md", Title: "Embedding Overview",
			Tags: "[]", ChunkID: 1, ChunkHeading: "Models",
			Text: "Popular embedding models include nomic-embed-text and text-embedding-3-small.",
			Modified: 1700000000, ContentHash: "h1c1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/api-reference.md", Title: "API Reference",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "The REST API exposes endpoints for CRUD operations.",
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
		Text: "Key architectural decisions made for the project.",
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
			Text: "Findings from the quarterly security review.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/performance-tuning.md", Title: "Performance Tuning",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Guidelines for optimizing query performance.",
			Modified: 1700000001, ContentHash: "h2", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/onboarding.md", Title: "Developer Onboarding",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Steps for new team members to get started.",
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
			Text: "Redis-based caching layer for API responses.",
			Modified: 1700000000, ContentHash: "h1", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/error-handling.md", Title: "Error Handling Patterns",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Centralized error handling with structured logging.",
			Modified: 1700000001, ContentHash: "h2", ContentType: "note", Confidence: 0.5,
		},
		{
			Path: "notes/testing-guide.md", Title: "Testing Guide",
			Tags: "[]", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Unit testing and integration testing best practices.",
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
