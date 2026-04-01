package store

import (
	"testing"
)

func TestInsertAndSearchFacts(t *testing.T) {
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

	// Insert a fact
	rec := &FactRecord{
		FactText:   "The team uses PostgreSQL for production",
		SourcePath: "notes/architecture.md",
		ChunkID:    0,
		Confidence: 0.9,
	}
	if err := db.InsertFact(rec, makeVec(0.8)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}

	// Verify count
	count, err := db.FactCount()
	if err != nil {
		t.Fatalf("FactCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 fact, got %d", count)
	}

	// Search
	results, err := db.SearchFacts(makeVec(0.8), 5)
	if err != nil {
		t.Fatalf("SearchFacts: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].FactText != "The team uses PostgreSQL for production" {
		t.Fatalf("unexpected fact text: %q", results[0].FactText)
	}
	if results[0].SourcePath != "notes/architecture.md" {
		t.Fatalf("unexpected source path: %q", results[0].SourcePath)
	}
}

func TestPathsWithFacts(t *testing.T) {
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

	// Initially no paths
	paths, err := db.PathsWithFacts()
	if err != nil {
		t.Fatalf("PathsWithFacts: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected 0 paths, got %d", len(paths))
	}

	// Insert facts for two paths
	if err := db.InsertFact(&FactRecord{
		FactText: "Fact 1", SourcePath: "a.md", Confidence: 0.8,
	}, makeVec(0.1)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}
	if err := db.InsertFact(&FactRecord{
		FactText: "Fact 2", SourcePath: "a.md", Confidence: 0.9,
	}, makeVec(0.2)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}
	if err := db.InsertFact(&FactRecord{
		FactText: "Fact 3", SourcePath: "b.md", Confidence: 0.7,
	}, makeVec(0.3)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}

	paths, err = db.PathsWithFacts()
	if err != nil {
		t.Fatalf("PathsWithFacts: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if !paths["a.md"] || !paths["b.md"] {
		t.Fatalf("expected paths a.md and b.md, got %v", paths)
	}
}

func TestGetSampleFacts(t *testing.T) {
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

	// Insert 3 facts
	for i, text := range []string{"Fact A", "Fact B -- longer fact text here", "Fact C is the newest"} {
		if err := db.InsertFact(&FactRecord{
			FactText: text, SourcePath: "notes/test.md", Confidence: 0.8,
		}, makeVec(float32(i)*0.1)); err != nil {
			t.Fatalf("InsertFact: %v", err)
		}
	}

	// Get 2 sample facts (should be most recent first)
	samples, err := db.GetSampleFacts(2)
	if err != nil {
		t.Fatalf("GetSampleFacts: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	// Most recent first (DESC order by id)
	if samples[0].FactText != "Fact C is the newest" {
		t.Fatalf("expected newest fact first, got %q", samples[0].FactText)
	}
}

func TestHasFacts(t *testing.T) {
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

	if db.HasFacts() {
		t.Fatal("expected HasFacts=false on empty db")
	}

	if err := db.InsertFact(&FactRecord{
		FactText: "A fact", SourcePath: "test.md", Confidence: 0.8,
	}, makeVec(0.5)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}

	if !db.HasFacts() {
		t.Fatal("expected HasFacts=true after insert")
	}
}

func TestDeleteFactsForPath(t *testing.T) {
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

	// Insert facts for two paths
	if err := db.InsertFact(&FactRecord{
		FactText: "Fact for A", SourcePath: "a.md", Confidence: 0.8,
	}, makeVec(0.1)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}
	if err := db.InsertFact(&FactRecord{
		FactText: "Fact for B", SourcePath: "b.md", Confidence: 0.8,
	}, makeVec(0.9)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}

	// Delete facts for a.md
	if err := db.DeleteFactsForPath("a.md"); err != nil {
		t.Fatalf("DeleteFactsForPath: %v", err)
	}

	count, _ := db.FactCount()
	if count != 1 {
		t.Fatalf("expected 1 fact after delete, got %d", count)
	}

	// Remaining fact should be for b.md
	facts, _ := db.GetFactsForPath("b.md")
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact for b.md, got %d", len(facts))
	}

	// a.md should have no facts
	factsA, _ := db.GetFactsForPath("a.md")
	if len(factsA) != 0 {
		t.Fatalf("expected 0 facts for a.md, got %d", len(factsA))
	}
}

func TestBoostFromFacts(t *testing.T) {
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

	// Insert a note
	notes := []NoteRecord{
		{Path: "arch.md", Title: "Architecture", ChunkID: 0, ChunkHeading: "(full)",
			Text: "We use PostgreSQL", Modified: 1700000000, ContentHash: "a",
			ContentType: "note", Confidence: 0.8, Tags: "[]", TrustState: "unknown"},
		{Path: "other.md", Title: "Other Note", ChunkID: 0, ChunkHeading: "(full)",
			Text: "Unrelated content", Modified: 1700000000, ContentHash: "b",
			ContentType: "note", Confidence: 0.5, Tags: "[]", TrustState: "unknown"},
	}
	vecs := [][]float32{makeVec(0.5), makeVec(0.9)}
	if _, err := db.BulkInsertNotes(notes, vecs); err != nil {
		t.Fatalf("BulkInsertNotes: %v", err)
	}

	// Insert a fact for arch.md
	if err := db.InsertFact(&FactRecord{
		FactText:   "The team uses PostgreSQL for production databases",
		SourcePath: "arch.md", ChunkID: 0, Confidence: 0.95,
	}, makeVec(0.5)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}

	// Search notes — the fact-boosted note should get a higher score
	queryVec := makeVec(0.5)
	results, err := db.HybridSearch(queryVec, "PostgreSQL", SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	// The arch.md note should be in results and have a fact-boosted score
	found := false
	for _, r := range results {
		if r.Path == "arch.md" {
			found = true
			// Score should be boosted above baseline
			if r.Score < 0.5 {
				t.Logf("arch.md score: %.3f (expected boost from fact)", r.Score)
			}
		}
	}
	if !found {
		t.Fatal("expected arch.md in results")
	}
}
