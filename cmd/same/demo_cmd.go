package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/ollama"
	"github.com/sgx-labs/statelessagent/internal/store"
)

var demoNotes = map[string]string{
	"architecture.md": `---
title: "Architecture Overview"
tags: [architecture, decisions, backend]
content_type: decision
domain: engineering
---

# Architecture Overview

We chose JWT for authentication with refresh token rotation. Access tokens
expire after 15 minutes, refresh tokens after 7 days. Tokens are stored
in httpOnly cookies for security.

Database is PostgreSQL 15 with connection pooling via pgbouncer. We use
a read replica for analytics queries to avoid impacting production traffic.

Caching layer is Redis 7, used for session data and frequently accessed
API responses. Cache invalidation follows a write-through pattern.

The API follows REST conventions with versioned endpoints (/api/v1/).
We considered GraphQL but chose REST for simplicity and team familiarity.
`,
	"decisions.md": `---
title: "Decisions Log"
tags: [decisions, architecture]
content_type: decision
---

# Decisions Log

## Decision: JWT over session cookies
**Date:** 2026-01-15
**Status:** Accepted
JWT with refresh rotation chosen over server-side sessions. Reasoning:
stateless auth scales better with our microservice architecture. Trade-off:
slightly larger request headers, but eliminates session store dependency.

## Decision: PostgreSQL over MongoDB
**Date:** 2026-01-10
**Status:** Accepted
Relational model fits our data better. Strong consistency guarantees needed
for financial transactions. Team has more SQL experience.

## Decision: Monorepo structure
**Date:** 2026-01-08
**Status:** Accepted
Single repo for API, frontend, and shared types. Simplifies CI/CD and
ensures type consistency across boundaries.
`,
	"api-redesign.md": `---
title: "API Redesign Plan"
tags: [api, migration, planning]
content_type: note
domain: engineering
workstream: api-v2
---

# API Redesign — v2 Migration

## Goals
- Migrate from REST v1 to v2 with breaking changes
- Add pagination to all list endpoints
- Standardize error response format
- Add rate limiting per API key

## Timeline
- Week 1-2: Design new endpoint schemas
- Week 3-4: Implement v2 alongside v1
- Week 5: Internal testing and migration guide
- Week 6: Deprecation notices on v1 endpoints

## Breaking Changes
1. All timestamps now ISO 8601 (was Unix epoch)
2. Pagination uses cursor-based (was offset-based)
3. Error responses use RFC 7807 Problem Details format
4. Authentication header changed from X-API-Key to Authorization: Bearer
`,
	"coding-standards.md": `---
title: "Coding Standards"
tags: [standards, code-quality, team]
content_type: reference
---

# Coding Standards

## Go
- Use gofmt and golangci-lint before committing
- Error messages start lowercase, no trailing punctuation
- Table-driven tests for functions with multiple cases
- Context as first parameter in all public functions

## Git
- Conventional commits: feat:, fix:, docs:, refactor:
- Squash merge to main, keep feature branch history
- PR requires one approval and passing CI

## API
- RESTful resource naming (plural nouns)
- Always return JSON, even for errors
- Version in URL path, not headers
- Rate limit headers on every response
`,
	"sessions/2026-02-08-handoff.md": `---
title: "Session Handoff — Feb 8"
tags: [handoff, session]
content_type: handoff
---

# Session Handoff — 2026-02-08

## What we worked on
- Fixed the authentication token refresh bug (issue #142)
- Root cause: refresh tokens weren't being rotated on use
- Added retry logic for failed token refreshes

## Key decisions made
- Will add refresh token rotation to prevent replay attacks
- Decided to log all auth failures to a dedicated audit table

## Next steps
- Write integration tests for the token refresh flow
- Update the API documentation for auth endpoints
- Review the rate limiting implementation
`,
	"research/performance-analysis.md": `---
title: "Performance Analysis"
tags: [performance, optimization, research]
content_type: note
domain: engineering
---

# Performance Analysis — February 2026

## Current Metrics
- API p50 latency: 45ms
- API p99 latency: 280ms
- Database query avg: 12ms
- Redis cache hit rate: 87%

## Bottlenecks Identified
1. N+1 queries on the /users/:id/orders endpoint (adds 150ms)
2. JSON serialization overhead on large response payloads
3. Missing index on orders.created_at (sequential scan on date filters)

## Recommendations
- Add eager loading for user orders query
- Switch to streaming JSON encoder for large payloads
- Add composite index on (user_id, created_at) to orders table
- Consider connection pool tuning (current: 20, recommended: 50)
`,
}

func demoCmd() *cobra.Command {
	var clean bool
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "See SAME in action with sample notes",
		Long: `Run an interactive demo that creates sample notes, indexes them,
and shows how SAME helps your AI remember your project context.

No existing notes are modified. The demo uses a temporary vault.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDemo(clean)
		},
	}
	cmd.Flags().BoolVar(&clean, "clean", false, "Delete demo vault after running")
	return cmd
}

func runDemo(clean bool) error {
	fmt.Printf("\n  %s✦ SAME Demo%s — see how your AI remembers\n\n", cli.Bold+cli.Cyan, cli.Reset)

	// 1. Create temp vault
	demoDir, err := os.MkdirTemp("", "same-demo-")
	if err != nil {
		return fmt.Errorf("create demo directory: %w", err)
	}

	fmt.Printf("  Creating demo vault with %d sample notes...\n", len(demoNotes))

	// Write demo notes
	for relPath, content := range demoNotes {
		fullPath := filepath.Join(demoDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write demo note: %w", err)
		}
	}

	// Create .same marker so vault detection works
	sameDir := filepath.Join(demoDir, ".same", "data")
	if err := os.MkdirAll(sameDir, 0o755); err != nil {
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
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// 3. Index — use Ollama if available, fall back to lite mode
	ollamaAvailable := true
	fmt.Printf("  Indexing")
	stats, err := indexer.Reindex(db, true)
	if err != nil {
		// Embedding provider failed — fall back to lite mode
		ollamaAvailable = false
		fmt.Fprintf(os.Stderr, "\n  %s[DEBUG] Reindex error: %v%s\n", cli.Dim, err, cli.Reset)
		fmt.Printf("  Indexing (keyword mode)...")
		stats, err = indexer.ReindexLite(db, true, nil)
		if err != nil {
			fmt.Println()
			return fmt.Errorf("indexing failed: %w", err)
		}
	} else {
		fmt.Printf(" (semantic mode)...")
	}
	fmt.Printf(" done (%d notes, %d chunks)\n", stats.TotalFiles, stats.ChunksInIndex)
	if !ollamaAvailable {
		fmt.Printf("  %sInstall Ollama for AI-powered semantic search%s\n", cli.Dim, cli.Reset)
	}

	// 4. Search demo
	cli.Section("Search")
	query := "authentication approach"
	fmt.Printf("  %s$%s same search \"%s\" --vault %s\n\n", cli.Dim, cli.Reset, query, demoDir)

	var results []store.SearchResult
	if ollamaAvailable {
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

	if len(results) > 0 {
		for i, r := range results {
			snippet := r.Snippet
			if len(snippet) > 120 {
				snippet = snippet[:120] + "..."
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Printf("  %d. %s%s%s (score: %.2f)\n", i+1, cli.Bold, r.Title, cli.Reset, r.Score)
			fmt.Printf("     %s\"%s\"%s\n\n", cli.Dim, snippet, cli.Reset)
		}
		fmt.Printf("  %s✓%s SAME found the right notes from %d indexed documents.\n",
			cli.Green, cli.Reset, stats.TotalFiles)
	}

	// 5. Session continuity preview
	cli.Section("Session Continuity")
	fmt.Printf("  When your AI starts a new session, it automatically gets:\n\n")
	fmt.Printf("    %s\"Last session worked on: API redesign — migrating from REST to v2.\n", cli.Dim)
	fmt.Printf("     Key decisions: JWT auth, PostgreSQL, Redis caching.\n")
	fmt.Printf("     %d notes indexed. Ready to pick up where you left off.\"%s\n\n", stats.TotalFiles, cli.Reset)
	fmt.Printf("  %sNo copy-pasting. No re-explaining. Automatic.%s\n", cli.Dim, cli.Reset)

	// 6. Pin demo
	cli.Section("Pin")
	pinPath := "coding-standards.md"
	fmt.Printf("  %s$%s same pin %s\n\n", cli.Dim, cli.Reset, pinPath)
	if err := db.PinNote(pinPath); err == nil {
		fmt.Printf("  %s✓%s Pinned %s%s%s\n", cli.Green, cli.Reset, cli.Cyan, pinPath, cli.Reset)
		fmt.Printf("  %sThis note will appear in EVERY session, regardless of topic.%s\n", cli.Dim, cli.Reset)
	}

	// 7. Ask demo (if Ollama has a chat model)
	llm, llmErr := ollama.NewClient()
	if llmErr == nil {
		bestModel, _ := llm.PickBestModel()
		if bestModel != "" {
			cli.Section("Ask — the magic moment")

			askQuery := "what did we decide about authentication?"
			fmt.Printf("  %s$%s same ask \"%s\"\n\n", cli.Dim, cli.Reset, askQuery)

			// Get context via search
			var askResults []store.SearchResult
			if ollamaAvailable {
				embedClient, _ := newEmbedProvider()
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
					ctx.WriteString(fmt.Sprintf("--- Source %d: %s (%s) ---\n", i+1, r.Title, r.Path))
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

				answer, err := llm.Generate(bestModel, prompt)
				if err == nil && answer != "" {
					for _, line := range strings.Split(answer, "\n") {
						fmt.Printf("  %s\n", line)
					}
					fmt.Printf("\n  %s✓%s Answered from YOUR notes. 100%% local. No cloud API.\n",
						cli.Green, cli.Reset)
				}
			}
		}
	}

	// 8. Done
	cli.Section("Ready")
	fmt.Printf("  To use SAME with your own project:\n\n")
	fmt.Printf("    %s$%s cd ~/your-project && same init\n\n", cli.Dim, cli.Reset)
	fmt.Printf("  Demo vault saved at: %s%s%s\n", cli.Dim, demoDir, cli.Reset)
	fmt.Printf("  %sExplore: same search \"query\" --vault %s%s\n\n", cli.Dim, demoDir, cli.Reset)

	if clean {
		os.RemoveAll(demoDir)
		fmt.Printf("  %s(demo vault cleaned up)%s\n\n", cli.Dim, cli.Reset)
	}

	return nil
}
