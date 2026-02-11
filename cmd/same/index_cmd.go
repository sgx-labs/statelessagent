package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func reindexCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Scan your notes and rebuild the search index",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReindex(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Re-embed all files regardless of changes")
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
			return runReindex(true)
		},
	}
}

func runReindex(force bool) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	indexer.Version = Version
	stats, err := indexer.Reindex(db, force)
	if err != nil {
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "ollama") || strings.Contains(errMsg, "connection") || strings.Contains(errMsg, "refused") {
			// Ollama-specific fallback — offer lite mode
			fmt.Fprintf(os.Stderr, "  Ollama not available — indexing with keyword search only.\n")
			fmt.Fprintf(os.Stderr, "  Start Ollama and run 'same reindex' again for semantic search.\n\n")
			stats, err = indexer.ReindexLite(db, force, nil)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("reindex failed: %w", err)
		}
	}

	data, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(data))
	return nil
}

func runStats() error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	stats := indexer.GetStats(db)
	data, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(data))
	return nil
}
