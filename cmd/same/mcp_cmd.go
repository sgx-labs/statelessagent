package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	mcpserver "github.com/sgx-labs/statelessagent/internal/mcp"
	memory "github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the AI tool integration server (MCP)",
		RunE: func(cmd *cobra.Command, args []string) error {
			mcpserver.Version = Version
			return mcpserver.Serve()
		},
	}
}

func budgetCmd() *cobra.Command {
	var (
		sessionID string
		lastN     int
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Show context utilization budget report",
		Long:  "Analyze how much injected context Claude actually used. Tracks injection events and reference detection.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBudget(sessionID, lastN, jsonOut)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "Report for a specific session ID")
	cmd.Flags().IntVar(&lastN, "last", 10, "Report for last N sessions")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runBudget(sessionID string, lastN int, jsonOut bool) error {
	db, err := store.Open()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	report := memory.GetBudgetReport(db, sessionID, lastN)

	if jsonOut {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// Human-readable output
	switch r := report.(type) {
	case memory.BudgetReport:
		fmt.Print("\nContext Utilization Budget Report\n\n")
		fmt.Printf("  Sessions analyzed:     %d\n", r.SessionsAnalyzed)
		fmt.Printf("  Total injections:      %d\n", r.TotalInjections)
		fmt.Printf("  Total tokens injected: %d\n", r.TotalTokensInjected)
		fmt.Printf("  Referenced by Claude:   %d (%.0f%%)\n", r.ReferencedCount, r.UtilizationRate*100)
		fmt.Printf("  Wasted tokens:         ~%d\n", r.TotalTokensInjected-int(float64(r.TotalTokensInjected)*r.UtilizationRate))

		if len(r.PerHook) > 0 {
			fmt.Println("\n  Per-hook breakdown:")
			for name, hs := range r.PerHook {
				fmt.Printf("    %-25s  %d injections, %d referenced (%.0f%%), avg %d tokens\n",
					name, hs.Injections, hs.Referenced, hs.UtilizationRate*100, hs.AvgTokensPerInject)
			}
		}

		if len(r.Suggestions) > 0 {
			fmt.Println("\n  Suggestions:")
			for _, s := range r.Suggestions {
				fmt.Printf("    - %s\n", s)
			}
		}
		fmt.Println()
	default:
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
	}
	return nil
}
