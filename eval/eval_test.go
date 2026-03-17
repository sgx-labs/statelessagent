// Package eval provides automated retrieval quality evaluation for SAME.
//
// This test uses SAME's internal packages directly (store, indexer) to evaluate
// search quality against a curated test vault with known-good expected results.
//
// Run with:
//
//	go test ./eval/ -v -run TestRetrievalEval
//	go test ./eval/ -v -run TestRetrievalEval -count=1 -timeout 10m
package eval

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// testCase represents a single retrieval evaluation test case.
type testCase struct {
	ID                 int      `json:"id"`
	Query              string   `json:"query"`
	ExpectInResults    []string `json:"expect_in_results"`
	ExpectNote         string   `json:"expect_note"`
	ExpectNotInResults []string `json:"expect_not_in_results"`
	Category           string   `json:"category"`
	Negative           bool     `json:"negative"`
	Description        string   `json:"description"`
}

// evalResult records the outcome of a single test case.
type evalResult struct {
	ID             int     `json:"id"`
	Query          string  `json:"query"`
	Category       string  `json:"category"`
	Status         string  `json:"status"` // "PASS" or "FAIL"
	NoteRank       int     `json:"note_rank"`
	TermsFound     int     `json:"terms_found"`
	TermsTotal     int     `json:"terms_total"`
	ReciprocalRank float64 `json:"reciprocal_rank"`
	ResultPaths    []string `json:"result_paths"`
}

// categoryStats holds aggregate metrics per category.
type categoryStats struct {
	Total    int
	Pass     int
	Fail     int
	SumRR    float64
}

// TestRetrievalEval runs the full retrieval evaluation suite using SAME's
// internal store and search packages. It loads the test vault markdown files,
// inserts them into an in-memory database with mock embeddings, and evaluates
// keyword/FTS search quality against the expected results.
//
// Note: This test uses keyword-based search (FTS5 + LIKE) since generating
// real embeddings requires an external provider (Ollama/OpenAI). For full
// semantic eval with real embeddings, use run_eval.sh which indexes through
// the CLI.
func TestRetrievalEval(t *testing.T) {
	// Load test cases
	casesPath := filepath.Join(".", "retrieval_eval.json")
	casesData, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatalf("Failed to read test cases: %v", err)
	}

	var cases []testCase
	if err := json.Unmarshal(casesData, &cases); err != nil {
		t.Fatalf("Failed to parse test cases: %v", err)
	}

	if len(cases) == 0 {
		t.Fatal("No test cases found")
	}

	// Open in-memory database
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Load and index all vault notes
	vaultDir := filepath.Join(".", "test_vault")
	noteCount := indexVault(t, db, vaultDir)
	t.Logf("Indexed %d notes from test vault", noteCount)

	if noteCount == 0 {
		t.Fatal("No notes indexed from test vault")
	}

	// Run eval
	results := make([]evalResult, 0, len(cases))
	catStats := make(map[string]*categoryStats)

	for _, tc := range cases {
		if _, ok := catStats[tc.Category]; !ok {
			catStats[tc.Category] = &categoryStats{}
		}
		cs := catStats[tc.Category]
		cs.Total++

		result := evaluateCase(t, db, tc)
		results = append(results, result)

		if result.Status == "PASS" {
			cs.Pass++
		} else {
			cs.Fail++
		}
		cs.SumRR += result.ReciprocalRank
	}

	// Report summary
	totalPass := 0
	totalFail := 0
	totalRR := 0.0
	for _, r := range results {
		if r.Status == "PASS" {
			totalPass++
		} else {
			totalFail++
		}
		totalRR += r.ReciprocalRank
	}

	total := totalPass + totalFail
	recall := 0.0
	mrr := 0.0
	if total > 0 {
		recall = float64(totalPass) / float64(total) * 100
		mrr = totalRR / float64(total)
	}

	t.Logf("")
	t.Logf("========== Retrieval Eval Results ==========")
	t.Logf("Total: %d  Pass: %d  Fail: %d", total, totalPass, totalFail)
	t.Logf("Recall@5: %.1f%%", recall)
	t.Logf("MRR:      %.4f", mrr)
	t.Logf("")
	t.Logf("Per-Category:")
	for cat, cs := range catStats {
		catRecall := 0.0
		catMRR := 0.0
		if cs.Total > 0 {
			catRecall = float64(cs.Pass) / float64(cs.Total) * 100
			catMRR = cs.SumRR / float64(cs.Total)
		}
		t.Logf("  %-12s  %5.1f%% recall (%d/%d), MRR: %.4f", cat, catRecall, cs.Pass, cs.Total, catMRR)
	}

	// Write JSON results
	resultsDir := filepath.Join(".", "results")
	os.MkdirAll(resultsDir, 0o755)
	resultFile := filepath.Join(resultsDir, fmt.Sprintf("eval_go_%s.json", time.Now().Format("20060102_150405")))

	catSummary := make(map[string]interface{})
	for cat, cs := range catStats {
		catRecall := 0.0
		catMRR := 0.0
		if cs.Total > 0 {
			catRecall = float64(cs.Pass) / float64(cs.Total) * 100
			catMRR = cs.SumRR / float64(cs.Total)
		}
		catSummary[cat] = map[string]interface{}{
			"total":      cs.Total,
			"pass":       cs.Pass,
			"fail":       cs.Fail,
			"recall_pct": math.Round(catRecall*10) / 10,
			"mrr":        math.Round(catMRR*10000) / 10000,
		}
	}

	output := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
		"mode":      "keyword-only (no embeddings)",
		"summary": map[string]interface{}{
			"total":          total,
			"pass":           totalPass,
			"fail":           totalFail,
			"recall_at_5_pct": math.Round(recall*10) / 10,
			"mrr":            math.Round(mrr*10000) / 10000,
		},
		"categories": catSummary,
		"details":    results,
	}

	jsonData, _ := json.MarshalIndent(output, "", "  ")
	os.WriteFile(resultFile, jsonData, 0o644)
	t.Logf("")
	t.Logf("Results saved to: %s", resultFile)

	// Optional: fail if recall drops below threshold
	thresholdEnv := os.Getenv("EVAL_RECALL_THRESHOLD")
	if thresholdEnv != "" {
		var threshold float64
		fmt.Sscanf(thresholdEnv, "%f", &threshold)
		if threshold > 0 && recall < threshold {
			t.Errorf("Recall %.1f%% is below threshold %.1f%%", recall, threshold)
		}
	}
}

// indexVault loads all markdown files from the test vault directory and indexes
// them into the database. Returns the number of notes indexed.
func indexVault(t *testing.T, db *store.DB, vaultDir string) int {
	t.Helper()

	count := 0
	err := filepath.WalkDir(vaultDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip .same directory
			if d.Name() == ".same" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			t.Logf("Warning: failed to read %s: %v", path, err)
			return nil
		}

		// Parse frontmatter and body
		parsed := indexer.ParseNote(string(content))

		// Compute relative path from vault root
		relPath, err := filepath.Rel(vaultDir, path)
		if err != nil {
			relPath = filepath.Base(path)
		}

		// Use title from frontmatter, or derive from filename
		title := parsed.Meta.Title
		if title == "" {
			base := filepath.Base(path)
			title = strings.TrimSuffix(base, ".md")
			title = strings.ReplaceAll(title, "-", " ")
			title = titleCase(title)
		}

		// Tags as JSON array
		tagsJSON := "[]"
		if len(parsed.Meta.Tags) > 0 {
			if j, err := json.Marshal(parsed.Meta.Tags); err == nil {
				tagsJSON = string(j)
			}
		}

		// Confidence
		confidence := 0.5
		if parsed.Meta.ContentType == "decision" {
			confidence = 0.85
		} else if parsed.Meta.ContentType == "architecture" {
			confidence = 0.8
		}

		// Content hash
		hash := fmt.Sprintf("%x", sha256.Sum256(content))[:16]

		// Create a deterministic mock embedding based on content hash.
		// This won't produce meaningful semantic similarity, but allows
		// keyword search, FTS5, and title matching to function.
		vec := makeMockEmbedding(string(content))

		// Full note as single chunk (chunk_id=0)
		bodyText := parsed.Meta.Title + "\n\n" + parsed.Body
		rec := &store.NoteRecord{
			Path:         relPath,
			Title:        title,
			Tags:         tagsJSON,
			Domain:       parsed.Meta.Domain,
			Workstream:   parsed.Meta.Workstream,
			Agent:        parsed.Meta.Agent,
			ChunkID:      0,
			ChunkHeading: "(full)",
			Text:         bodyText,
			Modified:     float64(time.Now().Unix()),
			ContentHash:  hash,
			ContentType:  parsed.Meta.ContentType,
			ReviewBy:     parsed.Meta.ReviewBy,
			Confidence:   confidence,
		}

		if err := db.InsertNote(rec, vec); err != nil {
			t.Logf("Warning: failed to insert %s: %v", relPath, err)
			return nil
		}

		// Set trust_state from frontmatter if present (e.g. stale notes)
		if parsed.Meta.TrustState != "" {
			if err := db.UpdateTrustState([]string{relPath}, parsed.Meta.TrustState); err != nil {
				t.Logf("Warning: failed to set trust_state for %s: %v", relPath, err)
			}
		}

		count++
		return nil
	})

	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	return count
}

// makeMockEmbedding generates a deterministic 768-dim mock embedding vector.
// Distributes content hash bytes across the vector dimensions so that
// different content produces different vectors. This is NOT semantically
// meaningful — it just avoids all-zero vectors which break sqlite-vec.
func makeMockEmbedding(content string) []float32 {
	const dim = 768
	vec := make([]float32, dim)

	h := sha256.Sum256([]byte(content))
	for i := 0; i < dim; i++ {
		// Use hash bytes cyclically, normalize to [-1, 1]
		b := h[i%32]
		vec[i] = (float32(b) - 128.0) / 128.0
	}

	// Normalize to unit vector
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}

	return vec
}

// evaluateCase runs a single test case and returns the result.
func evaluateCase(t *testing.T, db *store.DB, tc testCase) evalResult {
	t.Helper()

	result := evalResult{
		ID:       tc.ID,
		Query:    tc.Query,
		Category: tc.Category,
	}

	// Run keyword search (primary method for Go test since we have mock embeddings)
	terms := store.ExtractSearchTerms(tc.Query)
	var searchResults []store.RawSearchResult

	// Try multiple search strategies and combine results
	if len(terms) > 0 {
		// Strategy 1: Keyword search on content
		kwResults, err := db.KeywordSearch(terms, 10)
		if err != nil {
			t.Logf("[%d] KeywordSearch error: %v", tc.ID, err)
		} else {
			searchResults = append(searchResults, kwResults...)
		}

		// Strategy 2: Title matching
		titleResults, err := db.KeywordSearchTitleMatch(terms, 1, 10)
		if err == nil {
			searchResults = append(searchResults, titleResults...)
		}

		// Strategy 3: Content term search (cross-chunk)
		if len(terms) >= 2 {
			ctResults, err := db.ContentTermSearch(terms, 1, 10)
			if err == nil {
				searchResults = append(searchResults, ctResults...)
			}
		}
	}

	// Also try FTS5 if available
	if db.FTSAvailable() {
		ftsResults, err := db.FTS5Search(tc.Query, store.SearchOptions{TopK: 5})
		if err == nil {
			// Convert FTS results to raw for uniform handling
			for _, r := range ftsResults {
				searchResults = append(searchResults, store.RawSearchResult{
					Path:  r.Path,
					Title: r.Title,
					Text:  r.Snippet,
				})
			}
		}
	}

	// Strategy 4: Metadata filter search for trust/confidence/provenance queries.
	// When the query is about metadata (e.g. "stale notes", "low confidence"),
	// MetadataFilterSearch finds notes by their trust_state/confidence metadata
	// rather than relying on text content matching alone.
	// These results are PREPENDED so they get priority during deduplication.
	metaHints := memory.InferMetadataFilters(tc.Query)
	if metaHints.IsMetadataQuery {
		metaOpts := store.SearchOptions{
			TopK:       10,
			TrustState: metaHints.TrustState,
		}
		metaResults, metaErr := db.MetadataFilterSearch(metaOpts)
		if metaErr == nil {
			var metaRaw []store.RawSearchResult
			for _, mr := range metaResults {
				metaRaw = append(metaRaw, store.RawSearchResult{
					Path:       mr.Path,
					Title:      mr.Title,
					Text:       mr.Snippet,
					TrustState: mr.TrustState,
					Confidence: mr.Confidence,
				})
			}
			// Prepend metadata results so they get picked first during dedup
			searchResults = append(metaRaw, searchResults...)
		}
	}

	// Deduplicate by path, keep first occurrence (best result from any strategy)
	seen := make(map[string]bool)
	var deduped []store.RawSearchResult
	for _, r := range searchResults {
		if seen[r.Path] {
			continue
		}
		seen[r.Path] = true
		deduped = append(deduped, r)
		if len(deduped) >= 5 {
			break
		}
	}

	// Record result paths
	for _, r := range deduped {
		result.ResultPaths = append(result.ResultPaths, r.Path)
	}

	// Negative test evaluation
	if tc.Negative {
		foundForbidden := false
		if len(tc.ExpectNotInResults) > 0 {
			for _, r := range deduped {
				combined := strings.ToLower(r.Title + " " + r.Text)
				for _, forbidden := range tc.ExpectNotInResults {
					if strings.Contains(combined, strings.ToLower(forbidden)) {
						foundForbidden = true
						break
					}
				}
				if foundForbidden {
					break
				}
			}
		}

		if foundForbidden {
			result.Status = "FAIL"
			t.Logf("[%d] FAIL (negative): %s — found forbidden terms in results", tc.ID, tc.Query)
		} else {
			result.Status = "PASS"
			t.Logf("[%d] PASS (negative): %s", tc.ID, tc.Query)
		}
		return result
	}

	// Positive test evaluation
	// Check 1: Expected note in top-5
	noteFound := false
	noteRank := 0
	if tc.ExpectNote != "" {
		for i, r := range deduped {
			if r.Path == tc.ExpectNote {
				noteFound = true
				noteRank = i + 1
				break
			}
		}
	}
	result.NoteRank = noteRank

	// Check 2: Expected terms in results
	termsFound := 0
	termsTotal := len(tc.ExpectInResults)
	if termsTotal > 0 {
		// Combine all result text
		var combined strings.Builder
		for _, r := range deduped {
			combined.WriteString(strings.ToLower(r.Title))
			combined.WriteString(" ")
			combined.WriteString(strings.ToLower(r.Text))
			combined.WriteString(" ")
		}
		combinedStr := combined.String()

		for _, term := range tc.ExpectInResults {
			if strings.Contains(combinedStr, strings.ToLower(term)) {
				termsFound++
			}
		}
	}
	result.TermsFound = termsFound
	result.TermsTotal = termsTotal

	// Pass if: (a) expected note found, OR (b) >= half the expected terms found
	passed := false
	if noteFound {
		passed = true
	} else if termsTotal > 0 && termsFound >= (termsTotal+1)/2 {
		passed = true
	}

	// Reciprocal rank
	if noteFound && noteRank > 0 {
		result.ReciprocalRank = 1.0 / float64(noteRank)
	}

	if passed {
		result.Status = "PASS"
		if noteFound {
			t.Logf("[%d] PASS: %s (note@%d, terms: %d/%d)", tc.ID, tc.Query, noteRank, termsFound, termsTotal)
		} else {
			t.Logf("[%d] PASS: %s (terms: %d/%d, note not in top-5)", tc.ID, tc.Query, termsFound, termsTotal)
		}
	} else {
		result.Status = "FAIL"
		paths := make([]string, 0, len(deduped))
		for _, r := range deduped {
			paths = append(paths, r.Path)
		}
		t.Logf("[%d] FAIL: %s (note: %v, terms: %d/%d, got: %v)", tc.ID, tc.Query, noteFound, termsFound, termsTotal, paths)
	}

	return result
}

// titleCase converts a string to title case without the deprecated strings.Title.
func titleCase(s string) string {
	prev := ' '
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(prev) || unicode.IsPunct(prev) {
			prev = r
			return unicode.ToTitle(r)
		}
		prev = r
		return r
	}, s)
}
