package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/setup"
	"github.com/sgx-labs/statelessagent/internal/store"
)

type demoNote struct {
	Path    string
	Content string
}

var demoNotes = []demoNote{
	{"decisions/2026-03-03-auth-strategy.md", `---
title: "Decision: Auth Strategy"
tags: [decisions, auth, security, backend]
content_type: decision
domain: engineering
---

# Decision: Authentication Strategy

**Date:** 2026-03-03
**Status:** Accepted

## Context
We need stateless auth that scales across our microservices without a shared
session store. The team evaluated JWT, Paseto, and server-side sessions.

## Decision
JWT with refresh token rotation. Access tokens expire after 15 minutes,
refresh tokens after 7 days with single-use rotation.

## Rationale
- Stateless: no session store dependency, works across services
- Refresh rotation prevents token replay attacks
- httpOnly cookies for storage (not localStorage) to mitigate XSS
- Trade-off: slightly larger request headers, but eliminates a whole
  infrastructure dependency

## Alternatives Rejected
- Server-side sessions: requires shared Redis, adds latency + failure mode
- Paseto: better security properties but limited library ecosystem in Go
`},
	{"sessions/2026-03-07-handoff.md", `---
title: "Session Handoff — March 7"
tags: [handoff, session, api]
content_type: handoff
---

# Session Handoff — 2026-03-07

## What was accomplished
- Implemented cursor-based pagination on /api/v2/orders
- Fixed the N+1 query on /users/:id/orders (was adding 150ms per request)
- Added composite index on (user_id, created_at) — query time dropped from
  180ms to 8ms

## Key decisions
- Using cursor-based pagination instead of offset (better for large datasets)
- Error responses now follow RFC 7807 Problem Details format

## Blocked on
- Rate limiting middleware needs config schema review (draft in rate-limit.md)

## Next steps for whoever picks this up
1. Wire up rate limiting middleware to the v2 router
2. Add integration tests for cursor pagination edge cases (empty page, deleted cursor)
3. Update the API migration guide with the new error format
4. Run load test against the orders endpoint with the new index
`},
	{"research/stripe-webhook-setup.md", `---
title: "Stripe Webhook Integration"
tags: [research, stripe, payments, api]
content_type: note
domain: engineering
workstream: payments
---

# Stripe Webhook Integration — Research Notes

## Setup
Stripe sends events via POST to our /webhooks/stripe endpoint. Events are
signed with a webhook secret (whsec_...) that we verify server-side.

## Key Events We Handle
- checkout.session.completed — provision access
- invoice.payment_failed — send dunning email, retry 3x
- customer.subscription.deleted — revoke access after grace period

## Gotchas Found
1. Events can arrive out of order — always fetch current state from Stripe API
2. Webhook endpoint MUST return 200 within 5 seconds or Stripe retries
3. Use idempotency keys on our side to prevent double-provisioning
4. Test mode and live mode use different webhook secrets

## Verification Code Pattern
` + "```go" + `
err := webhook.VerifySignature(payload, sigHeader, whsec)
` + "```" + `

Stripe retries failed webhooks for up to 3 days with exponential backoff.
`},
	{"bugs/2026-03-05-token-refresh-loop.md", `---
title: "Bug: Token Refresh Infinite Loop"
tags: [bug, auth, investigation, postmortem]
content_type: note
domain: engineering
---

# Bug Investigation: Token Refresh Infinite Loop

**Reported:** 2026-03-05
**Severity:** P1 — users getting logged out in production
**Status:** Root cause found, fix deployed

## Symptoms
- Users reporting random logouts after ~15 minutes
- Auth service logs showing rapid token refresh requests (100+/second per user)
- Redis connection pool exhaustion

## Root Cause
The refresh token rotation was not atomic. When two tabs refreshed
simultaneously, both sent the same refresh token. The first request
succeeded and rotated the token. The second request failed (token already
used), which triggered the client retry logic, which created a loop.

## Fix
Added a 5-second grace window for recently-rotated refresh tokens.
If a refresh request arrives with a token that was rotated <5 seconds ago,
we return the same new token pair instead of rejecting it.

## Lessons Learned
- Refresh token rotation needs to handle concurrent clients
- Added metrics on refresh rate per user to catch loops early
- Client-side: added jitter to retry timing + max 3 retries
`},
	{"sessions/2026-03-04-standup.md", `---
title: "Session — March 4 Standup"
tags: [session, standup, progress]
content_type: session
---

# Session Notes — 2026-03-04

## Done today
- Merged PR #47: Stripe webhook handler with signature verification
- Code review on PR #51: cursor pagination (left 3 comments)
- Set up staging environment for API v2 testing

## In progress
- API v2 migration guide (60% done, need to document error format changes)
- Load testing script for the new orders endpoint

## Blockers
- Need DevOps to provision a second read replica for staging
- Waiting on product to confirm grace period length for subscription cancellation
`},
}

func demoCmd() *cobra.Command {
	var clean bool
	var noSetup bool
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "See SAME in action with sample notes",
		Long: `Run an interactive demo that creates sample notes, indexes them,
and shows how SAME helps your AI remember your project context.

No existing notes are modified. The demo uses a temporary vault.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDemo(clean, noSetup)
		},
	}
	cmd.Flags().BoolVar(&clean, "clean", false, "Delete demo vault after running")
	cmd.Flags().BoolVar(&noSetup, "no-setup", false, "Skip the setup prompt at the end")
	return cmd
}

// demoPause adds a brief delay between sections so the user can absorb each result.
func demoPause() {
	time.Sleep(250 * time.Millisecond)
}

func runDemo(clean, noSetup bool) error {
	fmt.Printf("\n  %s✦ SAME Demo%s — see how your AI remembers\n\n", cli.Bold+cli.Cyan, cli.Reset)

	// 1. Create temp vault
	demoDir, err := os.MkdirTemp("", "same-demo-")
	if err != nil {
		return fmt.Errorf("create demo directory: %w", err)
	}

	fmt.Printf("  Creating demo vault with %d sample notes...\n", len(demoNotes))

	// Write demo notes
	for _, note := range demoNotes {
		fullPath := filepath.Join(demoDir, note.Path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		if err := os.WriteFile(fullPath, []byte(note.Content), 0o600); err != nil {
			return fmt.Errorf("write demo note: %w", err)
		}
	}

	// Create .same marker so vault detection works
	sameDir := filepath.Join(demoDir, ".same", "data")
	if err := os.MkdirAll(sameDir, 0o700); err != nil {
		return fmt.Errorf("create .same dir: %w", err)
	}

	// 2. Point config at demo vault
	origVault := config.VaultOverride
	config.VaultOverride = demoDir
	defer func() { config.VaultOverride = origVault }()

	// 3. Open DB and index
	dbPath := filepath.Join(sameDir, "vault.db")
	db, err := store.OpenPath(dbPath)
	if err != nil {
		return dbOpenError(err)
	}
	defer db.Close()

	// Index — use semantic mode if embeddings are available, else fall back to lite.
	semanticAvailable := true
	fmt.Printf("  Indexing...")
	indexStart := time.Now()
	stats, err := indexer.Reindex(db, true)
	mode := "semantic"
	if err != nil {
		// Embedding provider failed — fall back to lite mode
		semanticAvailable = false
		mode = "keyword"
		stats, err = indexer.ReindexLite(context.Background(), db, true, nil)
		if err != nil {
			fmt.Println()
			return fmt.Errorf("indexing failed: %w", err)
		}
	}
	indexElapsed := time.Since(indexStart)
	fmt.Printf(" done (%d notes, %d chunks, %s mode)\n", stats.TotalFiles, stats.ChunksInIndex, mode)
	fmt.Printf("  Indexed %d notes in %dms\n", stats.TotalFiles, indexElapsed.Milliseconds())
	if !semanticAvailable {
		fmt.Printf("\n  %sNote:%s Running in keyword-only mode (Ollama not detected).\n", cli.Yellow, cli.Reset)
		fmt.Printf("  Semantic search finds %srelated concepts%s, not just exact words.\n", cli.Bold, cli.Reset)
		fmt.Printf("  Install Ollama for the full experience: %shttps://ollama.com%s\n", cli.Cyan, cli.Reset)
	}

	demoPause()

	// ── Step 1: Search ──────────────────────────────────────────────────
	cli.Section("Step 1 — Search")
	query := "authentication approach"
	fmt.Printf("  %s$%s same search \"%s\"\n\n", cli.Dim, cli.Reset, query)
	demoPause()

	searchStart := time.Now()
	var results []store.SearchResult
	if semanticAvailable {
		embedClient, err := newEmbedProvider()
		if err == nil {
			queryVec, err := embedClient.GetQueryEmbedding(query)
			if err == nil {
				results, _ = db.VectorSearch(queryVec, store.SearchOptions{TopK: 3})
			}
		}
	}
	// Fallback to FTS5 if vector search didn't work
	if len(results) == 0 && db.FTSAvailable() {
		results, _ = db.FTS5Search(query, store.SearchOptions{TopK: 3})
	}
	searchElapsed := time.Since(searchStart)

	if len(results) > 0 {
		fmt.Printf("  Found in %dms:\n\n", searchElapsed.Milliseconds())
		// Split query into terms for highlighting
		queryTerms := strings.Fields(strings.ToLower(query))
		for i, r := range results {
			snippet := r.Snippet
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			snippet = truncateAtWord(snippet, 120)
			snippet = highlightTerms(snippet, queryTerms)
			fmt.Printf("  %d. %s%s%s (score: %.2f)\n", i+1, cli.Bold, r.Title, cli.Reset, r.Score)
			fmt.Printf("     %s\"%s\"%s\n\n", cli.Dim, snippet, cli.Reset)
		}
		fmt.Printf("  %s✓%s Your AI searched %d notes and found the auth decision in %dms.\n",
			cli.Green, cli.Reset, stats.TotalFiles, searchElapsed.Milliseconds())

		if !semanticAvailable {
			fmt.Printf("\n  %sWith semantic search, this query would also find notes about:%s\n", cli.Dim, cli.Reset)
			fmt.Printf("  %s  - \"session cookies\" and \"token rotation\" (related auth concepts)%s\n", cli.Dim, cli.Reset)
			fmt.Printf("  %s  - \"security\" and \"access control\" (broader topic matches)%s\n", cli.Dim, cli.Reset)
			fmt.Printf("  %s  Keyword search only matches the exact words you typed.%s\n", cli.Dim, cli.Reset)
		}
	}

	demoPause()

	// ── Step 2: Decisions ───────────────────────────────────────────────
	cli.Section("Step 2 — Decisions")
	fmt.Printf("  Your vault tracks decisions with context and rationale.\n\n")
	demoPause()
	fmt.Printf("  %s\"%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sOn March 3, you chose JWT with refresh token rotation over%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sserver-side sessions. Rationale: stateless auth scales across%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sservices without a shared session store. Paseto was considered%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sbut rejected due to limited Go library ecosystem.%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %s\"%s\n\n", cli.Dim, cli.Reset)
	fmt.Printf("  %s✓%s Three weeks later, a new agent knows %swhy%s you chose JWT,\n",
		cli.Green, cli.Reset, cli.Bold, cli.Reset)
	fmt.Printf("    not just %sthat%s you did.\n", cli.Bold, cli.Reset)

	demoPause()

	// ── Step 3: Session Handoff (the wow moment) ────────────────────────
	cli.Section("Step 3 — Session Handoff")
	fmt.Printf("  Imagine a new AI session starts tomorrow. It automatically gets:\n\n")
	demoPause()
	fmt.Printf("    %s┌──────────────────────────────────────────────────────────┐%s\n", cli.Cyan, cli.Reset)
	fmt.Printf("    %s│%s  %sLast session (March 7):%s                                %s│%s\n", cli.Cyan, cli.Reset, cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("    %s│%s                                                          %s│%s\n", cli.Cyan, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("    %s│%s  %s✓%s Implemented cursor-based pagination on /api/v2/orders %s│%s\n", cli.Cyan, cli.Reset, cli.Green, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("    %s│%s  %s✓%s Fixed N+1 query — response time 180ms to 8ms          %s│%s\n", cli.Cyan, cli.Reset, cli.Green, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("    %s│%s                                                          %s│%s\n", cli.Cyan, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("    %s│%s  %sNext:%s Wire up rate limiting, add pagination tests,     %s│%s\n", cli.Cyan, cli.Reset, cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("    %s│%s        update migration guide with new error format.     %s│%s\n", cli.Cyan, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("    %s└──────────────────────────────────────────────────────────┘%s\n\n", cli.Cyan, cli.Reset)
	demoPause()
	fmt.Printf("  %s✓%s No re-explaining. Your AI picks up exactly where you left off.\n",
		cli.Green, cli.Reset)

	demoPause()

	// ── Step 4: Memory Integrity ─────────────────────────────────────────
	cli.Section("Step 4 — Memory Integrity")
	fmt.Printf("  Not all notes age well. SAME tracks trust state so stale\n")
	fmt.Printf("  knowledge doesn't mislead your AI.\n\n")
	demoPause()

	// Mark 4 notes as validated, 1 as stale
	validatedPaths := []string{
		"decisions/2026-03-03-auth-strategy.md",
		"sessions/2026-03-07-handoff.md",
		"research/stripe-webhook-setup.md",
		"sessions/2026-03-04-standup.md",
	}
	stalePath := "bugs/2026-03-05-token-refresh-loop.md"

	_ = db.UpdateTrustState(validatedPaths, "validated")
	_ = db.UpdateTrustState([]string{stalePath}, "stale")

	// Show trust summary
	trustSummary, trustErr := db.GetTrustStateSummary()
	if trustErr == nil {
		fmt.Printf("  %s$%s same health\n\n", cli.Dim, cli.Reset)
		demoPause()
		fmt.Printf("    %sTrust: %s%d validated%s · %s%d stale%s\n\n",
			cli.Bold,
			cli.Green, trustSummary.Validated, cli.Reset+cli.Bold,
			cli.Yellow, trustSummary.Stale, cli.Reset)
	}

	// Re-search to show stale note ranked lower
	integrityQuery := "token refresh bug"
	fmt.Printf("  %s$%s same search \"%s\"\n\n", cli.Dim, cli.Reset, integrityQuery)
	demoPause()

	var intResults []store.SearchResult
	if semanticAvailable {
		embedClient, err := newEmbedProvider()
		if err == nil {
			qVec, err := embedClient.GetQueryEmbedding(integrityQuery)
			if err == nil {
				intResults, _ = db.VectorSearch(qVec, store.SearchOptions{TopK: 3})
			}
		}
	}
	if len(intResults) == 0 && db.FTSAvailable() {
		intResults, _ = db.FTS5Search(integrityQuery, store.SearchOptions{TopK: 3})
	}

	if len(intResults) > 0 {
		for i, r := range intResults {
			trustLabel := ""
			switch r.TrustState {
			case "validated":
				trustLabel = fmt.Sprintf(" %s[validated]%s", cli.Green, cli.Reset)
			case "stale":
				trustLabel = fmt.Sprintf(" %s[stale]%s", cli.Yellow, cli.Reset)
			case "contradicted":
				trustLabel = fmt.Sprintf(" %s[contradicted]%s", cli.Red, cli.Reset)
			}
			fmt.Printf("  %d. %s%s%s (score: %.2f)%s\n", i+1, cli.Bold, r.Title, cli.Reset, r.Score, trustLabel)
		}
		fmt.Printf("\n  %s✓%s Stale notes are flagged and rank lower — your AI sees what to trust.\n",
			cli.Green, cli.Reset)
	}

	demoPause()

	// ── Step 5: Ask (if chat model available) ───────────────────────────
	askShown := false
	chatClient, chatErr := llm.NewClient()
	if chatErr == nil {
		bestModel, _ := chatClient.PickBestModel()
		if bestModel != "" {
			askShown = true
			cli.Section("Step 5 — Ask")

			askQuery := "what caused the token refresh bug and how did we fix it?"
			fmt.Printf("  %s$%s same ask \"%s\"\n\n", cli.Dim, cli.Reset, askQuery)
			demoPause()

			// Get context via search
			var askResults []store.SearchResult
			if semanticAvailable {
				embedClient, embedErr := newEmbedProvider()
				if embedErr != nil {
					fmt.Fprintf(os.Stderr, "  %s⚠ Embedding unavailable: %v — using keyword search%s\n", cli.Dim, embedErr, cli.Reset)
				}
				if embedClient != nil {
					askVec, err := embedClient.GetQueryEmbedding(askQuery)
					if err == nil {
						askResults, _ = db.VectorSearch(askVec, store.SearchOptions{TopK: 5})
					}
				}
			}
			if len(askResults) == 0 && db.FTSAvailable() {
				askResults, _ = db.FTS5Search(askQuery, store.SearchOptions{TopK: 5})
			}

			if len(askResults) > 0 {
				var ctx strings.Builder
				for i, r := range askResults {
					fmt.Fprintf(&ctx, "--- Source %d: %s (%s) ---\n", i+1, r.Title, r.Path)
					snippet := r.Snippet
					if len(snippet) > 1000 {
						snippet = snippet[:1000]
					}
					ctx.WriteString(snippet)
					ctx.WriteString("\n\n")
				}

				prompt := fmt.Sprintf(`You are a helpful assistant that answers questions using ONLY the provided notes.
If the notes don't contain enough information to answer, say so honestly.
Always cite which source(s) you used. Keep it under 4 sentences.

NOTES:
%s
QUESTION: %s

Answer concisely, citing sources by name:`, ctx.String(), askQuery)

				answer, err := chatClient.Generate(bestModel, prompt)
				if err == nil && answer != "" {
					for _, line := range strings.Split(answer, "\n") {
						fmt.Printf("  %s\n", line)
					}
					fmt.Printf("\n  %s✓%s Answered from your notes — with sources.\n",
						cli.Green, cli.Reset)
				}
			}
		}
	}

	// Show a note if the Ask section was skipped
	if !askShown {
		cli.Section("Ask")
		fmt.Printf("  %sWith a local chat model, you can ask questions about your project%s\n", cli.Dim, cli.Reset)
		fmt.Printf("  %sand get answers grounded in your actual notes and decisions.%s\n", cli.Dim, cli.Reset)
		fmt.Printf("  %sInstall a model:%s ollama pull llama3.2\n", cli.Dim, cli.Reset)
	}

	// ── Graph LLM tip ───────────────────────────────────────────────────
	if config.GraphLLMMode() == "off" {
		// Only show if a chat model is available (don't suggest if they can't use it)
		if chatErr == nil {
			if model, _ := chatClient.PickBestModel(); model != "" {
				fmt.Printf("\n  %sTip:%s %s'same graph enable'%s unlocks richer knowledge graph extraction.\n",
					cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
				fmt.Printf("        Works best with 7B+ models. Run %s'same tips'%s for model guidance.\n",
					cli.Cyan, cli.Reset)
			}
		} else {
			// chatClient wasn't created above — try fresh
			if c, e := llm.NewClient(); e == nil {
				if m, _ := c.PickBestModel(); m != "" {
					fmt.Printf("\n  %sTip:%s %s'same graph enable'%s unlocks richer knowledge graph extraction.\n",
						cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
					fmt.Printf("        Works best with 7B+ models. Run %s'same tips'%s for model guidance.\n",
						cli.Cyan, cli.Reset)
				}
			}
		}
	}

	// ── Closing ─────────────────────────────────────────────────────────
	fmt.Printf("\n  %s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", cli.Cyan, cli.Reset)
	fmt.Printf("\n  %sThat's SAME. Your AI remembers what you've built — and knows what to trust.%s\n\n", cli.Bold, cli.Reset)

	if clean {
		if err := os.RemoveAll(demoDir); err != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to clean up demo vault %q: %v\n", demoDir, err)
		}
		fmt.Printf("  %s(demo vault cleaned up)%s\n\n", cli.Dim, cli.Reset)
	}

	// ── Setup prompt ────────────────────────────────────────────────────
	if noSetup {
		fmt.Printf("  Run %ssame init%s anytime to set up a vault for your project.\n\n", cli.Cyan, cli.Reset)
		return nil
	}

	// If the current directory already has a vault, skip the init wizard entirely.
	cwd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(cwd, ".same")); err == nil {
		fmt.Printf("  %sVault already set up in %s%s — you're good to go!\n",
			cli.Dim, cli.ShortenHome(cwd), cli.Reset)
		fmt.Printf("  Run %ssame search \"query\"%s to try it on your own notes.\n\n", cli.Cyan, cli.Reset)
		return nil
	}

	fmt.Printf("  Ready to set up a vault for your project? %s(Y/n)%s ", cli.Dim, cli.Reset)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer == "" || answer == "y" || answer == "yes" {
		fmt.Println()
		// Prompt for directory or use current
		cwd, _ := os.Getwd()
		fmt.Printf("  Set up SAME in %s%s%s? %s(Y/n, or enter a path)%s ",
			cli.Bold, cli.ShortenHome(cwd), cli.Reset, cli.Dim, cli.Reset)
		dirAnswer, _ := reader.ReadString('\n')
		dirAnswer = strings.TrimSpace(dirAnswer)

		targetDir := cwd
		if dirAnswer != "" && dirAnswer != "y" && dirAnswer != "Y" && dirAnswer != "yes" {
			// User entered a path
			expanded := dirAnswer
			if strings.HasPrefix(expanded, "~/") {
				home, _ := os.UserHomeDir()
				expanded = filepath.Join(home, expanded[2:])
			}
			abs, err := filepath.Abs(expanded)
			if err == nil {
				targetDir = abs
			}
			// Create the directory if it doesn't exist
			if err := os.MkdirAll(targetDir, 0o755); err != nil {
				fmt.Printf("\n  %sCouldn't create %s: %v%s\n", cli.Red, targetDir, err, cli.Reset)
				fmt.Printf("  Run %ssame init%s from your project directory.\n\n", cli.Cyan, cli.Reset)
				return nil
			}
		}

		// Warn if target is home directory
		if homeDir, err := os.UserHomeDir(); err == nil && targetDir == homeDir {
			fmt.Printf("\n  %sWarning:%s You're in your home directory. Consider cd'ing to a project folder first,\n", cli.Yellow, cli.Reset)
			fmt.Printf("  or SAME will index everything here.\n")
			fmt.Printf("  Continue anyway? %s(y/N)%s ", cli.Dim, cli.Reset)
			confirmAnswer, _ := reader.ReadString('\n')
			confirmAnswer = strings.TrimSpace(strings.ToLower(confirmAnswer))
			if confirmAnswer != "y" && confirmAnswer != "yes" {
				fmt.Printf("\n  Run %ssame init%s from your project directory.\n\n", cli.Cyan, cli.Reset)
				return nil
			}
		}

		// Change to target directory and run init
		origDir, _ := os.Getwd()
		if err := os.Chdir(targetDir); err != nil {
			fmt.Printf("\n  %sCouldn't access %s: %v%s\n", cli.Red, targetDir, err, cli.Reset)
			fmt.Printf("  Run %ssame init%s from your project directory.\n\n", cli.Cyan, cli.Reset)
			return nil
		}
		defer func() { _ = os.Chdir(origDir) }()

		config.VaultOverride = "" // Clear demo vault override
		fmt.Println()
		return setup.RunInit(setup.InitOptions{
			Version: Version,
		})
	}

	fmt.Printf("\n  Run %ssame init%s anytime.\n\n", cli.Cyan, cli.Reset)
	return nil
}

// truncateAtWord truncates s to at most maxLen characters at a word boundary.
func truncateAtWord(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find last space at or before maxLen
	cut := strings.LastIndex(s[:maxLen], " ")
	if cut <= 0 {
		cut = maxLen
	}
	return s[:cut] + "..."
}

// highlightTerms highlights occurrences of query terms in text using cyan+bold ANSI codes.
func highlightTerms(text string, terms []string) string {
	for _, term := range terms {
		lower := strings.ToLower(text)
		termLower := strings.ToLower(term)
		var result strings.Builder
		pos := 0
		for {
			idx := strings.Index(lower[pos:], termLower)
			if idx < 0 {
				result.WriteString(text[pos:])
				break
			}
			result.WriteString(text[pos : pos+idx])
			result.WriteString(cli.Reset + cli.Bold + cli.Cyan)
			result.WriteString(text[pos+idx : pos+idx+len(term)])
			result.WriteString(cli.Reset + cli.Dim)
			pos += idx + len(term)
		}
		text = result.String()
	}
	return text
}
