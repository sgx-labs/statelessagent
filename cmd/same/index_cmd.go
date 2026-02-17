package main

import (
	"fmt"
	"os"
	"strings"

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
		return config.ErrNoDatabase
	}
	defer db.Close()

	var progress indexer.ProgressFunc
	if verbose {
		progress = func(current, total int, path string) {
			fmt.Printf("  [%d/%d] %s\n", current, total, path)
		}
	}

	indexer.Version = Version
	stats, err := indexer.ReindexWithProgress(db, force, progress)
	if err != nil {
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "ollama") ||
			strings.Contains(errMsg, "connection") ||
			strings.Contains(errMsg, "refused") ||
			strings.Contains(errMsg, "embedding backend unavailable") ||
			strings.Contains(errMsg, "no embeddings generated") ||
			strings.Contains(errMsg, "keyword-only mode") ||
			strings.Contains(errMsg, `provider is "none"`) {
			// Embedding unavailable/disabled — offer lite mode
			fmt.Fprintf(os.Stderr, "  Embedding backend unavailable or disabled — indexing with keyword search only.\n")
			fmt.Fprintf(os.Stderr, "  Start Ollama (or switch embedding provider) and run 'same reindex' again for semantic search.\n\n")
			stats, err = indexer.ReindexLite(db, force, progress)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("reindex failed: %w", err)
		}
	}

	fmt.Println()
	fmt.Printf("  %sReindex complete%s\n\n", cli.Bold, cli.Reset)
	fmt.Printf("  Files scanned:   %d\n", stats.TotalFiles)
	fmt.Printf("  Newly indexed:   %d\n", stats.NewlyIndexed)
	fmt.Printf("  Unchanged:       %d\n", stats.SkippedUnchanged)
	if stats.Errors > 0 {
		fmt.Printf("  Errors:          %s%d%s\n", cli.Yellow, stats.Errors, cli.Reset)
	}
	fmt.Printf("  Notes in index:  %d\n", stats.NotesInIndex)
	fmt.Printf("  Chunks in index: %d\n", stats.ChunksInIndex)
	fmt.Printf("\n  %sTip: Run 'same watch' in another terminal to auto-reindex as you edit notes.%s\n", cli.Dim, cli.Reset)
	return nil
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
