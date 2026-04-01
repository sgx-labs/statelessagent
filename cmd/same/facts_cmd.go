package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func factsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "facts",
		Short: "View and manage extracted atomic facts",
		Long: `Show fact statistics and sample facts from your vault.

Facts are atomic knowledge entries extracted from your notes using an LLM.
They provide precision search: searches hit facts first, then return the
linked source notes for full context.

Examples:
  same facts                    Show fact count and sample facts
  same facts search "query"     Search facts directly
  same facts extract            Run fact extraction on the current vault`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFactsShow()
		},
	}

	cmd.AddCommand(factsSearchCmd())
	cmd.AddCommand(factsExtractCmd())

	return cmd
}

func factsSearchCmd() *cobra.Command {
	var (
		topK    int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search facts by meaning",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			return runFactsSearch(query, topK, jsonOut)
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 10, "Number of results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func factsExtractCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "extract",
		Short: "Extract facts from indexed notes (requires LLM)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFactsExtract()
		},
	}
}

func runFactsShow() error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	count, err := db.FactCount()
	if err != nil {
		return fmt.Errorf("fact count: %w", err)
	}

	fmt.Println()
	fmt.Printf("  %sFact Statistics%s\n\n", cli.Bold, cli.Reset)
	fmt.Printf("  Total facts:     %d\n", count)

	if count == 0 {
		fmt.Printf("\n  No facts extracted yet.\n")
		fmt.Printf("  Run %ssame facts extract%s or %ssame reindex --extract-facts%s to extract facts from your notes.\n\n",
			cli.Bold, cli.Reset, cli.Bold, cli.Reset)
		return nil
	}

	// Show sample facts
	samples, err := db.GetSampleFacts(5)
	if err != nil {
		return fmt.Errorf("get sample facts: %w", err)
	}

	if len(samples) > 0 {
		fmt.Printf("\n  %sRecent facts:%s\n", cli.Dim, cli.Reset)
		for _, f := range samples {
			text := f.FactText
			if len(text) > 100 {
				text = text[:100] + "..."
			}
			fmt.Printf("    - %s\n", text)
			fmt.Printf("      %ssource: %s (confidence: %.0f%%)%s\n",
				cli.Dim, f.SourcePath, f.Confidence*100, cli.Reset)
		}
	}
	fmt.Println()
	return nil
}

func runFactsSearch(query string, topK int, jsonOut bool) error {
	if strings.TrimSpace(query) == "" {
		return userError("Empty search query", "Provide a search term: same facts search \"your query\"")
	}

	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	if !db.HasFacts() {
		fmt.Printf("\n  No facts extracted yet. Run %ssame facts extract%s first.\n\n",
			cli.Bold, cli.Reset)
		return nil
	}

	client, err := newEmbedProvider()
	if err != nil {
		return fmt.Errorf("embedding provider unavailable: %w", err)
	}

	queryVec, err := client.GetQueryEmbedding(query)
	if err != nil {
		return fmt.Errorf("embed query: %w", err)
	}

	results, err := db.SearchFacts(queryVec, topK)
	if err != nil {
		return fmt.Errorf("search facts: %w", err)
	}

	if len(results) == 0 {
		if jsonOut {
			fmt.Println("[]")
		} else {
			fmt.Printf("\n  No matching facts found.\n\n")
		}
		return nil
	}

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Println()
	for i, r := range results {
		fmt.Printf("%d. %s\n", i+1, r.FactText)
		fmt.Printf("   %ssource: %s  distance: %.1f  confidence: %.0f%%%s\n",
			cli.Dim, r.SourcePath, r.Distance, r.Confidence*100, cli.Reset)
	}
	fmt.Println()
	return nil
}

func runFactsExtract() error {
	db, err := store.Open()
	if err != nil {
		return userError("No SAME vault found", "Run 'same init' first.")
	}
	defer db.Close()

	ctx := context.Background()
	result := runFactExtraction(ctx, db)
	if result == nil {
		return nil // message already printed by runFactExtraction
	}

	totalFacts, _ := db.FactCount()
	fmt.Printf("\n  %sFact extraction complete%s\n", cli.Bold, cli.Reset)
	fmt.Printf("  Notes processed: %d\n", result.Completed)
	fmt.Printf("  Facts extracted: %d (total in vault: %d)\n", result.Facts, totalFacts)
	if result.Failed > 0 {
		fmt.Printf("  Failed:          %s%d%s\n", cli.Yellow, result.Failed, cli.Reset)
	}
	if result.Skipped > 0 {
		fmt.Printf("  Already done:    %d\n", result.Skipped)
	}
	fmt.Println()

	return nil
}
