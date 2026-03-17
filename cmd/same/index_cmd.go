package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func reindexCmd() *cobra.Command {
	var (
		force   bool
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Scan your notes and rebuild the search index",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReindex(force, verbose)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Re-embed all files regardless of changes")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show each file being processed")
	return cmd
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show how many notes are indexed",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStats()
		},
	}
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Rebuild index from scratch (replaces old data)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReindex(true, false)
		},
	}
}

func runReindex(force bool, verbose bool) error {
	db, err := store.Open()
	if err != nil {
		return userError("No SAME vault found", "Run 'same init' first.")
	}
	defer db.Close()

	// Set up context with signal handling for graceful cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\n  Stopping... press Ctrl+C again to force quit\n")
			cancel()
			// Wait for second signal to force quit
			<-sigCh
			os.Exit(1)
		case <-ctx.Done():
		}
	}()
	defer signal.Stop(sigCh)

	var liteProgress indexer.ProgressFunc
	if verbose {
		liteProgress = func(current, total int, path string) {
			fmt.Printf("  [%d/%d] %s\n", current, total, path)
		}
	}

	fmt.Printf("  Graph extraction: %s\n", graphModeSummary(config.GraphLLMMode()))

	indexer.Version = Version

	// Progressive mode: FTS5 first (fast), then embeddings (slow).
	// Keyword search works immediately after Phase 1.
	embedProgress := func(completed, total int) {
		fmt.Fprintf(os.Stderr, "\r  Embedding: %d/%d notes (keyword search active)", completed, total)
	}

	stats, embResult, err := indexer.ReindexProgressive(ctx, db, force, liteProgress, embedProgress)
	if err != nil && !errors.Is(err, indexer.ErrCanceled) {
		return fmt.Errorf("reindex failed: %w", err)
	}

	// Clear the embedding progress line if it was printed
	if embResult != nil && embResult.Total > 0 {
		fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", 60))
		if embResult.Completed == embResult.Total {
			fmt.Fprintf(os.Stderr, "  All notes embedded. Semantic search ready.\n")
		} else if errors.Is(err, indexer.ErrCanceled) {
			fmt.Fprintf(os.Stderr, "  Embedding paused: %d/%d notes done. Resume with 'same reindex'.\n",
				embResult.Completed, embResult.Total)
		}
	}

	fmt.Println()
	if stats != nil && stats.Canceled {
		fmt.Printf("  %sReindex canceled by user. %d of %d notes indexed.%s\n\n",
			cli.Yellow, stats.NewlyIndexed, stats.TotalFiles, cli.Reset)
	} else {
		fmt.Printf("  %sReindex complete%s\n\n", cli.Bold, cli.Reset)
	}
	if stats != nil {
		fmt.Printf("  Files scanned:   %d\n", stats.TotalFiles)
		fmt.Printf("  Newly indexed:   %d\n", stats.NewlyIndexed)
		fmt.Printf("  Unchanged:       %d\n", stats.SkippedUnchanged)
		if stats.Errors > 0 {
			fmt.Printf("  Errors:          %s%d%s\n", cli.Yellow, stats.Errors, cli.Reset)
		}
		fmt.Printf("  Notes in index:  %d\n", stats.NotesInIndex)
		fmt.Printf("  Chunks in index: %d\n", stats.ChunksInIndex)
	}
	searchMode := "keyword-only"
	if db.HasVectors() {
		searchMode = "semantic"
	}
	fmt.Printf("  Search mode:    %s\n", searchMode)
	fmt.Printf("  Graph mode:     %s\n", graphModeSummary(config.GraphLLMMode()))
	fmt.Printf("  Graph role:     additive (works with search, not a replacement)\n")
	fmt.Printf("\n  %sTip: Run 'same watch' in another terminal to auto-reindex as you edit notes.%s\n", cli.Dim, cli.Reset)
	return nil
}

func graphModeSummary(mode string) string {
	switch mode {
	case "local-only":
		return "LLM local-only + regex fallback"
	case "on":
		return "LLM enabled + regex fallback"
	default:
		return "regex-only (default)"
	}
}

func runStats() error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	stats := indexer.GetStats(db)
	fmt.Println()
	fmt.Printf("  %sIndex Statistics%s\n\n", cli.Bold, cli.Reset)
	for _, key := range []string{
		"total_notes_in_index", "total_chunks_in_index",
		"embedding_model", "embedding_dimensions",
		"db_size_mb", "status",
	} {
		if v, ok := stats[key]; ok {
			label := strings.ReplaceAll(key, "_", " ")
			label = strings.ToUpper(label[:1]) + label[1:]
			fmt.Printf("  %-22s %v\n", label+":", v)
		}
	}
	fmt.Println()
	return nil
}
