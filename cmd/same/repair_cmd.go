package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
)

func repairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repair",
		Short: "Back up and rebuild the database",
		Long: `Creates a backup of vault.db and force-rebuilds the index.

This is the go-to command when something seems broken. It:
  1. Copies vault.db to vault.db.bak
  2. Runs a full force reindex

After repair, verify with 'same doctor'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRepair()
		},
	}
}

func runRepair() error {
	cli.Header("SAME Repair")
	fmt.Println()

	dbPath := config.DBPath()

	// Step 1: Back up
	bakPath := dbPath + ".bak"
	fmt.Printf("  Backing up database...")
	if _, err := os.Stat(dbPath); err == nil {
		src, err := os.ReadFile(dbPath)
		if err != nil {
			fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
			return fmt.Errorf("read database: %w", err)
		}
		if err := os.WriteFile(bakPath, src, 0o600); err != nil {
			fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
			return fmt.Errorf("write backup: %w", err)
		}
		fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)
		fmt.Printf("  Backup saved to %s\n", cli.ShortenHome(bakPath))
	} else {
		fmt.Printf(" %sskipped%s (no existing database)\n", cli.Yellow, cli.Reset)
	}

	// Step 2: Force reindex
	fmt.Printf("\n  Rebuilding index...\n")
	if err := runReindex(true, false); err != nil {
		return fmt.Errorf("reindex failed: %w", err)
	}

	fmt.Println()
	fmt.Printf("  %s✓%s Repair complete.\n", cli.Green, cli.Reset)
	fmt.Printf("  Run %ssame doctor%s to verify.\n", cli.Bold, cli.Reset)
	fmt.Printf("\n  Backup saved to %s — delete after verifying repair.\n", cli.ShortenHome(bakPath))
	cli.Footer()
	return nil
}
