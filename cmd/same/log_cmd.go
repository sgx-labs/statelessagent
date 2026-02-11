package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func logCmd() *cobra.Command {
	var (
		lastN   int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show recent SAME activity",
		Long:  "Shows recent context injections, decision extractions, and handoff generations.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLog(lastN, jsonOut)
		},
	}
	cmd.Flags().IntVar(&lastN, "last", 5, "Number of recent sessions to show")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runLog(lastN int, jsonOut bool) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	usage, err := db.GetRecentUsage(lastN)
	if err != nil {
		return fmt.Errorf("query usage: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(usage, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(usage) == 0 {
		fmt.Println("\nNo recent activity. SAME records activity when hooks fire during Claude Code sessions.")
		return nil
	}

	fmt.Printf("\nRecent Activity (last %d sessions):\n\n", lastN)

	for _, u := range usage {
		ts := u.Timestamp
		if len(ts) > 16 {
			ts = ts[:16] // trim to YYYY-MM-DD HH:MM
		}
		ts = strings.Replace(ts, "T", " ", 1)

		fmt.Printf("  %s  %-22s", ts, u.HookName)

		switch u.HookName {
		case "context_surfacing":
			fmt.Printf("  Injected %d notes (%d tokens)\n", len(u.InjectedPaths), u.EstimatedTokens)
			for _, p := range u.InjectedPaths {
				// Show just filename
				name := filepath.Base(p)
				name = strings.TrimSuffix(name, ".md")
				fmt.Printf("  %s%-40s→ %s%s\n", strings.Repeat(" ", 40), "", name, "")
			}
		case "decision_extractor":
			fmt.Printf("  Extracted decision(s)\n")
		case "handoff_generator":
			fmt.Printf("  Created handoff\n")
		case "staleness_check":
			if len(u.InjectedPaths) > 0 {
				fmt.Printf("  Surfaced %d stale notes\n", len(u.InjectedPaths))
			} else {
				fmt.Printf("  No stale notes\n")
			}
		default:
			fmt.Printf("  %d tokens\n", u.EstimatedTokens)
		}
	}
	fmt.Println()

	return nil
}
