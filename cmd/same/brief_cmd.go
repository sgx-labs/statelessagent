package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func briefCmd() *cobra.Command {
	var maxItems int
	cmd := &cobra.Command{
		Use:   "brief",
		Short: "Get oriented — what matters right now",
		Long: `Show a concise briefing of your current context.

Brief analyzes your vault to surface:
  • Recent session activity (what you were working on)
  • Open decisions that need attention
  • High-confidence knowledge relevant to active work
  • Conflicts or contradictions detected

Think of it as your AI's morning briefing — orientation, not recall.

Requires an LLM provider (Ollama or OpenAI). Run 'same init' to configure one.

Examples:
  same brief              Get oriented
  same brief --items 10   Show more items`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrief(maxItems)
		},
	}
	cmd.Flags().IntVar(&maxItems, "items", 5, "Maximum items per section")
	return cmd
}

// briefNote holds a note record gathered for briefing context.
type briefNote struct {
	Path        string
	Title       string
	Text        string
	Modified    float64
	ContentType string
	Confidence  float64
	AccessCount int
}

func runBrief(maxItems int) error {
	// 1. Open database
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// Check if vault is empty
	noteCount, _ := db.NoteCount()
	if noteCount == 0 {
		fmt.Printf("\n  Your vault is empty. Run %ssame store%s or %ssame demo%s to add some notes.\n\n",
			cli.Bold, cli.Reset, cli.Bold, cli.Reset)
		return nil
	}

	// 2. Gather orientation data from the vault
	conn := db.Conn()

	recentNotes := queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence
		 FROM vault_notes
		 WHERE chunk_id = 0 AND path NOT LIKE '_PRIVATE/%'
		 ORDER BY modified DESC
		 LIMIT 20`)

	sessionNotes := queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence
		 FROM vault_notes
		 WHERE chunk_id = 0 AND (content_type = 'session' OR path LIKE 'sessions/%' OR path LIKE '%session%')
		 ORDER BY modified DESC
		 LIMIT ?`, maxItems)

	decisionNotes := queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence
		 FROM vault_notes
		 WHERE chunk_id = 0 AND (content_type = 'decision' OR path LIKE '%decision%')
		 ORDER BY modified DESC
		 LIMIT ?`, maxItems)

	highConfNotes := queryBriefNotesWithAccess(conn,
		`SELECT path, title, text, confidence, access_count
		 FROM vault_notes
		 WHERE chunk_id = 0 AND confidence > 0.7 AND path NOT LIKE '_PRIVATE/%'
		 ORDER BY confidence DESC, access_count DESC
		 LIMIT 10`)

	totalGathered := len(recentNotes) + len(sessionNotes) + len(decisionNotes) + len(highConfNotes)
	if totalGathered == 0 {
		fmt.Printf("\n  No notes found for briefing. Your vault has %d notes but none match briefing criteria.\n", noteCount)
		fmt.Printf("  Try adding session logs or decision records.\n\n")
		return nil
	}

	// 3. Connect to LLM
	fmt.Printf("\n  %s*%s Preparing briefing...\n", cli.Cyan, cli.Reset)

	chat, err := llm.NewClient()
	if err != nil {
		return userError(
			"Brief requires an LLM",
			"Run 'same init' to configure one, or set SAME_CHAT_PROVIDER (ollama/openai/openai-compatible).",
		)
	}

	// 4. Pick model
	model, err := chat.PickBestModel()
	if err != nil {
		if chat.Provider() == "ollama" {
			return userError(
				"No chat model available",
				"Start Ollama or set SAME_CHAT_PROVIDER=openai/openai-compatible, then retry 'same brief'.",
			)
		}
		return userError(
			fmt.Sprintf("Can't list models from %s provider", chat.Provider()),
			"Check that your provider has at least one chat model installed. For Ollama: ollama pull llama3.2",
		)
	}
	if model == "" {
		return userError(
			"No chat model found",
			"Set SAME_CHAT_MODEL explicitly or install/configure at least one chat-capable model.",
		)
	}

	fmt.Printf("  %s*%s Thinking with %s/%s (%d sources)...\n", cli.Cyan, cli.Reset, chat.Provider(), model, totalGathered)

	// 5. Build LLM prompt
	prompt := buildBriefPrompt(recentNotes, sessionNotes, decisionNotes, highConfNotes)

	// 6. Generate briefing
	answer, err := chat.Generate(model, prompt)
	if err != nil {
		return fmt.Errorf("generate briefing: %w", err)
	}

	// 7. Display formatted output
	fmt.Printf("\n  %s-- Briefing ------------------------------------------%s\n\n", cli.Cyan, cli.Reset)
	for _, line := range strings.Split(answer, "\n") {
		fmt.Printf("  %s\n", line)
	}

	// Sources summary
	fmt.Printf("\n  %s-- Sources -------------------------------------------%s\n\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sBased on %d notes (%d sessions, %d decisions, %d knowledge)%s\n",
		cli.Dim, totalGathered, len(sessionNotes), len(decisionNotes), len(highConfNotes), cli.Reset)

	if len(recentNotes) > 0 {
		mostRecent := recentNotes[0].Modified
		ago := time.Since(time.Unix(int64(mostRecent), 0))
		fmt.Printf("  %sMost recent activity: %s ago%s\n", cli.Dim, formatDuration(ago), cli.Reset)
	}
	fmt.Println()

	return nil
}

func buildBriefPrompt(recent, sessions, decisions, highConf []briefNote) string {
	var b strings.Builder

	b.WriteString(`You are a briefing engine for a personal knowledge vault. Given the following vault contents, produce a concise orientation briefing.

RULES:
- Be extremely concise — this is a briefing, not a report
- Lead with what's most actionable RIGHT NOW
- Flag any open decisions that need attention
- Note any contradictions or conflicts between notes
- Use bullet points, not paragraphs
- Maximum 15 lines total
- Do NOT add information beyond what's in the notes

`)

	b.WriteString("RECENT ACTIVITY:\n")
	if len(recent) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range recent {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s: %s\n", n.Path, n.Title, snippet)
		}
	}

	b.WriteString("\nSESSIONS:\n")
	if len(sessions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range sessions {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s: %s\n", n.Path, n.Title, snippet)
		}
	}

	b.WriteString("\nOPEN DECISIONS:\n")
	if len(decisions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range decisions {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s: %s\n", n.Path, n.Title, snippet)
		}
	}

	b.WriteString("\nHIGH-CONFIDENCE KNOWLEDGE:\n")
	if len(highConf) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range highConf {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s (confidence: %.0f%%): %s\n", n.Path, n.Title, n.Confidence*100, snippet)
		}
	}

	b.WriteString("\nProduce the briefing now.")

	return b.String()
}

// queryBriefNotes runs a SQL query and returns briefNote records.
// The query must SELECT: path, title, text, modified, content_type, confidence (in that order).
func queryBriefNotes(conn *sql.DB, query string, args ...interface{}) []briefNote {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notes []briefNote
	for rows.Next() {
		var n briefNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Text, &n.Modified, &n.ContentType, &n.Confidence); err != nil {
			continue
		}
		notes = append(notes, n)
	}
	return notes
}

// queryBriefNotesWithAccess runs a SQL query for high-confidence notes.
// The query must SELECT: path, title, text, confidence, access_count (in that order).
func queryBriefNotesWithAccess(conn *sql.DB, query string, args ...interface{}) []briefNote {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notes []briefNote
	for rows.Next() {
		var n briefNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Text, &n.Confidence, &n.AccessCount); err != nil {
			continue
		}
		notes = append(notes, n)
	}
	return notes
}
