package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	memory "github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func benchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bench",
		Short: "Test how fast search is on your vault",
		Long:  "Measure cold-start, search, embedding, and database performance.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBench()
		},
	}
}

type benchResult struct {
	Name    string `json:"name"`
	Latency string `json:"latency_ms"`
	Detail  string `json:"detail,omitempty"`
}

func runBench() error {
	fmt.Println("SAME Performance Benchmark")
	fmt.Println("==========================")
	fmt.Println()

	var results []benchResult

	// 1. Database open (cold start)
	t0 := time.Now()
	db, err := store.Open()
	dbOpen := time.Since(t0)
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	results = append(results, benchResult{
		Name:    "DB open (cold start)",
		Latency: fmt.Sprintf("%.1f", float64(dbOpen.Microseconds())/1000.0),
		Detail:  fmt.Sprintf("%d notes, %d chunks", noteCount, chunkCount),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 2. Embedding latency (single query)
	client, provErr := newEmbedProvider()
	if provErr != nil {
		results = append(results, benchResult{
			Name:    "Embedding",
			Latency: "FAILED",
			Detail:  provErr.Error(),
		})
		fmt.Printf("  %-30s %8s     %s\n", "Embedding", "FAILED", provErr.Error())
		printBenchSummary(results)
		return nil
	}
	testQuery := "what decisions were made about the memory system architecture"
	t0 = time.Now()
	queryVec, err := client.GetQueryEmbedding(testQuery)
	embedLatency := time.Since(t0)
	embedLabel := fmt.Sprintf("Embedding (%s)", client.Name())
	if err != nil {
		results = append(results, benchResult{
			Name:    embedLabel,
			Latency: "FAILED",
			Detail:  err.Error(),
		})
		fmt.Printf("  %-30s %8s     %s\n", embedLabel, "FAILED", err.Error())
	} else {
		results = append(results, benchResult{
			Name:    embedLabel,
			Latency: fmt.Sprintf("%.1f", float64(embedLatency.Microseconds())/1000.0),
			Detail:  fmt.Sprintf("%d dimensions", len(queryVec)),
		})
		fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)
	}

	if queryVec == nil {
		fmt.Println("\n  Skipping search benchmarks (embedding failed).")
		printBenchSummary(results)
		return nil
	}

	// 3. Vector search (vanilla, KNN only)
	t0 = time.Now()
	searchResults, err := db.VectorSearch(queryVec, store.SearchOptions{TopK: 10})
	searchLatency := time.Since(t0)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	results = append(results, benchResult{
		Name:    "Vector search (top-10)",
		Latency: fmt.Sprintf("%.1f", float64(searchLatency.Microseconds())/1000.0),
		Detail:  fmt.Sprintf("%d results", len(searchResults)),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 4. Raw search + composite scoring
	t0 = time.Now()
	rawResults, _ := db.VectorSearchRaw(queryVec, 50)
	_ = rawResults
	rawSearchLatency := time.Since(t0)
	results = append(results, benchResult{
		Name:    "Raw search (top-50)",
		Latency: fmt.Sprintf("%.1f", float64(rawSearchLatency.Microseconds())/1000.0),
		Detail:  fmt.Sprintf("%d raw results", len(rawResults)),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 5. Composite scoring (CPU only, no I/O)
	t0 = time.Now()
	for i := 0; i < 1000; i++ {
		for _, r := range rawResults {
			memory.CompositeScore(0.8, r.Modified, r.Confidence, r.ContentType, 0.5, 0.4, 0.1)
		}
	}
	compositeDur := time.Since(t0)
	opsPerSec := float64(1000*len(rawResults)) / compositeDur.Seconds()
	results = append(results, benchResult{
		Name:    "Composite scoring",
		Latency: fmt.Sprintf("%.3f", float64(compositeDur.Microseconds())/1000.0/1000.0),
		Detail:  fmt.Sprintf("%.0f scores/sec (1000 x %d)", opsPerSec, len(rawResults)),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 6. End-to-end: embed + search + score (what a hook actually does)
	t0 = time.Now()
	vec2, _ := client.GetQueryEmbedding("recent session handoffs and decisions")
	raw2, _ := db.VectorSearchRaw(vec2, 12)
	for _, r := range raw2 {
		memory.CompositeScore(0.8, r.Modified, r.Confidence, r.ContentType, 0.5, 0.4, 0.1)
	}
	e2eLatency := time.Since(t0)
	results = append(results, benchResult{
		Name:    "End-to-end (hook sim)",
		Latency: fmt.Sprintf("%.1f", float64(e2eLatency.Microseconds())/1000.0),
		Detail:  "embed + search + score",
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	printBenchSummary(results)

	// Output JSON for programmatic consumption
	data, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println("\n" + string(data))
	return nil
}

func printBenchSummary(results []benchResult) {
	fmt.Println()
	fmt.Println("Summary:")

	// Find the embed and search latencies to calculate overhead
	var embedMs, searchMs, e2eMs float64
	for _, r := range results {
		v, err := strconv.ParseFloat(r.Latency, 64)
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(r.Name, "Embedding"):
			embedMs = v
		case r.Name == "Vector search (top-10)":
			searchMs = v
		case r.Name == "End-to-end (hook sim)":
			e2eMs = v
		}
	}

	if embedMs > 0 && searchMs > 0 {
		goOverhead := e2eMs - embedMs
		fmt.Printf("  Embedding:        %.0fms (network I/O, dominates latency)\n", embedMs)
		fmt.Printf("  Go overhead:      %.1fms (search + scoring + I/O)\n", goOverhead)
		fmt.Printf("  Total e2e:        %.0fms\n", e2eMs)
	}
}
