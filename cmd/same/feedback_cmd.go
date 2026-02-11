package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func feedbackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feedback [path] [up|down]",
		Short: "Tell SAME which notes are helpful (or not)",
		Long: `Manually adjust how likely a note is to be surfaced.

  same feedback "projects/plan.md" up     Boost confidence
  same feedback "projects/plan.md" down   Penalize confidence
  same feedback "projects/*" down         Glob-style path matching

'up' makes the note more likely to appear in future sessions.
'down' makes it much less likely to appear (strong penalty).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFeedback(args[0], args[1])
		},
	}
	return cmd
}

func runFeedback(pathPattern, direction string) error {
	if strings.TrimSpace(pathPattern) == "" {
		return userError("Empty path", "Provide a note path: same feedback \"path/to/note.md\" up")
	}
	if direction != "up" && direction != "down" {
		return userError(
			fmt.Sprintf("Unknown direction: %s", direction),
			"Use 'up' or 'down'",
		)
	}

	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// Convert glob to SQL LIKE pattern
	likePattern := strings.ReplaceAll(pathPattern, "*", "%")

	// Get matching notes (chunk_id=0 for dedup)
	rows, err := db.Conn().Query(
		`SELECT path, title, confidence, access_count FROM vault_notes WHERE path LIKE ? AND chunk_id = 0 ORDER BY path`,
		likePattern,
	)
	if err != nil {
		return fmt.Errorf("query notes: %w", err)
	}
	defer rows.Close()

	type noteInfo struct {
		path        string
		title       string
		confidence  float64
		accessCount int
	}
	var notes []noteInfo
	for rows.Next() {
		var n noteInfo
		if err := rows.Scan(&n.path, &n.title, &n.confidence, &n.accessCount); err != nil {
			continue
		}
		notes = append(notes, n)
	}

	if len(notes) == 0 {
		return fmt.Errorf("no notes matching %q found in index", pathPattern)
	}

	for _, n := range notes {
		oldConf := n.confidence
		var newConf float64
		var boostMsg string

		if direction == "up" {
			newConf = oldConf + 0.2
			if newConf > 1.0 {
				newConf = 1.0
			}
			if err := db.AdjustConfidence(n.path, newConf); err != nil {
				fmt.Fprintf(os.Stderr, "  error adjusting %s: %v\n", n.path, err)
				continue
			}
			if err := db.SetAccessBoost(n.path, 5); err != nil {
				fmt.Fprintf(os.Stderr, "  error boosting %s: %v\n", n.path, err)
			}
			boostMsg = fmt.Sprintf("Boosted '%s' — confidence: %.2f → %.2f, access +5",
				n.title, oldConf, newConf)
		} else {
			newConf = oldConf - 0.3
			if newConf < 0.05 {
				newConf = 0.05
			}
			if err := db.AdjustConfidence(n.path, newConf); err != nil {
				fmt.Fprintf(os.Stderr, "  error adjusting %s: %v\n", n.path, err)
				continue
			}
			boostMsg = fmt.Sprintf("Penalized '%s' — confidence: %.2f → %.2f",
				n.title, oldConf, newConf)
		}

		fmt.Printf("  %s\n", boostMsg)
	}

	if len(notes) > 1 {
		fmt.Printf("\n  Adjusted %d notes.\n", len(notes))
	}

	return nil
}
