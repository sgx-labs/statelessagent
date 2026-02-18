package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/graph"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// tutorialLessons defines the available lessons.
var tutorialLessons = []struct {
	name  string
	title string
	desc  string
}{
	{"search", "Semantic Search", "SAME finds notes by meaning, not just keywords"},
	{"decisions", "Decisions Stick", "Your architectural choices are extracted and remembered"},
	{"pin", "Pin What Matters", "Critical context appears in every session"},
	{"privacy", "Privacy Tiers", "Three-tier privacy is structural, not policy"},
	{"ask", "Ask Your Notes", "Get answers from your notes with source citations"},
	{"handoff", "Session Handoff", "Your AI picks up where you left off"},
	{"graph", "Knowledge Graph", "Trace relationships between notes, decisions, and agents"},
}

func tutorialCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tutorial [lesson]",
		Short: "Learn SAME by doing — interactive lessons",
		Long: `A hands-on tutorial that teaches SAME's features by creating real notes
and running real commands. Each lesson is self-contained.

Run all lessons:
  same tutorial

Run a specific lesson:
  same tutorial search
  same tutorial pin
  same tutorial ask
  same tutorial graph

Available lessons: search, decisions, pin, privacy, ask, handoff, graph`,
		ValidArgs: []string{"search", "decisions", "pin", "privacy", "ask", "handoff", "graph"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return runTutorialLesson(args[0])
			}
			return runTutorial()
		},
	}
	return cmd
}

// tutorialState holds the shared state for a tutorial session.
type tutorialState struct {
	dir       string
	db        *store.DB
	dbPath    string
	hasVec    bool   // true if semantic embeddings are available
	origVault string // saved VaultOverride to restore on close
}

func newTutorialState() (*tutorialState, error) {
	dir, err := os.MkdirTemp("", "same-tutorial-")
	if err != nil {
		return nil, fmt.Errorf("create tutorial directory: %w", err)
	}

	sameDir := filepath.Join(dir, ".same", "data")
	if err := os.MkdirAll(sameDir, 0o755); err != nil {
		if cleanErr := os.RemoveAll(dir); cleanErr != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to clean up tutorial directory %q: %v\n", dir, cleanErr)
		}
		return nil, err
	}

	// Save and override vault path
	origVault := config.VaultOverride
	config.VaultOverride = dir

	dbPath := filepath.Join(sameDir, "vault.db")
	db, err := store.OpenPath(dbPath)
	if err != nil {
		config.VaultOverride = origVault
		if cleanErr := os.RemoveAll(dir); cleanErr != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to clean up tutorial directory %q: %v\n", dir, cleanErr)
		}
		return nil, fmt.Errorf("open database: %w", err)
	}

	return &tutorialState{dir: dir, db: db, dbPath: dbPath, origVault: origVault}, nil
}

func (ts *tutorialState) close() {
	ts.db.Close()
	config.VaultOverride = ts.origVault
	if err := os.RemoveAll(ts.dir); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to clean up tutorial directory %q: %v\n", ts.dir, err)
	}
}

// writeNote writes a markdown file to the tutorial vault and indexes it.
func (ts *tutorialState) writeNote(relPath, content string) error {
	fullPath := filepath.Join(ts.dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

// indexAll indexes the tutorial vault.
func (ts *tutorialState) indexAll() (*indexer.Stats, error) {
	stats, err := indexer.Reindex(ts.db, true)
	if err != nil {
		// Embedding provider failed — fall back to lite mode
		fmt.Fprintf(os.Stderr, "  %s[fallback: %v]%s\n", cli.Dim, err, cli.Reset)
		stats, err = indexer.ReindexLite(ts.db, true, nil)
		if err != nil {
			return nil, err
		}
		ts.hasVec = false
	} else {
		ts.hasVec = true
	}
	return stats, nil
}

// search searches the tutorial vault, using vectors or FTS5.
func (ts *tutorialState) search(query string, topK int) ([]store.SearchResult, error) {
	if ts.hasVec {
		embedClient, err := newEmbedProvider()
		if err == nil {
			vec, err := embedClient.GetQueryEmbedding(query)
			if err == nil {
				return ts.db.VectorSearch(vec, store.SearchOptions{TopK: topK})
			}
		}
	}
	if ts.db.FTSAvailable() {
		return ts.db.FTS5Search(query, store.SearchOptions{TopK: topK})
	}
	return nil, fmt.Errorf("no search method available")
}

// errQuit is returned when the user presses 'q' during the tutorial.
var errQuit = fmt.Errorf("quit")

func waitForEnter() error {
	fmt.Printf("\n  %s[Press Enter to continue, q to quit]%s ", cli.Dim, cli.Reset)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return errQuit // stdin closed or piped — exit gracefully
	}
	if strings.TrimSpace(strings.ToLower(line)) == "q" {
		fmt.Printf("\n  %sTutorial stopped. Run 'same tutorial' anytime to continue.%s\n\n", cli.Dim, cli.Reset)
		return errQuit
	}
	return nil
}

func runTutorial() error {
	fmt.Printf("\n  %s✦ SAME Tutorial%s — learn by doing\n\n", cli.Bold+cli.Cyan, cli.Reset)
	fmt.Printf("  7 lessons, ~7 minutes. You can also run individual lessons:\n")
	for _, l := range tutorialLessons {
		fmt.Printf("    %ssame tutorial %s%s — %s\n", cli.Dim, l.name, cli.Reset, l.desc)
	}
	if err := waitForEnter(); err != nil {
		return nil // user quit
	}

	for i, l := range tutorialLessons {
		fmt.Printf("\n")
		cli.Section(fmt.Sprintf("Lesson %d/%d: %s", i+1, len(tutorialLessons), l.title))
		fmt.Printf("  %s%s%s\n", cli.Dim, l.desc, cli.Reset)

		if err := runLessonByName(l.name); err != nil {
			fmt.Printf("  %s!%s Lesson error: %v\n", cli.Yellow, cli.Reset, err)
		}

		if i < len(tutorialLessons)-1 {
			if err := waitForEnter(); err != nil {
				return nil // user quit
			}
		}
	}

	fmt.Printf("\n  %s✦ Tutorial complete!%s\n\n", cli.Bold+cli.Green, cli.Reset)
	fmt.Printf("  You now know how to:\n")
	fmt.Printf("    %s✓%s Search notes by meaning\n", cli.Green, cli.Reset)
	fmt.Printf("    %s✓%s Track decisions automatically\n", cli.Green, cli.Reset)
	fmt.Printf("    %s✓%s Pin critical context\n", cli.Green, cli.Reset)
	fmt.Printf("    %s✓%s Keep private notes private\n", cli.Green, cli.Reset)
	fmt.Printf("    %s✓%s Ask questions and get cited answers\n", cli.Green, cli.Reset)
	fmt.Printf("    %s✓%s Hand off context between sessions\n", cli.Green, cli.Reset)
	fmt.Printf("    %s✓%s Traverse relationship paths in the knowledge graph\n", cli.Green, cli.Reset)
	fmt.Printf("\n  Ready to use SAME for real:\n")
	fmt.Printf("    %s$%s cd ~/your-project && same init\n\n", cli.Dim, cli.Reset)
	return nil
}

func runTutorialLesson(name string) error {
	for _, l := range tutorialLessons {
		if l.name == name {
			fmt.Printf("\n")
			cli.Section(l.title)
			fmt.Printf("  %s%s%s\n", cli.Dim, l.desc, cli.Reset)
			return runLessonByName(name)
		}
	}
	return fmt.Errorf("unknown lesson: %s (available: search, decisions, pin, privacy, ask, handoff, graph)", name)
}

func runLessonByName(name string) error {
	ts, err := newTutorialState()
	if err != nil {
		return err
	}
	defer ts.close()

	switch name {
	case "search":
		return lessonSearch(ts)
	case "decisions":
		return lessonDecisions(ts)
	case "pin":
		return lessonPin(ts)
	case "privacy":
		return lessonPrivacy(ts)
	case "ask":
		return lessonAsk(ts)
	case "handoff":
		return lessonHandoff(ts)
	case "graph":
		return lessonGraph(ts)
	}
	return nil
}

func lessonSearch(ts *tutorialState) error {
	fmt.Printf("\n  SAME finds notes by %smeaning%s, not just exact keywords.\n", cli.Bold, cli.Reset)
	fmt.Printf("  Let's prove it.\n\n")

	// Write a note about JWT auth
	note := `---
title: "Authentication Design"
tags: [auth, security]
---
# Authentication Design
We use JWT tokens with refresh rotation for API authentication.
Access tokens expire after 15 minutes. Refresh tokens last 7 days.
Tokens are stored in httpOnly cookies to prevent XSS attacks.
`
	if err := ts.writeNote("auth-design.md", note); err != nil {
		return err
	}
	fmt.Printf("  Created: %sauth-design.md%s\n", cli.Cyan, cli.Reset)
	fmt.Printf("  %sContent: JWT tokens, refresh rotation, httpOnly cookies%s\n\n", cli.Dim, cli.Reset)

	fmt.Printf("  Indexing...")
	if _, err := ts.indexAll(); err != nil {
		return err
	}
	fmt.Printf(" done.\n\n")

	// Search for "login system" — the word "login" isn't in the note
	query := "login system"
	fmt.Printf("  Now searching for %s\"%s\"%s\n", cli.Cyan, query, cli.Reset)
	fmt.Printf("  %s(Note: the word \"login\" doesn't appear in the note!)%s\n\n", cli.Dim, cli.Reset)

	results, err := ts.search(query, 3)
	if err != nil {
		return err
	}

	if len(results) > 0 {
		fmt.Printf("  %s✓%s Found: %s%s%s\n", cli.Green, cli.Reset, cli.Bold, results[0].Title, cli.Reset)
		fmt.Printf("    SAME understood that \"login\" relates to \"JWT authentication\"\n")
		fmt.Printf("    even though the exact word wasn't there.\n")
	} else {
		fmt.Printf("  %sNo results — this can happen in keyword-only mode.%s\n", cli.Dim, cli.Reset)
		fmt.Printf("  %sConfigure embeddings (ollama/openai/openai-compatible) for semantic search that understands meaning.%s\n", cli.Dim, cli.Reset)
	}
	return nil
}

func lessonDecisions(ts *tutorialState) error {
	fmt.Printf("\n  When your AI makes architectural decisions, SAME remembers them.\n")
	fmt.Printf("  No more \"we already decided to use PostgreSQL.\"\n\n")

	note := `---
title: "Decisions Log"
tags: [decisions, architecture]
content_type: decision
---
# Decisions Log

## Decision: PostgreSQL over MongoDB
**Date:** 2026-01-15
**Status:** Accepted
Relational model fits our data. Strong consistency needed for transactions.

## Decision: Monorepo structure
**Date:** 2026-01-10
**Status:** Accepted
Single repo for API + frontend. Simplifies CI/CD and type sharing.
`
	if err := ts.writeNote("decisions.md", note); err != nil {
		return err
	}
	fmt.Printf("  Created: %sdecisions.md%s with 2 architectural decisions\n", cli.Cyan, cli.Reset)

	fmt.Printf("  Indexing...")
	if _, err := ts.indexAll(); err != nil {
		return err
	}
	fmt.Printf(" done.\n\n")

	results, err := ts.search("database choice", 3)
	if err != nil {
		return err
	}

	if len(results) > 0 {
		fmt.Printf("  %s✓%s Search for \"database choice\" found: %s%s%s\n",
			cli.Green, cli.Reset, cli.Bold, results[0].Title, cli.Reset)
		fmt.Printf("    Your AI will automatically surface this when discussing databases.\n")
		fmt.Printf("    Decisions tagged with %scontent_type: decision%s get priority boosting.\n", cli.Cyan, cli.Reset)
	}
	return nil
}

func lessonPin(ts *tutorialState) error {
	fmt.Printf("\n  Some notes should appear in %severy%s session, no matter what.\n", cli.Bold, cli.Reset)
	fmt.Printf("  Coding standards, team agreements, critical configs.\n\n")

	note := `---
title: "Coding Standards"
tags: [standards, team]
---
# Coding Standards
- All functions must have error handling
- Use conventional commits: feat:, fix:, docs:
- PR requires one approval and passing CI
- No console.log in production code
`
	if err := ts.writeNote("standards.md", note); err != nil {
		return err
	}
	fmt.Printf("  Created: %sstandards.md%s\n\n", cli.Cyan, cli.Reset)

	fmt.Printf("  Indexing...")
	if _, err := ts.indexAll(); err != nil {
		return err
	}
	fmt.Printf(" done.\n\n")

	// Pin it
	fmt.Printf("  %s$%s same pin standards.md\n\n", cli.Dim, cli.Reset)
	if err := ts.db.PinNote("standards.md"); err != nil {
		return err
	}
	fmt.Printf("  %s✓%s Pinned! This note now appears in %severy session%s.\n", cli.Green, cli.Reset, cli.Bold, cli.Reset)
	fmt.Printf("    Even if you're discussing something completely unrelated,\n")
	fmt.Printf("    your AI will see your coding standards.\n")
	fmt.Printf("\n    Manage pins: %ssame pin list%s / %ssame pin remove <path>%s\n", cli.Cyan, cli.Reset, cli.Cyan, cli.Reset)
	return nil
}

func lessonPrivacy(ts *tutorialState) error {
	fmt.Printf("\n  SAME has three privacy tiers — enforced by your filesystem.\n\n")
	fmt.Printf("    %sYour notes%s     — Indexed, searchable by your AI\n", cli.Bold, cli.Reset)
	fmt.Printf("    %s_PRIVATE/%s     — %sNever indexed%s, never committed to git\n", cli.Bold, cli.Reset, cli.Red, cli.Reset)
	fmt.Printf("    %sresearch/%s     — Indexed but gitignored (local-only)\n\n", cli.Bold, cli.Reset)

	// Create a regular note and a private note
	public := `---
title: "API Design"
tags: [api]
---
# API Design
Public endpoints use /api/v1/ prefix.
`
	private := `---
title: "Secret Keys"
tags: [credentials]
---
# Secret Keys
API_KEY=sk-example-key-do-not-share
`
	if err := ts.writeNote("api-design.md", public); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(ts.dir, "_PRIVATE"), 0o755); err != nil {
		return fmt.Errorf("create _PRIVATE tutorial directory: %w", err)
	}
	if err := ts.writeNote("_PRIVATE/secrets.md", private); err != nil {
		return err
	}

	fmt.Printf("  Created: %sapi-design.md%s (regular note)\n", cli.Cyan, cli.Reset)
	fmt.Printf("  Created: %s_PRIVATE/secrets.md%s (private note)\n", cli.Cyan, cli.Reset)

	fmt.Printf("  Indexing...")
	if _, err := ts.indexAll(); err != nil {
		return err
	}
	fmt.Printf(" done.\n\n")

	// Search for "API" — should find api-design but not secrets
	results, err := ts.search("API key", 5)
	if err != nil {
		return err
	}

	foundPrivate := false
	for _, r := range results {
		if strings.HasPrefix(r.Path, "_PRIVATE/") {
			foundPrivate = true
		}
	}

	if !foundPrivate {
		fmt.Printf("  %s✓%s Search for \"API key\" found api-design.md but %sNOT%s _PRIVATE/secrets.md\n",
			cli.Green, cli.Reset, cli.Bold, cli.Reset)
		fmt.Printf("    Private notes are structurally invisible to search.\n")
		fmt.Printf("    No configuration needed — the filesystem IS the policy.\n")
	} else {
		fmt.Printf("  %s!%s Privacy filtering is enforced during context surfacing.\n", cli.Yellow, cli.Reset)
	}
	return nil
}

func lessonAsk(ts *tutorialState) error {
	fmt.Printf("\n  %ssame ask%s lets you ask questions and get answers FROM your notes.\n", cli.Bold, cli.Reset)
	fmt.Printf("  Like ChatGPT, but the answers come from YOUR knowledge base.\n\n")

	// Write some notes to ask about
	arch := `---
title: "Architecture"
tags: [architecture, backend]
---
# Architecture
Database: PostgreSQL 15 with pgbouncer connection pooling.
Cache: Redis 7 for session data and API response caching.
Auth: JWT with 15-minute access tokens and 7-day refresh tokens.
`
	deploy := `---
title: "Deployment Guide"
tags: [deployment, ops]
---
# Deployment Guide
1. Push to main triggers CI pipeline
2. Tests must pass before deploy
3. Docker image built and pushed to registry
4. Kubernetes rolling update with zero downtime
5. Health checks verify the new version before traffic switch
`
	if err := ts.writeNote("architecture.md", arch); err != nil {
		return err
	}
	if err := ts.writeNote("deployment.md", deploy); err != nil {
		return err
	}

	fmt.Printf("  Created: %sarchitecture.md%s, %sdeployment.md%s\n", cli.Cyan, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("  Indexing...")
	if _, err := ts.indexAll(); err != nil {
		return err
	}
	fmt.Printf(" done.\n\n")

	question := "how do we deploy?"
	fmt.Printf("  %s$%s same ask \"%s\"\n\n", cli.Dim, cli.Reset, question)

	// Try to answer using the configured chat provider.
	chatClient, chatErr := llm.NewClient()
	if chatErr == nil {
		bestModel, _ := chatClient.PickBestModel()
		if bestModel != "" {
			results, _ := ts.search(question, 5)
			if len(results) > 0 {
				var ctx strings.Builder
				for i, r := range results {
					ctx.WriteString(fmt.Sprintf("--- Source %d: %s ---\n%s\n\n", i+1, r.Title, r.Snippet))
				}
				prompt := fmt.Sprintf(`Answer using ONLY these notes. Cite sources. Keep it under 3 sentences.

NOTES:
%s
QUESTION: %s

Answer:`, ctx.String(), question)

				fmt.Printf("  %sThinking with %s...%s\n\n", cli.Dim, bestModel, cli.Reset)
				answer, err := chatClient.Generate(bestModel, prompt)
				if err == nil && answer != "" {
					for _, line := range strings.Split(answer, "\n") {
						fmt.Printf("  %s\n", line)
					}
					fmt.Printf("\n  %s✓%s Answer sourced from your notes.\n", cli.Green, cli.Reset)
					return nil
				}
			}
		}
	}

	// No chat provider available.
	fmt.Printf("  %sNo chat provider available — 'same ask' needs chat configured.%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  Configure one of these options:\n")
	fmt.Printf("    %s• Local: SAME_CHAT_PROVIDER=ollama and run 'ollama pull llama3.2'%s\n", cli.Dim, cli.Reset)
	fmt.Printf("    %s• Cloud/OpenAI-compatible: set SAME_CHAT_PROVIDER + model (+ base URL/API key as needed)%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  Then try: %ssame ask \"how do we deploy?\"%s\n", cli.Cyan, cli.Reset)
	return nil
}

func lessonHandoff(ts *tutorialState) error {
	fmt.Printf("\n  When a coding session ends, SAME generates a handoff note.\n")
	fmt.Printf("  The next session picks up exactly where you left off.\n\n")

	handoff := `---
title: "Session Handoff — Feb 8"
tags: [handoff, session]
content_type: handoff
---
# Session Handoff — 2026-02-08

## What we worked on
- Fixed token refresh bug (issue #142)
- Root cause: tokens weren't being rotated on use

## Decisions made
- Adding refresh token rotation to prevent replay attacks
- Auth failures logged to dedicated audit table

## Next steps
- Write integration tests for token refresh
- Update API docs for auth endpoints
`
	if err := ts.writeNote("sessions/2026-02-08.md", handoff); err != nil {
		return err
	}

	fmt.Printf("  Created: %ssessions/2026-02-08.md%s\n", cli.Cyan, cli.Reset)
	fmt.Printf("  Indexing...")
	if _, err := ts.indexAll(); err != nil {
		return err
	}
	fmt.Printf(" done.\n\n")

	fmt.Printf("  When your AI starts the next session, it sees:\n\n")
	fmt.Printf("    %s\"Last session: Fixed token refresh bug (#142).%s\n", cli.Dim, cli.Reset)
	fmt.Printf("    %s Decisions: refresh token rotation, auth audit logging.%s\n", cli.Dim, cli.Reset)
	fmt.Printf("    %s Next: write integration tests, update API docs.\"%s\n\n", cli.Dim, cli.Reset)
	fmt.Printf("  %s✓%s No more \"what were we working on?\" — it's automatic.\n", cli.Green, cli.Reset)
	fmt.Printf("    Handoffs are generated by SAME's Stop hook when your session ends.\n")
	fmt.Printf("    They go in %ssessions/%s by default (configurable).\n", cli.Cyan, cli.Reset)
	return nil
}

func lessonGraph(ts *tutorialState) error {
	fmt.Printf("\n  SAME can build a relationship graph from your notes.\n")
	fmt.Printf("  Let's create a tiny vault and traverse it.\n\n")

	arch := `---
title: "Architecture"
tags: [architecture]
---
# Architecture
We decided to use event-driven workers.
See notes/queue.md for queue details.
`
	queue := `---
title: "Queue Design"
tags: [backend, queue]
---
# Queue Design
Workers process jobs with retries and dead-letter queues.
`
	if err := ts.writeNote("notes/architecture.md", arch); err != nil {
		return err
	}
	if err := ts.writeNote("notes/queue.md", queue); err != nil {
		return err
	}
	fmt.Printf("  Created: %snotes/architecture.md%s and %snotes/queue.md%s\n", cli.Cyan, cli.Reset, cli.Cyan, cli.Reset)

	fmt.Printf("  Indexing + extracting graph links...")
	if _, err := ts.indexAll(); err != nil {
		return err
	}
	fmt.Printf(" done.\n\n")

	gdb := graph.NewDB(ts.db.Conn())
	paths, err := gdb.QueryGraph(graph.QueryOptions{
		FromNodeType: graph.NodeNote,
		FromNodeName: "notes/architecture.md",
		MaxDepth:     2,
		Direction:    "forward",
	})
	if err != nil {
		return fmt.Errorf("query graph: %w", err)
	}

	if len(paths) == 0 {
		fmt.Printf("  No paths found yet. Try rebuilding graph with:\n")
		fmt.Printf("    %s$%s same graph rebuild\n", cli.Dim, cli.Reset)
		return nil
	}

	fmt.Printf("  %s✓%s Found %d path(s). Example:\n", cli.Green, cli.Reset, len(paths))
	sample := paths[0]
	for i, n := range sample.Nodes {
		if i > 0 {
			rel := "related_to"
			if i-1 < len(sample.Edges) && sample.Edges[i-1].Relationship != "" {
				rel = sample.Edges[i-1].Relationship
			}
			fmt.Printf("    --[%s]--> ", rel)
		} else {
			fmt.Printf("    ")
		}
		fmt.Printf("[%s] %s\n", n.Type, n.Name)
	}
	fmt.Printf("\n  Useful commands:\n")
	fmt.Printf("    %s$%s same graph stats\n", cli.Dim, cli.Reset)
	fmt.Printf("    %s$%s same graph query --type note --node \"notes/architecture.md\" --depth 2\n", cli.Dim, cli.Reset)
	fmt.Printf("    %s$%s same web   # open dashboard with graph highlights + note connections\n", cli.Dim, cli.Reset)
	return nil
}
