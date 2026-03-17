package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func briefCmd() *cobra.Command {
	var maxItems int
	var noLLM bool
	cmd := &cobra.Command{
		Use:   "brief",
		Short: "Get oriented — what matters right now [experimental]",
		Long: `Show a concise briefing of your current context.

Brief analyzes your vault to surface:
  • Recent session activity (what you were working on)
  • Open decisions that need attention
  • High-confidence knowledge relevant to active work
  • Trust state — which notes are validated, stale, or contradicted
  • Provenance — source files behind key decisions

Think of it as your AI's morning briefing — orientation, not recall.

Use --no-llm for a structured data-only view when no LLM is available.

Examples:
  same brief              Get oriented (with LLM summarization)
  same brief --no-llm     Structured view without LLM
  same brief --items 10   Show more items per section`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrief(maxItems, noLLM)
		},
	}
	cmd.Flags().IntVar(&maxItems, "items", 5, "Maximum items per section")
	cmd.Flags().BoolVar(&noLLM, "no-llm", false, "Show structured data without LLM summarization")
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
	TrustState  string
}

// briefSource holds provenance info for a note.
type briefSource struct {
	NotePath   string
	SourcePath string
	SourceType string
}

// briefContext holds all gathered data for the briefing.
type briefContext struct {
	RecentNotes   []briefNote
	SessionNotes  []briefNote
	DecisionNotes []briefNote
	HighConfNotes []briefNote
	TrustSummary  *store.TrustSummary
	StaleNotes    []briefNote
	Sources       map[string][]briefSource // note path -> sources
	NoteCount     int
}

func (bc *briefContext) totalGathered() int {
	return len(bc.RecentNotes) + len(bc.SessionNotes) + len(bc.DecisionNotes) + len(bc.HighConfNotes)
}

func runBrief(maxItems int, noLLM bool) error {
	fmt.Fprintf(os.Stderr, "%s  This feature is experimental. Feedback welcome: https://github.com/sgx-labs/statelessagent/issues%s\n\n", cli.Dim, cli.Reset)

	// 1. Open database
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// Check if vault is empty
	noteCount, _ := db.NoteCount()
	if noteCount == 0 {
		// Check if markdown files exist on disk but just aren't indexed.
		// This can happen after a failed force reindex or before first indexing.
		mdCount := indexer.CountMarkdownFiles(config.VaultPath())
		if mdCount > 0 {
			fmt.Printf("\n  Your vault has %d markdown files but they aren't indexed yet.\n", mdCount)
			fmt.Printf("  Run %ssame reindex%s to index them.\n\n", cli.Bold, cli.Reset)
		} else {
			fmt.Printf("\n  Your vault is empty. Add markdown files to your vault directory, or run %ssame seed install%s for starter content.\n\n",
				cli.Bold, cli.Reset)
		}
		return nil
	}

	// 2. Gather all orientation data
	ctx := gatherBriefContext(db, maxItems)
	ctx.NoteCount = noteCount

	if ctx.totalGathered() == 0 {
		fmt.Printf("\n  No notes found for briefing. Your vault has %d notes but none match briefing criteria.\n", noteCount)
		fmt.Printf("  Try adding session logs or decision records.\n\n")
		return nil
	}

	// 3. --no-llm mode: structured output without LLM
	if noLLM {
		return renderBriefNoLLM(ctx)
	}

	// 4. LLM mode: connect and generate
	fmt.Printf("\n  %s*%s Preparing briefing...\n", cli.Cyan, cli.Reset)

	chat, err := llm.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %sCould not connect to LLM. Falling back to structured view.%s\n\n", cli.Yellow, cli.Reset)
		return renderBriefNoLLM(ctx)
	}

	model, err := chat.PickBestModel()
	if err != nil || model == "" {
		fmt.Fprintf(os.Stderr, "  %sNo chat model available. Falling back to structured view.%s\n\n", cli.Yellow, cli.Reset)
		return renderBriefNoLLM(ctx)
	}

	fmt.Printf("  %s*%s Thinking with %s/%s (%d sources)...\n\n", cli.Cyan, cli.Reset, chat.Provider(), model, ctx.totalGathered())

	// 5. Build LLM prompt
	prompt := buildBriefPrompt(ctx)

	// 6. Generate briefing
	answer, err := chat.Generate(model, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %sCould not generate briefing. Falling back to structured view.%s\n", cli.Yellow, cli.Reset)
		fmt.Fprintf(os.Stderr, "  %sTip: check that your LLM is running. For Ollama: ollama serve%s\n\n", cli.Dim, cli.Reset)
		return renderBriefNoLLM(ctx)
	}

	// 7. Display formatted output
	renderBriefHeader()
	for _, line := range strings.Split(answer, "\n") {
		fmt.Printf("  %s\n", line)
	}

	// Trust state summary footer
	renderTrustFooter(ctx.TrustSummary)

	// Sources summary
	renderSourcesFooter(ctx)

	return nil
}

// gatherBriefContext collects all data needed for the briefing in a single pass.
func gatherBriefContext(db *store.DB, maxItems int) *briefContext {
	conn := db.Conn()
	ctx := &briefContext{
		Sources: make(map[string][]briefSource),
	}

	// Recent notes — include trust_state
	ctx.RecentNotes = queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence, COALESCE(trust_state, 'unknown')
		 FROM vault_notes
		 WHERE chunk_id = 0 AND path NOT LIKE '_PRIVATE/%' AND COALESCE(suppressed, 0) = 0
		 ORDER BY modified DESC
		 LIMIT 20`)

	// Session notes
	ctx.SessionNotes = queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence, COALESCE(trust_state, 'unknown')
		 FROM vault_notes
		 WHERE chunk_id = 0 AND (content_type = 'session' OR path LIKE 'sessions/%' OR path LIKE '%session%')
			AND COALESCE(suppressed, 0) = 0
		 ORDER BY modified DESC
		 LIMIT ?`, maxItems)

	// Decision notes
	ctx.DecisionNotes = queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence, COALESCE(trust_state, 'unknown')
		 FROM vault_notes
		 WHERE chunk_id = 0 AND (content_type = 'decision' OR path LIKE '%decision%')
			AND COALESCE(suppressed, 0) = 0
		 ORDER BY modified DESC
		 LIMIT ?`, maxItems)

	// High-confidence notes
	ctx.HighConfNotes = queryBriefNotesHighConf(conn,
		`SELECT path, title, text, confidence, access_count, COALESCE(trust_state, 'unknown')
		 FROM vault_notes
		 WHERE chunk_id = 0 AND confidence > 0.7 AND path NOT LIKE '_PRIVATE/%'
			AND COALESCE(suppressed, 0) = 0
		 ORDER BY confidence DESC, access_count DESC
		 LIMIT 10`)

	// Stale notes — notes explicitly marked stale
	ctx.StaleNotes = queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence, COALESCE(trust_state, 'unknown')
		 FROM vault_notes
		 WHERE chunk_id = 0 AND trust_state = 'stale' AND COALESCE(suppressed, 0) = 0
		 ORDER BY modified DESC
		 LIMIT 10`)

	// Trust state summary
	ctx.TrustSummary, _ = db.GetTrustStateSummary()

	// Provenance: gather sources for decision and high-confidence notes
	notePaths := make(map[string]bool)
	for _, n := range ctx.DecisionNotes {
		notePaths[n.Path] = true
	}
	for _, n := range ctx.HighConfNotes {
		notePaths[n.Path] = true
	}
	for _, n := range ctx.StaleNotes {
		notePaths[n.Path] = true
	}
	for path := range notePaths {
		sources, err := db.GetSourcesForNote(path)
		if err == nil && len(sources) > 0 {
			var bs []briefSource
			for _, s := range sources {
				bs = append(bs, briefSource{
					NotePath:   s.NotePath,
					SourcePath: s.SourcePath,
					SourceType: s.SourceType,
				})
			}
			ctx.Sources[path] = bs
		}
	}

	return ctx
}

// buildBriefPrompt constructs a rich prompt with trust state and provenance.
func buildBriefPrompt(ctx *briefContext) string {
	var b strings.Builder

	b.WriteString(`You are a briefing engine for a personal knowledge vault called SAME (Stateless Agent Memory Engine).

Your job: produce a concise, structured orientation briefing so the user can immediately resume productive work.

OUTPUT FORMAT (use these exact section headers):
Current Focus
  [1-2 bullets: what they were working on based on recent sessions/handoffs]

Key Decisions
  [bullet list of active decisions with trust status]

Stale Context
  [any notes whose source files changed — flag these clearly]

Recent Activity
  [last 3-5 sessions summarized in 1 line each]

Suggestions
  [1-2 actionable next steps based on vault state]

RULES:
- Be extremely concise — this is a briefing, not a report
- Lead with what's most actionable RIGHT NOW
- If a note has trust_state "stale", flag it with a warning
- If a note has trust_state "validated", mark it with a checkmark
- If a note has trust_state "contradicted", flag it as needing review
- Include source file names when provenance is available
- Flag any contradictions or conflicts between notes
- Do NOT add information beyond what's in the notes
- Do NOT use markdown headers (#). Use plain text section names
- Keep total output under 25 lines

`)

	// Trust state summary
	if ctx.TrustSummary != nil {
		total := ctx.TrustSummary.Validated + ctx.TrustSummary.Stale + ctx.TrustSummary.Contradicted + ctx.TrustSummary.Unknown
		if total > 0 {
			fmt.Fprintf(&b, "VAULT TRUST STATE: %d validated, %d stale, %d contradicted, %d unknown\n\n",
				ctx.TrustSummary.Validated, ctx.TrustSummary.Stale,
				ctx.TrustSummary.Contradicted, ctx.TrustSummary.Unknown)
		}
	}

	writeBriefSection(&b, "RECENT ACTIVITY", ctx.RecentNotes, ctx.Sources, true)
	writeBriefSection(&b, "SESSIONS", ctx.SessionNotes, ctx.Sources, true)
	writeBriefSection(&b, "DECISIONS", ctx.DecisionNotes, ctx.Sources, true)
	writeBriefSection(&b, "HIGH-CONFIDENCE KNOWLEDGE", ctx.HighConfNotes, ctx.Sources, true)

	if len(ctx.StaleNotes) > 0 {
		b.WriteString("\nSTALE CONTEXT (source files changed since capture):\n")
		for _, n := range ctx.StaleNotes {
			snippet := truncateSnippet(n.Text, 200)
			fmt.Fprintf(&b, "- [%s] %s (trust: STALE): %s\n", n.Path, n.Title, snippet)
			if sources, ok := ctx.Sources[n.Path]; ok {
				var names []string
				for _, s := range sources {
					names = append(names, s.SourcePath)
				}
				fmt.Fprintf(&b, "  sources: %s\n", strings.Join(names, ", "))
			}
		}
	}

	b.WriteString("\nProduce the briefing now.")

	return b.String()
}

// writeBriefSection writes a section of notes to the prompt builder.
func writeBriefSection(b *strings.Builder, header string, notes []briefNote, sources map[string][]briefSource, includeTrust bool) {
	fmt.Fprintf(b, "\n%s:\n", header)
	if len(notes) == 0 {
		b.WriteString("(none)\n")
		return
	}
	for _, n := range notes {
		snippet := truncateSnippet(n.Text, 250)
		if includeTrust && n.TrustState != "" && n.TrustState != "unknown" {
			fmt.Fprintf(b, "- [%s] %s (trust: %s): %s\n", n.Path, n.Title, n.TrustState, snippet)
		} else {
			fmt.Fprintf(b, "- [%s] %s: %s\n", n.Path, n.Title, snippet)
		}
		if srcs, ok := sources[n.Path]; ok {
			var names []string
			for _, s := range srcs {
				names = append(names, s.SourcePath)
			}
			fmt.Fprintf(b, "  sources: %s\n", strings.Join(names, ", "))
		}
	}
}

// truncateSnippet truncates text to maxLen characters and collapses newlines.
func truncateSnippet(text string, maxLen int) string {
	snippet := strings.ReplaceAll(text, "\n", " ")
	if len(snippet) > maxLen {
		snippet = snippet[:maxLen]
	}
	return snippet
}

// renderBriefHeader prints the briefing header bar.
func renderBriefHeader() {
	cli.Section("Briefing")
}

// renderTrustFooter shows the trust state summary.
func renderTrustFooter(ts *store.TrustSummary) {
	if ts == nil {
		return
	}
	total := ts.Validated + ts.Stale + ts.Contradicted + ts.Unknown
	if total == 0 {
		return
	}

	fmt.Println()
	fmt.Printf("  %sVault trust:%s", cli.Dim, cli.Reset)
	if ts.Validated > 0 {
		fmt.Printf(" %s%d validated%s", cli.Green, ts.Validated, cli.Reset)
	}
	if ts.Stale > 0 {
		fmt.Printf(" %s%d stale%s", cli.Yellow, ts.Stale, cli.Reset)
	}
	if ts.Contradicted > 0 {
		fmt.Printf(" %s%d contradicted%s", cli.Red, ts.Contradicted, cli.Reset)
	}
	if ts.Unknown > 0 {
		fmt.Printf(" %s%d unknown%s", cli.Dim, ts.Unknown, cli.Reset)
	}
	fmt.Println()
}

// renderSourcesFooter prints the sources summary at the bottom.
func renderSourcesFooter(ctx *briefContext) {
	cli.Section("Sources")
	fmt.Printf("  %sBased on %d notes (%d sessions, %d decisions, %d knowledge)%s\n",
		cli.Dim, ctx.totalGathered(), len(ctx.SessionNotes), len(ctx.DecisionNotes), len(ctx.HighConfNotes), cli.Reset)

	if len(ctx.StaleNotes) > 0 {
		fmt.Printf("  %s%d notes have stale context — source files changed since capture%s\n",
			cli.Yellow, len(ctx.StaleNotes), cli.Reset)
	}

	if len(ctx.RecentNotes) > 0 {
		mostRecent := ctx.RecentNotes[0].Modified
		ago := time.Since(time.Unix(int64(mostRecent), 0))
		fmt.Printf("  %sMost recent activity: %s ago%s\n", cli.Dim, formatDuration(ago), cli.Reset)
	}
	fmt.Println()
}

// renderBriefNoLLM renders a structured briefing without LLM summarization.
func renderBriefNoLLM(ctx *briefContext) error {
	renderBriefHeader()

	// Current Focus — from most recent session notes
	fmt.Printf("  %sCurrent Focus%s\n", cli.Bold, cli.Reset)
	if len(ctx.SessionNotes) > 0 {
		limit := 3
		if len(ctx.SessionNotes) < limit {
			limit = len(ctx.SessionNotes)
		}
		for _, n := range ctx.SessionNotes[:limit] {
			ago := time.Since(time.Unix(int64(n.Modified), 0))
			trustTag := briefTrustTag(n.TrustState)
			fmt.Printf("    %s %s%s%s  %s%s ago%s%s\n",
				bulletChar, cli.Cyan, n.Title, cli.Reset,
				cli.Dim, formatDuration(ago), cli.Reset, trustTag)
		}
	} else {
		fmt.Printf("    %sNo recent sessions%s\n", cli.Dim, cli.Reset)
	}

	// Key Decisions
	fmt.Println()
	decisionLabel := "Key Decisions"
	if len(ctx.DecisionNotes) > 0 {
		decisionLabel = fmt.Sprintf("Key Decisions (%d active)", len(ctx.DecisionNotes))
	}
	fmt.Printf("  %s%s%s\n", cli.Bold, decisionLabel, cli.Reset)
	if len(ctx.DecisionNotes) > 0 {
		for _, n := range ctx.DecisionNotes {
			trustTag := briefTrustTag(n.TrustState)
			provenanceTag := ""
			if srcs, ok := ctx.Sources[n.Path]; ok && len(srcs) > 0 {
				var names []string
				for _, s := range srcs {
					names = append(names, s.SourcePath)
				}
				provenanceTag = fmt.Sprintf("  %s(based on %s)%s", cli.Dim, strings.Join(names, ", "), cli.Reset)
			}
			fmt.Printf("    %s %s%s%s%s%s\n",
				bulletChar, cli.Cyan, n.Title, cli.Reset, trustTag, provenanceTag)
		}
	} else {
		fmt.Printf("    %sNo decisions recorded%s\n", cli.Dim, cli.Reset)
	}

	// Stale Context
	if len(ctx.StaleNotes) > 0 {
		fmt.Println()
		fmt.Printf("  %sStale Context (%d notes)%s\n", cli.Bold, len(ctx.StaleNotes), cli.Reset)
		for _, n := range ctx.StaleNotes {
			provenanceTag := ""
			if srcs, ok := ctx.Sources[n.Path]; ok && len(srcs) > 0 {
				var names []string
				for _, s := range srcs {
					names = append(names, s.SourcePath)
				}
				provenanceTag = fmt.Sprintf(" — source %s changed", strings.Join(names, ", "))
			}
			ago := time.Since(time.Unix(int64(n.Modified), 0))
			fmt.Printf("    %s %s%s%s  %s%s ago%s%s\n",
				staleChar, cli.Yellow, n.Title, cli.Reset,
				cli.Dim, formatDuration(ago), cli.Reset,
				fmt.Sprintf("%s%s%s", cli.Yellow, provenanceTag, cli.Reset))
		}
	}

	// Recent Activity
	fmt.Println()
	fmt.Printf("  %sRecent Activity%s\n", cli.Bold, cli.Reset)
	if len(ctx.RecentNotes) > 0 {
		limit := 5
		if len(ctx.RecentNotes) < limit {
			limit = len(ctx.RecentNotes)
		}
		for _, n := range ctx.RecentNotes[:limit] {
			ago := time.Since(time.Unix(int64(n.Modified), 0))
			typeTag := ""
			if n.ContentType != "" && n.ContentType != "note" {
				typeTag = fmt.Sprintf(" %s[%s]%s", cli.Dim, n.ContentType, cli.Reset)
			}
			fmt.Printf("    %s %s%s%s  %s%s ago%s%s\n",
				bulletChar, cli.Cyan, n.Title, cli.Reset,
				cli.Dim, formatDuration(ago), cli.Reset, typeTag)
		}
	} else {
		fmt.Printf("    %sNo recent activity%s\n", cli.Dim, cli.Reset)
	}

	// High-Confidence Knowledge
	if len(ctx.HighConfNotes) > 0 {
		fmt.Println()
		fmt.Printf("  %sHigh-Confidence Knowledge%s\n", cli.Bold, cli.Reset)
		limit := 5
		if len(ctx.HighConfNotes) < limit {
			limit = len(ctx.HighConfNotes)
		}
		for _, n := range ctx.HighConfNotes[:limit] {
			trustTag := briefTrustTag(n.TrustState)
			fmt.Printf("    %s %s%s%s  %s%.0f%% confidence%s%s\n",
				bulletChar, cli.Cyan, n.Title, cli.Reset,
				cli.Dim, n.Confidence*100, cli.Reset, trustTag)
		}
	}

	// Trust state summary + sources
	renderTrustFooter(ctx.TrustSummary)
	renderSourcesFooter(ctx)

	return nil
}

// briefTrustTag returns a colored trust annotation for display.
func briefTrustTag(state string) string {
	switch state {
	case "validated":
		return fmt.Sprintf(" %s(validated)%s", cli.Green, cli.Reset)
	case "stale":
		return fmt.Sprintf(" %s(stale)%s", cli.Yellow, cli.Reset)
	case "contradicted":
		return fmt.Sprintf(" %s(contradicted)%s", cli.Red, cli.Reset)
	default:
		return ""
	}
}

const bulletChar = "\xe2\x80\xa2" // bullet: U+2022
const staleChar = "\xe2\x9a\xa0"  // warning: U+26A0

// queryBriefNotes runs a SQL query and returns briefNote records.
// The query must SELECT: path, title, text, modified, content_type, confidence, trust_state (in that order).
func queryBriefNotes(conn *sql.DB, query string, args ...interface{}) []briefNote {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notes []briefNote
	for rows.Next() {
		var n briefNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Text, &n.Modified, &n.ContentType, &n.Confidence, &n.TrustState); err != nil {
			continue
		}
		notes = append(notes, n)
	}
	return notes
}

// queryBriefNotesHighConf runs a SQL query for high-confidence notes including trust_state.
// The query must SELECT: path, title, text, confidence, access_count, trust_state (in that order).
func queryBriefNotesHighConf(conn *sql.DB, query string, args ...interface{}) []briefNote {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notes []briefNote
	for rows.Next() {
		var n briefNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Text, &n.Confidence, &n.AccessCount, &n.TrustState); err != nil {
			continue
		}
		notes = append(notes, n)
	}
	return notes
}
