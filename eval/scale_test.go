// Package eval provides scale testing for SAME at large vault sizes.
//
// Run with:
//
//	go test ./eval/ -v -run TestScale -timeout 120s
package eval

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/graph"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// scaleConfig controls the scale test parameters.
type scaleConfig struct {
	TotalNotes       int
	NotePct          float64 // 0.60
	DecisionPct      float64 // 0.20
	HandoffPct       float64 // 0.10
	ResearchPct      float64 // 0.10
	StalePct         float64 // fraction of notes marked stale
	ContradictedPct  float64 // fraction marked contradicted
	FTSQueryCount    int     // number of FTS5 search queries to average
	MetaQueryCount   int     // number of metadata filter queries to average
}

func defaultScaleConfig() scaleConfig {
	return scaleConfig{
		TotalNotes:      10000,
		NotePct:         0.60,
		DecisionPct:     0.20,
		HandoffPct:      0.10,
		ResearchPct:     0.10,
		StalePct:        0.05,
		ContradictedPct: 0.03,
		FTSQueryCount:   20,
		MetaQueryCount:  20,
	}
}

// scaleResults holds all timing and count data for JSON output.
type scaleResults struct {
	Timestamp         string  `json:"timestamp"`
	TotalNotes        int     `json:"total_notes"`
	TotalChunks       int     `json:"total_chunks"`
	IndexTimeMs       int64   `json:"index_time_ms"`
	SearchAvgMs       float64 `json:"search_avg_ms"`
	SearchMode        string  `json:"search_mode"`
	MetaFilterAvgMs   float64 `json:"meta_filter_avg_ms"`
	NoteCountMs       float64 `json:"note_count_ms"`
	ChunkCountMs      float64 `json:"chunk_count_ms"`
	GraphStatsMs      float64 `json:"graph_stats_ms"`
	MemBeforeMB       float64 `json:"mem_before_mb"`
	MemAfterMB        float64 `json:"mem_after_mb"`
	MemDeltaMB        float64 `json:"mem_delta_mb"`
	GraphNodes        int     `json:"graph_nodes"`
	GraphEdges        int     `json:"graph_edges"`
	SearchQueryCount  int     `json:"search_query_count"`
	MetaFilterCount   int     `json:"meta_filter_query_count"`
}

// TestScale generates 10K synthetic notes, indexes them (FTS5-only),
// and measures latency for search, metadata filter, counts, and graph stats.
func TestScale(t *testing.T) {
	cfg := defaultScaleConfig()

	// --- Memory before ---
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// --- Open in-memory DB ---
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// --- Generate notes ---
	t.Logf("Generating %d synthetic notes...", cfg.TotalNotes)
	records := generateNotes(cfg)
	t.Logf("Generated %d note records", len(records))

	// --- Index (bulk insert + FTS rebuild) ---
	t.Log("Indexing notes (FTS5-only)...")
	indexStart := time.Now()

	_, err = db.BulkInsertNotesLite(records)
	if err != nil {
		t.Fatalf("BulkInsertNotesLite: %v", err)
	}

	if err := db.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	// Set trust_state on a subset of notes
	var stalePaths, contradictedPaths []string
	staleCount := int(float64(cfg.TotalNotes) * cfg.StalePct)
	contradictedCount := int(float64(cfg.TotalNotes) * cfg.ContradictedPct)
	for i := 0; i < len(records); i++ {
		if records[i].ChunkID != 0 {
			continue
		}
		if staleCount > 0 {
			stalePaths = append(stalePaths, records[i].Path)
			staleCount--
		} else if contradictedCount > 0 {
			contradictedPaths = append(contradictedPaths, records[i].Path)
			contradictedCount--
		}
	}
	if len(stalePaths) > 0 {
		if err := db.UpdateTrustState(stalePaths, "stale"); err != nil {
			t.Fatalf("UpdateTrustState(stale): %v", err)
		}
	}
	if len(contradictedPaths) > 0 {
		if err := db.UpdateTrustState(contradictedPaths, "contradicted"); err != nil {
			t.Fatalf("UpdateTrustState(contradicted): %v", err)
		}
	}

	// Populate some graph data from the indexed notes
	gdb := graph.NewDB(db.Conn())
	populateGraphData(t, gdb, records)

	indexDuration := time.Since(indexStart)
	t.Logf("Index time: %v", indexDuration)

	// --- Verify counts ---
	noteCount, err := db.NoteCount()
	if err != nil {
		t.Fatalf("NoteCount: %v", err)
	}
	chunkCount, err := db.ChunkCount()
	if err != nil {
		t.Fatalf("ChunkCount: %v", err)
	}
	t.Logf("NoteCount=%d  ChunkCount=%d", noteCount, chunkCount)

	if noteCount < cfg.TotalNotes {
		t.Fatalf("Expected at least %d notes, got %d", cfg.TotalNotes, noteCount)
	}

	// --- Search latency (FTS5 if available, else LIKE-based keyword search) ---
	searchQueries := []string{
		"authentication middleware implementation",
		"database migration strategy",
		"API rate limiting design",
		"error handling patterns",
		"deployment pipeline configuration",
		"caching layer optimization",
		"security audit findings",
		"performance benchmarks results",
		"refactoring legacy code",
		"testing integration endpoints",
		"logging observability metrics",
		"microservice communication patterns",
		"session management tokens",
		"webhook retry logic",
		"dependency injection container",
		"schema validation approach",
		"concurrent worker pool",
		"graceful shutdown handler",
		"feature flag rollout",
		"incident postmortem analysis",
	}

	useFTS := db.FTSAvailable()
	searchMode := "keyword-LIKE"
	if useFTS {
		searchMode = "FTS5"
	}
	t.Logf("Search mode: %s", searchMode)

	var totalSearchMs float64
	for _, q := range searchQueries[:cfg.FTSQueryCount] {
		start := time.Now()
		if useFTS {
			_, err = db.FTS5Search(q, store.SearchOptions{TopK: 10})
		} else {
			terms := store.ExtractSearchTerms(q)
			_, err = db.KeywordSearch(terms, 10)
		}
		elapsed := time.Since(start)
		if err != nil {
			t.Logf("Search(%q) error: %v", q, err)
			continue
		}
		ms := float64(elapsed.Microseconds()) / 1000.0
		totalSearchMs += ms
	}
	avgSearchMs := totalSearchMs / float64(cfg.FTSQueryCount)
	t.Logf("%s search avg latency: %.2fms (%d queries)", searchMode, avgSearchMs, cfg.FTSQueryCount)

	// --- MetadataFilterSearch latency ---
	metaFilters := []store.SearchOptions{
		{TopK: 20, TrustState: "stale"},
		{TopK: 20, TrustState: "contradicted"},
		{TopK: 20, TrustState: "validated"},
		{TopK: 20, ContentType: "decision"},
		{TopK: 20, ContentType: "handoff"},
		{TopK: 20, ContentType: "research"},
		{TopK: 20, ContentType: "note"},
		{TopK: 20, Domain: "backend"},
		{TopK: 20, Domain: "infrastructure"},
		{TopK: 20, Domain: "security"},
		{TopK: 20, TrustState: "stale", ContentType: "decision"},
		{TopK: 20, Domain: "backend", ContentType: "note"},
		{TopK: 20, TrustState: "unknown"},
		{TopK: 20, ContentType: "decision", Domain: "frontend"},
		{TopK: 20, TrustState: "stale", Domain: "backend"},
		{TopK: 50, ContentType: "handoff"},
		{TopK: 50, TrustState: "contradicted"},
		{TopK: 100, ContentType: "note"},
		{TopK: 100, TrustState: "stale"},
		{TopK: 200, ContentType: "decision"},
	}

	var totalMetaMs float64
	for _, opts := range metaFilters[:cfg.MetaQueryCount] {
		start := time.Now()
		results, err := db.MetadataFilterSearch(opts)
		elapsed := time.Since(start)
		if err != nil {
			t.Logf("MetadataFilterSearch error: %v", err)
			continue
		}
		ms := float64(elapsed.Microseconds()) / 1000.0
		totalMetaMs += ms
		_ = results
	}
	avgMetaMs := totalMetaMs / float64(cfg.MetaQueryCount)
	t.Logf("MetadataFilterSearch avg latency: %.2fms (%d queries)", avgMetaMs, cfg.MetaQueryCount)

	// --- NoteCount / ChunkCount query time ---
	ncStart := time.Now()
	_, _ = db.NoteCount()
	ncMs := float64(time.Since(ncStart).Microseconds()) / 1000.0

	ccStart := time.Now()
	_, _ = db.ChunkCount()
	ccMs := float64(time.Since(ccStart).Microseconds()) / 1000.0
	t.Logf("NoteCount: %.2fms  ChunkCount: %.2fms", ncMs, ccMs)

	// --- Graph stats query time ---
	gsStart := time.Now()
	stats, err := gdb.GetStats()
	gsDuration := time.Since(gsStart)
	gsMs := float64(gsDuration.Microseconds()) / 1000.0
	if err != nil {
		t.Logf("Graph GetStats error: %v", err)
	} else {
		t.Logf("Graph stats: nodes=%d edges=%d avg_degree=%.2f  latency=%.2fms",
			stats.TotalNodes, stats.TotalEdges, stats.AvgDegree, gsMs)
	}

	// --- Memory after ---
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	memBeforeMB := float64(memBefore.Alloc) / 1024 / 1024
	memAfterMB := float64(memAfter.Alloc) / 1024 / 1024
	memDeltaMB := memAfterMB - memBeforeMB
	t.Logf("Memory: before=%.1fMB after=%.1fMB delta=%.1fMB", memBeforeMB, memAfterMB, memDeltaMB)

	// --- Build results ---
	graphNodes, graphEdges := 0, 0
	if stats != nil {
		graphNodes = stats.TotalNodes
		graphEdges = stats.TotalEdges
	}

	results := scaleResults{
		Timestamp:       time.Now().Format(time.RFC3339),
		TotalNotes:      noteCount,
		TotalChunks:     chunkCount,
		IndexTimeMs:     indexDuration.Milliseconds(),
		SearchAvgMs:     avgSearchMs,
		SearchMode:      searchMode,
		MetaFilterAvgMs: avgMetaMs,
		NoteCountMs:     ncMs,
		ChunkCountMs:    ccMs,
		GraphStatsMs:    gsMs,
		MemBeforeMB:     memBeforeMB,
		MemAfterMB:      memAfterMB,
		MemDeltaMB:      memDeltaMB,
		GraphNodes:      graphNodes,
		GraphEdges:      graphEdges,
		SearchQueryCount: cfg.FTSQueryCount,
		MetaFilterCount: cfg.MetaQueryCount,
	}

	// --- Save results JSON ---
	resultsDir := filepath.Join(".", "results")
	os.MkdirAll(resultsDir, 0o755)
	resultFile := filepath.Join(resultsDir, fmt.Sprintf("scale_%s.json", time.Now().Format("20060102_150405")))

	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatalf("Marshal results: %v", err)
	}
	if err := os.WriteFile(resultFile, jsonData, 0o644); err != nil {
		t.Fatalf("Write results: %v", err)
	}
	t.Logf("Results saved to: %s", resultFile)

	// --- Performance assertions ---
	t.Log("")
	t.Log("========== Performance Assertions ==========")

	if indexDuration.Seconds() > 30 {
		t.Errorf("FAIL: Index time %.1fs exceeds 30s limit", indexDuration.Seconds())
	} else {
		t.Logf("PASS: Index time %.1fs < 30s", indexDuration.Seconds())
	}

	// LIKE-based search scans full text and is slower than FTS5.
	// Use 100ms limit for FTS5, 200ms for keyword-LIKE fallback.
	searchLimit := 100.0
	if !useFTS {
		searchLimit = 200.0
	}
	if avgSearchMs > searchLimit {
		t.Errorf("FAIL: %s search avg %.2fms exceeds %.0fms limit", searchMode, avgSearchMs, searchLimit)
	} else {
		t.Logf("PASS: %s search avg %.2fms < %.0fms", searchMode, avgSearchMs, searchLimit)
	}

	if avgMetaMs > 50 {
		t.Errorf("FAIL: MetadataFilter avg %.2fms exceeds 50ms limit", avgMetaMs)
	} else {
		t.Logf("PASS: MetadataFilter avg %.2fms < 50ms", avgMetaMs)
	}
}

// --- Note generation ---

var (
	// Content type distribution boundaries (cumulative).
	contentTypes = []struct {
		name      string
		cumWeight float64
	}{
		{"note", 0.60},
		{"decision", 0.80},
		{"handoff", 0.90},
		{"research", 1.00},
	}

	domains = []string{
		"backend", "frontend", "infrastructure", "security",
		"data", "platform", "mobile", "devops", "observability", "testing",
	}

	workstreams = []string{
		"auth-revamp", "api-v2", "cloud-migration", "perf-sprint",
		"compliance-audit", "design-system", "data-pipeline", "ci-cd-overhaul",
		"mobile-launch", "observability-v3",
	}

	tagPool = []string{
		"golang", "rust", "typescript", "python", "sql", "graphql",
		"docker", "kubernetes", "terraform", "nginx", "redis", "postgres",
		"architecture", "refactoring", "performance", "security", "testing",
		"api", "microservice", "monolith", "migration", "deployment",
		"monitoring", "logging", "tracing", "alerting",
		"authentication", "authorization", "encryption", "compliance",
		"caching", "indexing", "search", "pagination",
	}

	// Words for generating realistic developer content.
	techNouns = []string{
		"endpoint", "middleware", "handler", "service", "controller", "repository",
		"schema", "migration", "index", "query", "transaction", "connection",
		"pipeline", "workflow", "deployment", "container", "cluster", "node",
		"token", "session", "credential", "certificate", "key", "secret",
		"metric", "trace", "span", "log", "alert", "dashboard",
		"cache", "queue", "stream", "buffer", "pool", "channel",
		"module", "package", "library", "framework", "runtime", "binary",
		"test", "benchmark", "fixture", "mock", "stub", "assertion",
		"branch", "commit", "merge", "rebase", "tag", "release",
		"interface", "struct", "method", "function", "goroutine", "mutex",
	}

	techVerbs = []string{
		"implement", "refactor", "optimize", "debug", "fix", "migrate",
		"deploy", "configure", "monitor", "test", "validate", "authenticate",
		"serialize", "deserialize", "parse", "encode", "decode", "compress",
		"cache", "index", "search", "paginate", "filter", "aggregate",
		"provision", "scale", "replicate", "partition", "shard", "backup",
		"retry", "timeout", "throttle", "circuit-break", "fallback", "recover",
	}

	techAdjs = []string{
		"concurrent", "distributed", "asynchronous", "stateless", "idempotent",
		"immutable", "ephemeral", "persistent", "resilient", "scalable",
		"composable", "reusable", "modular", "testable", "observable",
		"encrypted", "compressed", "cached", "indexed", "partitioned",
	}

	sentenceTemplates = []string{
		"We need to %s the %s %s to improve %s.",
		"The %s %s currently handles %s but should be %s.",
		"After analyzing the %s, we decided to %s the %s %s.",
		"The %s approach for the %s %s reduces latency by %s.",
		"Consider using a %s %s instead of the current %s %s.",
		"The team agreed to %s the %s %s before the next release.",
		"Performance testing showed the %s %s needs %s optimization.",
		"The %s %s was causing issues in production due to %s state.",
		"We should %s the %s %s to handle %s traffic patterns.",
		"The existing %s %s doesn't support %s operations properly.",
	}
)

func pickContentType(rng *rand.Rand) string {
	r := rng.Float64()
	for _, ct := range contentTypes {
		if r < ct.cumWeight {
			return ct.name
		}
	}
	return "note"
}

func pickN(rng *rand.Rand, pool []string, min, max int) []string {
	n := min + rng.Intn(max-min+1)
	if n > len(pool) {
		n = len(pool)
	}
	// Fisher-Yates partial shuffle
	picked := make([]string, len(pool))
	copy(picked, pool)
	for i := 0; i < n; i++ {
		j := i + rng.Intn(len(picked)-i)
		picked[i], picked[j] = picked[j], picked[i]
	}
	return picked[:n]
}

func generateSentence(rng *rand.Rand) string {
	tmpl := sentenceTemplates[rng.Intn(len(sentenceTemplates))]
	// Fill format verbs: each %s gets a random technical word
	words := make([]interface{}, 4)
	for i := range words {
		switch rng.Intn(3) {
		case 0:
			words[i] = techVerbs[rng.Intn(len(techVerbs))]
		case 1:
			words[i] = techNouns[rng.Intn(len(techNouns))]
		case 2:
			words[i] = techAdjs[rng.Intn(len(techAdjs))]
		}
	}
	return fmt.Sprintf(tmpl, words...)
}

func generateParagraph(rng *rand.Rand, sentenceCount int) string {
	sentences := make([]string, sentenceCount)
	for i := range sentences {
		sentences[i] = generateSentence(rng)
	}
	result := ""
	for i, s := range sentences {
		if i > 0 {
			result += " "
		}
		result += s
	}
	return result
}

func generateNoteBody(rng *rand.Rand, contentType string) string {
	// Target 200-500 words. Each sentence is roughly 10-15 words.
	// So 15-40 sentences, spread across 3-6 paragraphs.
	paraCount := 3 + rng.Intn(4) // 3-6 paragraphs
	body := ""

	// Type-specific intro
	switch contentType {
	case "decision":
		body += "## Decision\n\n"
		body += generateParagraph(rng, 3+rng.Intn(3)) + "\n\n"
		body += "## Context\n\n"
		body += generateParagraph(rng, 3+rng.Intn(4)) + "\n\n"
		body += "## Consequences\n\n"
		for i := 0; i < 2+rng.Intn(3); i++ {
			body += fmt.Sprintf("- %s\n", generateSentence(rng))
		}
		return body
	case "handoff":
		body += "## Status\n\n"
		body += generateParagraph(rng, 2+rng.Intn(2)) + "\n\n"
		body += "## What Was Done\n\n"
		for i := 0; i < 3+rng.Intn(4); i++ {
			body += fmt.Sprintf("- %s\n", generateSentence(rng))
		}
		body += "\n## Next Steps\n\n"
		for i := 0; i < 2+rng.Intn(3); i++ {
			body += fmt.Sprintf("- %s\n", generateSentence(rng))
		}
		return body
	case "research":
		body += "## Summary\n\n"
		body += generateParagraph(rng, 3+rng.Intn(3)) + "\n\n"
		body += "## Findings\n\n"
		for i := 0; i < 3+rng.Intn(5); i++ {
			body += fmt.Sprintf("### Finding %d\n\n", i+1)
			body += generateParagraph(rng, 2+rng.Intn(2)) + "\n\n"
		}
		return body
	}

	// Default "note" type
	for i := 0; i < paraCount; i++ {
		sentCount := 3 + rng.Intn(5) // 3-7 sentences per paragraph
		body += generateParagraph(rng, sentCount) + "\n\n"
	}
	return body
}

func generateTitle(rng *rand.Rand, idx int, contentType string) string {
	noun1 := techNouns[rng.Intn(len(techNouns))]
	noun2 := techNouns[rng.Intn(len(techNouns))]
	adj := techAdjs[rng.Intn(len(techAdjs))]
	verb := techVerbs[rng.Intn(len(techVerbs))]

	switch contentType {
	case "decision":
		patterns := []string{
			fmt.Sprintf("Decision: %s %s %s", verb, adj, noun1),
			fmt.Sprintf("ADR-%04d: %s %s approach", idx, adj, noun1),
			fmt.Sprintf("Decision on %s vs %s for %s", noun1, noun2, adj),
		}
		return patterns[rng.Intn(len(patterns))]
	case "handoff":
		patterns := []string{
			fmt.Sprintf("Handoff: %s %s %s", noun1, verb, noun2),
			fmt.Sprintf("Session handoff — %s %s work", adj, noun1),
			fmt.Sprintf("Handoff %04d: %s %s status", idx, noun1, noun2),
		}
		return patterns[rng.Intn(len(patterns))]
	case "research":
		patterns := []string{
			fmt.Sprintf("Research: %s %s patterns", adj, noun1),
			fmt.Sprintf("Investigation into %s %s", noun1, noun2),
			fmt.Sprintf("Evaluating %s for %s %s", noun1, adj, noun2),
		}
		return patterns[rng.Intn(len(patterns))]
	}

	// Default note titles
	patterns := []string{
		fmt.Sprintf("%s %s implementation notes", adj, noun1),
		fmt.Sprintf("How to %s the %s %s", verb, adj, noun1),
		fmt.Sprintf("%s %s and %s integration", noun1, noun2, adj),
		fmt.Sprintf("Notes on %s %s %s", verb, adj, noun1),
	}
	return patterns[rng.Intn(len(patterns))]
}

// generateNotes creates cfg.TotalNotes synthetic NoteRecords.
func generateNotes(cfg scaleConfig) []store.NoteRecord {
	rng := rand.New(rand.NewSource(42)) // deterministic for reproducibility
	records := make([]store.NoteRecord, 0, cfg.TotalNotes)

	baseTime := float64(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix())

	for i := 0; i < cfg.TotalNotes; i++ {
		ct := pickContentType(rng)
		title := generateTitle(rng, i, ct)
		domain := domains[rng.Intn(len(domains))]
		workstream := workstreams[rng.Intn(len(workstreams))]
		tags := pickN(rng, tagPool, 1, 4)
		body := generateNoteBody(rng, ct)

		tagsJSON, _ := json.Marshal(tags)

		path := fmt.Sprintf("%s/%s/%04d-%s.md", domain, ct, i, slugify(title, 40))
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))[:16]

		// Vary modification times across the year
		modified := baseTime + float64(rng.Intn(365*24*3600))

		confidence := 0.5
		switch ct {
		case "decision":
			confidence = 0.8 + rng.Float64()*0.2
		case "handoff":
			confidence = 0.6 + rng.Float64()*0.3
		case "research":
			confidence = 0.4 + rng.Float64()*0.4
		default:
			confidence = 0.3 + rng.Float64()*0.5
		}

		rec := store.NoteRecord{
			Path:        path,
			Title:       title,
			Tags:        string(tagsJSON),
			Domain:      domain,
			Workstream:  workstream,
			ChunkID:     0,
			ChunkHeading: "(full)",
			Text:        title + "\n\n" + body,
			Modified:    modified,
			ContentHash: hash,
			ContentType: ct,
			Confidence:  confidence,
		}
		records = append(records, rec)
	}

	return records
}

// slugify converts a title to a URL-friendly slug, truncated to maxLen.
func slugify(s string, maxLen int) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			result = append(result, c)
		} else if c >= 'A' && c <= 'Z' {
			result = append(result, c+32) // lowercase
		} else if c == ' ' || c == '-' || c == '_' {
			if len(result) > 0 && result[len(result)-1] != '-' {
				result = append(result, '-')
			}
		}
	}
	// Trim trailing dash
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	if len(result) > maxLen {
		result = result[:maxLen]
	}
	return string(result)
}

// populateGraphData inserts graph nodes and edges for a sample of notes
// so that graph stats queries have data to work with.
func populateGraphData(t *testing.T, gdb *graph.DB, records []store.NoteRecord) {
	t.Helper()

	// Insert a node for each unique domain and workstream,
	// plus a sample of note nodes, and edges between them.
	domainNodes := make(map[string]int64)
	wsNodes := make(map[string]int64)

	// Create domain entity nodes
	for _, d := range domains {
		id, err := gdb.UpsertNode(&graph.Node{
			Type:       graph.NodeEntity,
			Name:       "domain:" + d,
			Properties: "{}",
		})
		if err != nil {
			t.Logf("UpsertNode(domain:%s): %v", d, err)
			continue
		}
		domainNodes[d] = id
	}

	// Create workstream entity nodes
	for _, ws := range workstreams {
		id, err := gdb.UpsertNode(&graph.Node{
			Type:       graph.NodeEntity,
			Name:       "workstream:" + ws,
			Properties: "{}",
		})
		if err != nil {
			t.Logf("UpsertNode(workstream:%s): %v", ws, err)
			continue
		}
		wsNodes[ws] = id
	}

	// Create note nodes and edges for every 10th note (1000 nodes)
	for i := 0; i < len(records); i += 10 {
		rec := records[i]
		noteID, err := gdb.UpsertNode(&graph.Node{
			Type:       graph.NodeNote,
			Name:       rec.Path,
			Properties: fmt.Sprintf(`{"content_type":%q}`, rec.ContentType),
		})
		if err != nil {
			continue
		}

		// Edge: note -> domain
		if domID, ok := domainNodes[rec.Domain]; ok {
			_, _ = gdb.UpsertEdge(&graph.Edge{
				SourceID:     noteID,
				TargetID:     domID,
				Relationship: graph.RelRelatedTo,
				Weight:       1.0,
				Properties:   "{}",
			})
		}

		// Edge: note -> workstream
		if wsID, ok := wsNodes[rec.Workstream]; ok {
			_, _ = gdb.UpsertEdge(&graph.Edge{
				SourceID:     noteID,
				TargetID:     wsID,
				Relationship: graph.RelAffects,
				Weight:       1.0,
				Properties:   "{}",
			})
		}
	}
}
