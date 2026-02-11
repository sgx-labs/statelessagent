// Package main is the entrypoint for the SAME CLI.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/hooks"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/ollama"
	"github.com/sgx-labs/statelessagent/internal/setup"
	"github.com/sgx-labs/statelessagent/internal/store"
	"github.com/sgx-labs/statelessagent/internal/watcher"
)

// Version is set at build time via ldflags.
var Version = "dev"

// compareSemver compares two semver strings (without "v" prefix).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Falls back to string comparison if parsing fails.
func compareSemver(a, b string) int {
	parseSemver := func(s string) (major, minor, patch int, ok bool) {
		// Strip any pre-release suffix (e.g., "1.2.3-beta")
		if idx := strings.IndexByte(s, '-'); idx >= 0 {
			s = s[:idx]
		}
		parts := strings.Split(s, ".")
		if len(parts) < 1 || len(parts) > 3 {
			return 0, 0, 0, false
		}
		var err error
		major, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, 0, false
		}
		if len(parts) >= 2 {
			minor, err = strconv.Atoi(parts[1])
			if err != nil {
				return 0, 0, 0, false
			}
		}
		if len(parts) >= 3 {
			patch, err = strconv.Atoi(parts[2])
			if err != nil {
				return 0, 0, 0, false
			}
		}
		return major, minor, patch, true
	}

	aMaj, aMin, aPat, aOK := parseSemver(a)
	bMaj, bMin, bPat, bOK := parseSemver(b)

	if !aOK || !bOK {
		// Fallback to string comparison if parsing fails
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}

	if aMaj != bMaj {
		if aMaj < bMaj {
			return -1
		}
		return 1
	}
	if aMin != bMin {
		if aMin < bMin {
			return -1
		}
		return 1
	}
	if aPat != bPat {
		if aPat < bPat {
			return -1
		}
		return 1
	}
	return 0
}

// newEmbedProvider creates an embedding provider from config.
// Only passes the Ollama base URL to the Ollama provider; OpenAI uses its own default.
func newEmbedProvider() (embedding.Provider, error) {
	ec := config.EmbeddingProviderConfig()
	cfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		Dimensions: ec.Dimensions,
	}

	// Only pass the Ollama URL to the Ollama provider
	if cfg.Provider == "ollama" || cfg.Provider == "" {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			return nil, fmt.Errorf("ollama URL: %w", err)
		}
		cfg.BaseURL = ollamaURL
	}

	return embedding.NewProvider(cfg)
}

func main() {
	root := &cobra.Command{
		Use:   "same",
		Short: "Give your AI a memory of your project",
		Long: `SAME (Stateless Agent Memory Engine) gives your AI a memory.

Your AI will remember your project decisions, your preferences, and what you've
built together across sessions. No more re-explaining everything.

Quick Start:
  same init     Set up SAME for your project (run this first)
  same status   See what SAME is tracking
  same doctor   Check if everything is working

Need help? https://discord.gg/GZGHtrrKF2`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	root.AddCommand(initCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(updateCmd())
	root.AddCommand(reindexCmd())
	root.AddCommand(statsCmd())
	root.AddCommand(migrateCmd())
	root.AddCommand(hookCmd())
	root.AddCommand(hooksCmd())
	root.AddCommand(mcpCmd())
	root.AddCommand(benchCmd())
	root.AddCommand(searchCmd())
	root.AddCommand(relatedCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(budgetCmd())
	root.AddCommand(vaultCmd())
	root.AddCommand(watchCmd())
	root.AddCommand(pluginCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(logCmd())
	root.AddCommand(configCmd())
	root.AddCommand(setupSubCmd())
	root.AddCommand(displayCmd())
	root.AddCommand(profileCmd())
	root.AddCommand(guardCmd())
	root.AddCommand(pushAllowCmd())
	root.AddCommand(ciCmd())
	root.AddCommand(repairCmd())
	root.AddCommand(feedbackCmd())
	root.AddCommand(pinCmd())
	root.AddCommand(askCmd())
	root.AddCommand(demoCmd())
	root.AddCommand(tutorialCmd())

	// Global --vault flag
	root.PersistentFlags().StringVar(&config.VaultOverride, "vault", "", "Vault name or path (overrides auto-detect)")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage hook extensions",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			plugins := hooks.LoadPlugins()
			if len(plugins) == 0 {
				pluginsPath := filepath.Join(config.VaultPath(), ".same", "plugins.json")
				fmt.Printf("No plugins registered.\n")
				fmt.Printf("Create %s to add custom hooks.\n\n", pluginsPath)
				fmt.Println("Example plugins.json:")
				fmt.Println(`{
  "plugins": [
    {
      "name": "my-custom-hook",
      "event": "UserPromptSubmit",
      "command": "/path/to/script.sh",
      "args": [],
      "timeout_ms": 5000,
      "enabled": true
    }
  ]
}`)
				return nil
			}
			fmt.Println("Registered plugins:")
			for _, p := range plugins {
				status := "enabled"
				if !p.Enabled {
					status = "disabled"
				}
				fmt.Printf("  %-20s  event=%-20s  %s  %s %s\n",
					p.Name, p.Event, status, p.Command, strings.Join(p.Args, " "))
			}
			return nil
		},
	})

	return cmd
}

func watchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Watch vault for changes and auto-reindex",
		Long:  "Monitor the vault filesystem for markdown file changes. Automatically reindexes modified, created, or deleted notes with a 2-second debounce.",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			return watcher.Watch(db)
		},
	}
}

func vaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage vault registrations",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered vaults",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := config.LoadRegistry()
			if len(reg.Vaults) == 0 {
				fmt.Println("No vaults registered. Use 'same vault add <name> <path>' to register one.")
				fmt.Printf("Current vault (auto-detected): %s\n", config.VaultPath())
				return nil
			}
			fmt.Println("Registered vaults:")
			for name, path := range reg.Vaults {
				marker := "  "
				if name == reg.Default {
					marker = "* "
				}
				fmt.Printf("  %s%-15s %s\n", marker, name, path)
			}
			if reg.Default != "" {
				fmt.Printf("\n  (* = default)\n")
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "add [name] [path]",
		Short: "Register a vault",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := args[0], args[1]
			absPath, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			if info, err := os.Stat(absPath); err != nil || !info.IsDir() {
				return fmt.Errorf("path does not exist or is not a directory: %s", absPath)
			}
			reg := config.LoadRegistry()
			reg.Vaults[name] = absPath
			if len(reg.Vaults) == 1 {
				reg.Default = name
			}
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Registered vault %q at %s\n", name, absPath)
			if reg.Default == name {
				fmt.Println("Set as default vault.")
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "remove [name]",
		Short: "Unregister a vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			reg := config.LoadRegistry()
			if _, ok := reg.Vaults[name]; !ok {
				return fmt.Errorf("vault %q not registered", name)
			}
			delete(reg.Vaults, name)
			if reg.Default == name {
				reg.Default = ""
			}
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Removed vault %q\n", name)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "default [name]",
		Short: "Set the default vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			reg := config.LoadRegistry()
			if _, ok := reg.Vaults[name]; !ok {
				return fmt.Errorf("vault %q not registered", name)
			}
			reg.Default = name
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Default vault set to %q (%s)\n", name, reg.Vaults[name])
			return nil
		},
	})

	return cmd
}

// ---------- init ----------

func initCmd() *cobra.Command {
	var (
		yes     bool
		mcpOnly bool
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up SAME for your project (start here)",
		Long: `The setup wizard walks you through connecting SAME to your project.

What it does:
  1. Checks that Ollama is running (needed for local AI processing)
  2. Finds your notes/markdown files
  3. Indexes them so your AI can search them
  4. Connects to your AI tools (Claude, Cursor, etc.)

Run this command from inside your project folder.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return setup.RunInit(setup.InitOptions{
				Yes:     yes,
				MCPOnly: mcpOnly,
				Verbose: verbose,
				Version: Version,
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Accept all defaults without prompting")
	cmd.Flags().BoolVar(&mcpOnly, "mcp-only", false, "Skip hooks setup (for Cursor/Windsurf users)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show each file being processed")
	return cmd
}


func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}


func formatRelevance(score float64) string {
	// score is 0-1, higher is better
	pct := int(score * 100)
	stars := int(score * 5)
	if stars > 5 {
		stars = 5
	}
	if stars < 1 {
		stars = 1
	}
	filled := strings.Repeat("★", stars)
	empty := strings.Repeat("☆", 5-stars)
	return fmt.Sprintf("%s%s %d%%", filled, empty, pct)
}

// ---------- log ----------

// ---------- repair ----------

func repairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repair",
		Short: "Back up and rebuild the database",
		Long: `Creates a backup of same.db and force-rebuilds the index.

This is the go-to command when something seems broken. It:
  1. Copies same.db to same.db.bak
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
	if err := runReindex(true); err != nil {
		return fmt.Errorf("reindex failed: %w", err)
	}

	fmt.Println()
	fmt.Printf("  %s✓%s Repair complete.\n", cli.Green, cli.Reset)
	fmt.Printf("  Run %ssame doctor%s to verify.\n", cli.Bold, cli.Reset)
	fmt.Printf("\n  Backup saved to %s — delete after verifying repair.\n", cli.ShortenHome(bakPath))
	cli.Footer()
	return nil
}

// ---------- feedback ----------

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
		path       string
		title      string
		confidence float64
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

// ---------- pin ----------

func pinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pin",
		Short: "Always include a note in every session",
		Long: `Pin important notes so they're always included when your AI starts a session.

Pinned notes are injected every time, regardless of what you're working on.
Use this for architecture decisions, coding standards, or project context
that your AI should always know about.

  same pin path/to/note.md      Pin a note
  same pin list                 Show all pinned notes
  same pin remove path/to/note  Unpin a note`,
	}

	cmd.AddCommand(pinAddCmd())
	cmd.AddCommand(pinListCmd())
	cmd.AddCommand(pinRemoveCmd())

	// Allow `same pin <path>` as shorthand for `same pin add <path>`
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return runPinAdd(args[0])
		}
		return cmd.Help()
	}
	// Accept arbitrary args so `same pin path/to/note.md` works
	cmd.Args = cobra.ArbitraryArgs

	return cmd
}

func pinAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [path]",
		Short: "Pin a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPinAdd(args[0])
		},
	}
}

func pinListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show all pinned notes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPinList()
		},
	}
}

func pinRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [path]",
		Short: "Unpin a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPinRemove(args[0])
		},
	}
}

func runPinAdd(path string) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// Check if note exists in the index
	notes, err := db.GetNoteByPath(path)
	if err != nil || len(notes) == 0 {
		return fmt.Errorf("note not found in index: %s\n  Make sure the path is relative to your vault root", path)
	}

	already, _ := db.IsPinned(path)
	if already {
		fmt.Printf("  Already pinned: %s\n", path)
		return nil
	}

	if err := db.PinNote(path); err != nil {
		return fmt.Errorf("pin note: %w", err)
	}

	fmt.Printf("  %s✓%s Pinned: %s\n", cli.Green, cli.Reset, notes[0].Title)
	fmt.Printf("    %sThis note will be included in every session%s\n", cli.Dim, cli.Reset)
	return nil
}

func runPinList() error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	paths, err := db.GetPinnedPaths()
	if err != nil {
		return fmt.Errorf("get pinned notes: %w", err)
	}

	if len(paths) == 0 {
		fmt.Println("  No pinned notes.")
		fmt.Printf("  %sPin a note with: same pin path/to/note.md%s\n", cli.Dim, cli.Reset)
		return nil
	}

	fmt.Printf("  %sPinned notes%s (always included in sessions):\n\n", cli.Bold, cli.Reset)
	for _, p := range paths {
		notes, _ := db.GetNoteByPath(p)
		title := p
		if len(notes) > 0 {
			title = notes[0].Title
		}
		fmt.Printf("    %s %s\n", title, cli.Dim+p+cli.Reset)
	}
	fmt.Printf("\n  %d pinned note(s).\n", len(paths))
	return nil
}

func runPinRemove(path string) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	if err := db.UnpinNote(path); err != nil {
		return err
	}

	fmt.Printf("  %s✓%s Unpinned: %s\n", cli.Green, cli.Reset, path)
	return nil
}

// ---------- demo ----------

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

// ---------- tutorial ----------

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

Available lessons: search, decisions, pin, privacy, ask, handoff`,
		ValidArgs: []string{"search", "decisions", "pin", "privacy", "ask", "handoff"},
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
	hasVec    bool   // true if Ollama is available for embeddings
	origVault string // saved VaultOverride to restore on close
}

func newTutorialState() (*tutorialState, error) {
	dir, err := os.MkdirTemp("", "same-tutorial-")
	if err != nil {
		return nil, fmt.Errorf("create tutorial directory: %w", err)
	}

	sameDir := filepath.Join(dir, ".same", "data")
	if err := os.MkdirAll(sameDir, 0o755); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}

	// Save and override vault path
	origVault := config.VaultOverride
	config.VaultOverride = dir

	dbPath := filepath.Join(sameDir, "vault.db")
	db, err := store.OpenPath(dbPath)
	if err != nil {
		config.VaultOverride = origVault
		os.RemoveAll(dir)
		return nil, fmt.Errorf("open database: %w", err)
	}

	return &tutorialState{dir: dir, db: db, dbPath: dbPath, origVault: origVault}, nil
}

func (ts *tutorialState) close() {
	ts.db.Close()
	config.VaultOverride = ts.origVault
	os.RemoveAll(ts.dir)
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
	fmt.Printf("  6 lessons, ~5 minutes. You can also run individual lessons:\n")
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
	return fmt.Errorf("unknown lesson: %s (available: search, decisions, pin, privacy, ask, handoff)", name)
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
		fmt.Printf("  %sInstall Ollama for semantic search that understands meaning.%s\n", cli.Dim, cli.Reset)
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
	os.MkdirAll(filepath.Join(ts.dir, "_PRIVATE"), 0o755)
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

	// Try to actually answer with Ollama
	llm, llmErr := ollama.NewClient()
	if llmErr == nil {
		bestModel, _ := llm.PickBestModel()
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
				answer, err := llm.Generate(bestModel, prompt)
				if err == nil && answer != "" {
					for _, line := range strings.Split(answer, "\n") {
						fmt.Printf("  %s\n", line)
					}
					fmt.Printf("\n  %s✓%s Answer sourced from your notes. 100%% local.\n", cli.Green, cli.Reset)
					return nil
				}
			}
		}
	}

	// Ollama not available
	fmt.Printf("  %sOllama not running — 'same ask' requires a local LLM.%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  Install Ollama (https://ollama.ai) and a chat model:\n")
	fmt.Printf("    %s$%s ollama pull llama3.2\n", cli.Dim, cli.Reset)
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

// ---------- error helpers ----------

type sameError struct {
	message string
	hint    string
}

func (e *sameError) Error() string {
	return fmt.Sprintf("%s\n  Hint: %s", e.message, e.hint)
}

func userError(message, hint string) error {
	return &sameError{message: message, hint: hint}
}

