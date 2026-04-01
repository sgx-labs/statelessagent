package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
)

func ignoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ignore",
		Short: "View or manage .sameignore patterns",
		Long: `View and manage the .sameignore file that controls which files are excluded
from SAME indexing. Works like .gitignore — glob patterns, one per line.

Examples:
  same ignore              Show current ignore patterns
  same ignore add "*.log"  Add a pattern
  same ignore reset        Reset to defaults`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIgnoreShow()
		},
	}

	addCmd := &cobra.Command{
		Use:   "add <pattern>",
		Short: "Add a pattern to .sameignore",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIgnoreAdd(args[0])
		},
	}

	resetCmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset .sameignore to defaults",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIgnoreReset()
		},
	}

	cmd.AddCommand(addCmd, resetCmd)
	return cmd
}

func runIgnoreShow() error {
	vaultPath := config.VaultPath()
	sameignorePath := filepath.Join(vaultPath, ".sameignore")

	ip := indexer.LoadSameignore(vaultPath)
	if ip == nil {
		fmt.Printf("No .sameignore file found at %s\n", sameignorePath)
		fmt.Printf("\nRun %ssame ignore reset%s to create one with defaults.\n", cli.Bold, cli.Reset)
		return nil
	}

	patterns := ip.Patterns()
	if len(patterns) == 0 {
		fmt.Println("No active ignore patterns (.sameignore is empty or only comments).")
		return nil
	}

	fmt.Printf("%s.sameignore%s — %d active patterns:\n\n", cli.Bold, cli.Reset, len(patterns))
	for _, p := range patterns {
		fmt.Printf("  %s\n", p)
	}
	fmt.Printf("\n%sFile: %s%s\n", cli.Dim, sameignorePath, cli.Reset)
	return nil
}

func runIgnoreAdd(pattern string) error {
	vaultPath := config.VaultPath()

	if err := indexer.AddPattern(vaultPath, pattern); err != nil {
		return fmt.Errorf("add pattern: %w", err)
	}

	fmt.Printf("Added %q to .sameignore\n", pattern)
	fmt.Printf("%sRun 'same reindex' to apply changes to existing index.%s\n", cli.Dim, cli.Reset)
	return nil
}

func runIgnoreReset() error {
	vaultPath := config.VaultPath()
	sameignorePath := filepath.Join(vaultPath, ".sameignore")

	// Check if file exists and warn
	if _, err := os.Stat(sameignorePath); err == nil {
		fmt.Printf("This will overwrite %s with defaults.\n", sameignorePath)
		fmt.Print("Continue? [y/N] ")
		var response string
		_, _ = fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" {
			fmt.Println("Canceled.")
			return nil
		}
	}

	if err := indexer.WriteSameignore(vaultPath, indexer.DefaultSameignore); err != nil {
		return fmt.Errorf("write .sameignore: %w", err)
	}

	ip := indexer.ParseSameignoreString(indexer.DefaultSameignore)
	fmt.Printf("Reset .sameignore to defaults (%d patterns)\n", ip.PatternCount())
	fmt.Printf("%sRun 'same reindex' to apply changes to existing index.%s\n", cli.Dim, cli.Reset)
	return nil
}
