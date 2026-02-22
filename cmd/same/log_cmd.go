package main

import (
	"encoding/json"
	"fmt"
	"time"

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
		Long:  "Shows recent hook activity (context surfacing, decision extraction, handoff generation, and related hooks).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLog(lastN, jsonOut)
		},
	}
	cmd.Flags().IntVar(&lastN, "last", 20, "Number of recent hook entries to show")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runLog(lastN int, jsonOut bool) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	entries, err := db.GetRecentHookActivity(lastN)
	if err != nil {
		return fmt.Errorf("query hook activity: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(entries, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(entries) == 0 {
		fmt.Println("\nNo recent activity. SAME records activity when hooks fire during Claude Code sessions.")
		return nil
	}

	fmt.Printf("\nRecent Activity (last %d entries):\n\n", min(lastN, len(entries)))

	for _, entry := range entries {
		ts := time.Unix(entry.TimestampUnix, 0).Local().Format("2006-01-02 15:04")
		fmt.Printf("  %s  %-20s %-8s", ts, entry.HookName, entry.Status)

		switch entry.Status {
		case "injected":
			noteWord := "notes"
			if entry.SurfacedNotes == 1 {
				noteWord = "note"
			}
			if entry.EstimatedTokens > 0 {
				fmt.Printf("  %d %s  ~%d tokens\n", entry.SurfacedNotes, noteWord, entry.EstimatedTokens)
			} else {
				fmt.Printf("  %d %s\n", entry.SurfacedNotes, noteWord)
			}
		case "error":
			if entry.ErrorMessage != "" {
				fmt.Printf("  (%s)\n", entry.ErrorMessage)
			} else {
				fmt.Printf("\n")
			}
		default:
			if entry.Detail != "" {
				fmt.Printf("  (%s)\n", entry.Detail)
			} else {
				fmt.Printf("\n")
			}
		}
	}
	fmt.Println()

	return nil
}
