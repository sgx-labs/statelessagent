// Package main is the entrypoint for the SAME CLI.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/hooks"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	mcpserver "github.com/sgx-labs/statelessagent/internal/mcp"
	memory "github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/setup"
	"github.com/sgx-labs/statelessagent/internal/store"
	"github.com/sgx-labs/statelessagent/internal/watcher"
)

// Version is set at build time via ldflags.
var Version = "dev"

func main() {
	root := &cobra.Command{
		Use:   "same",
		Short: "Stateless Agent Memory Engine",
		Long:  "SAME — semantic search, context surfacing, and memory for markdown knowledge bases.",
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	root.AddCommand(initCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(reindexCmd())
	root.AddCommand(statsCmd())
	root.AddCommand(migrateCmd())
	root.AddCommand(hookCmd())
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

	// Global --vault flag
	root.PersistentFlags().StringVar(&config.VaultOverride, "vault", "", "Vault name or path (overrides auto-detect)")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the SAME version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return runVersionCheck()
			}
			fmt.Printf("same %s\n", Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Check for updates against GitHub releases")
	return cmd
}

func runVersionCheck() error {
	if Version == "dev" {
		fmt.Println("same dev (built from source, no version check)")
		return nil
	}

	// Fetch latest release tag from GitHub API (no auth needed for public repos)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sgx-labs/statelessagent/releases/latest")
	if err != nil {
		// Network error — silently succeed (don't block hooks)
		fmt.Printf("same %s (update check failed: %v)\n", Version, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		// No releases yet or API issue
		fmt.Printf("same %s (no releases found)\n", Version)
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("same %s\n", Version)
		return nil
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		fmt.Printf("same %s\n", Version)
		return nil
	}

	latestVer := strings.TrimPrefix(release.TagName, "v")
	currentVer := strings.TrimPrefix(Version, "v")

	if latestVer != currentVer && latestVer > currentVer {
		// Output as hook-compatible JSON for SessionStart hook
		fmt.Printf(`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"\n\n**SAME update available:** %s → %s\nRun: same version --check\nInstall: curl -fsSL https://raw.githubusercontent.com/sgx-labs/statelessagent/main/install.sh | bash\n\n"}}`, currentVer, latestVer)
		fmt.Println()
	}

	return nil
}

func reindexCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Index vault into SQLite",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReindex(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Re-embed all files regardless of changes")
	return cmd
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show index statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStats()
		},
	}
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Re-index from markdown (drops old data)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReindex(true)
		},
	}
}

func hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Run a hook handler",
	}
	cmd.AddCommand(hookSubCmd("context-surfacing", "UserPromptSubmit hook: surface relevant vault context"))
	cmd.AddCommand(hookSubCmd("decision-extractor", "Stop hook: extract decisions from transcript"))
	cmd.AddCommand(hookSubCmd("handoff-generator", "PreCompact/Stop hook: generate handoff notes"))
	cmd.AddCommand(hookSubCmd("staleness-check", "SessionStart hook: surface stale notes"))
	cmd.AddCommand(hookSubCmd("session-bootstrap", "SessionStart hook: bootstrap session with handoff + decisions + stale notes"))
	return cmd
}

func hookSubCmd(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			hooks.Run(name)
			return nil
		},
	}
}

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP stdio server",
		RunE: func(cmd *cobra.Command, args []string) error {
			mcpserver.Version = Version
			return mcpserver.Serve()
		},
	}
}

func runReindex(force bool) error {
	db, err := store.Open()
	if err != nil {
		return userError("Cannot open SAME database",
			"Run 'same init' to set up, or check VAULT_PATH")
	}
	defer db.Close()

	stats, err := indexer.Reindex(db, force)
	if err != nil {
		return err
	}

	data, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(data))
	return nil
}

func runStats() error {
	db, err := store.Open()
	if err != nil {
		return userError("Cannot open SAME database",
			"Run 'same init' to set up, or 'same doctor' to diagnose")
	}
	defer db.Close()

	stats := indexer.GetStats(db)
	data, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(data))
	return nil
}

func runEvalExport(strategy, query string, topK int, weights map[string]float64) error {
	if query == "" {
		return fmt.Errorf("--query is required")
	}

	db, err := store.Open()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	client := embedding.NewClient()
	queryVec, err := client.GetQueryEmbedding(query)
	if err != nil {
		return fmt.Errorf("embed query: %w", err)
	}

	var results interface{}

	if strategy == "vanilla" {
		searchResults, err := db.VectorSearch(queryVec, store.SearchOptions{TopK: topK})
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		results = searchResults
	} else {
		// Composite scoring strategies use VectorSearchRaw
		rw := weights["relevance_weight"]
		recW := weights["recency_weight"]
		confW := weights["confidence_weight"]

		rawResults, err := db.VectorSearchRaw(queryVec, topK*5)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}

		// Deduplicate by path (keep best chunk per note)
		type scoredResult struct {
			store.SearchResult
			CompositeScore float64 `json:"composite_score"`
		}

		seen := make(map[string]bool)
		type dedupedRaw struct {
			store.RawSearchResult
		}
		var deduped []store.RawSearchResult
		for _, r := range rawResults {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			deduped = append(deduped, r)
		}

		// Min-max normalize distances within result set (matches Python)
		minDist := 0.0
		maxDist := 0.0
		if len(deduped) > 0 {
			minDist = deduped[0].Distance
			maxDist = deduped[0].Distance
			for _, r := range deduped[1:] {
				if r.Distance < minDist {
					minDist = r.Distance
				}
				if r.Distance > maxDist {
					maxDist = r.Distance
				}
			}
		}
		distRange := maxDist - minDist
		if distRange <= 0 {
			distRange = 1.0
		}

		var scored []scoredResult
		for _, r := range deduped {
			semanticScore := 1.0 - ((r.Distance - minDist) / distRange)

			composite := memory.CompositeScore(
				semanticScore, r.Modified, r.Confidence, r.ContentType,
				rw, recW, confW,
			)

			snippet := r.Text
			if len(snippet) > 500 {
				snippet = snippet[:500]
			}

			scored = append(scored, scoredResult{
				SearchResult: store.SearchResult{
					Path:         r.Path,
					Title:        r.Title,
					ChunkHeading: r.Heading,
					Score:        round3(semanticScore),
					Distance:     round1(r.Distance),
					Snippet:      snippet,
					Domain:       r.Domain,
					Workstream:   r.Workstream,
					Tags:         r.Tags,
					ContentType:  r.ContentType,
					Confidence:   round3(r.Confidence),
				},
				CompositeScore: composite,
			})
		}

		// Sort by composite score descending
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].CompositeScore > scored[j].CompositeScore
		})

		if len(scored) > topK {
			scored = scored[:topK]
		}
		results = scored
	}

	output := map[string]interface{}{
		"strategy": strategy,
		"query":    query,
		"top_k":    topK,
		"results":  results,
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
	return nil
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func searchCmd() *cobra.Command {
	var (
		topK     int
		domain   string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the vault from the command line",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			return runSearch(query, topK, domain, jsonOut)
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 5, "Number of results")
	cmd.Flags().StringVar(&domain, "domain", "", "Filter by domain")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runSearch(query string, topK int, domain string, jsonOut bool) error {
	db, err := store.Open()
	if err != nil {
		return userError("Cannot open SAME database",
			"Run 'same init' to set up, or 'same doctor' to diagnose")
	}
	defer db.Close()

	client := embedding.NewClient()
	queryVec, err := client.GetQueryEmbedding(query)
	if err != nil {
		return fmt.Errorf("embed query: %w", err)
	}

	results, err := db.VectorSearch(queryVec, store.SearchOptions{
		TopK:   topK,
		Domain: domain,
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, r := range results {
		typeTag := ""
		if r.ContentType != "" && r.ContentType != "note" {
			typeTag = fmt.Sprintf(" [%s]", r.ContentType)
		}

		fmt.Printf("\n%d. %s%s\n", i+1, r.Title, typeTag)
		fmt.Printf("   %s\n", r.Path)
		fmt.Printf("   Score: %.3f  Distance: %.1f  Confidence: %.3f\n", r.Score, r.Distance, r.Confidence)

		// Show first 150 chars of snippet
		snippet := r.Snippet
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		// Replace newlines with spaces for compact display
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", "")
		fmt.Printf("   %s\n", snippet)
	}
	fmt.Println()

	return nil
}

func relatedCmd() *cobra.Command {
	var (
		topK    int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "related [note-path]",
		Short: "Find notes semantically similar to a given note",
		Long:  "Find notes related to a specific vault note using its stored embedding. Path is relative to vault root.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelated(args[0], topK, jsonOut)
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 5, "Number of related notes to show")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runRelated(notePath string, topK int, jsonOut bool) error {
	db, err := store.Open()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Get the stored embedding for this note
	noteVec, err := db.GetNoteEmbedding(notePath)
	if err != nil {
		return fmt.Errorf("get embedding: %w", err)
	}
	if noteVec == nil {
		return fmt.Errorf("note not found in index: %s", notePath)
	}

	// Search for similar notes, requesting extra to filter out the source note
	results, err := db.VectorSearch(noteVec, store.SearchOptions{
		TopK: topK + 3,
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	// Filter out the source note itself
	var filtered []store.SearchResult
	for _, r := range results {
		if r.Path != notePath {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) > topK {
		filtered = filtered[:topK]
	}

	if jsonOut {
		data, _ := json.MarshalIndent(filtered, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(filtered) == 0 {
		fmt.Println("No related notes found.")
		return nil
	}

	fmt.Printf("\nNotes related to: %s\n", notePath)
	for i, r := range filtered {
		typeTag := ""
		if r.ContentType != "" && r.ContentType != "note" {
			typeTag = fmt.Sprintf(" [%s]", r.ContentType)
		}

		fmt.Printf("\n%d. %s%s\n", i+1, r.Title, typeTag)
		fmt.Printf("   %s\n", r.Path)
		fmt.Printf("   Score: %.3f  Distance: %.1f\n", r.Score, r.Distance)

		snippet := r.Snippet
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", "")
		fmt.Printf("   %s\n", snippet)
	}
	fmt.Println()

	return nil
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check system health: vault, database, Ollama, hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

func runDoctor() error {
	passed := 0
	failed := 0

	check := func(name string, hint string, fn func() (string, error)) {
		detail, err := fn()
		if err != nil {
			fmt.Printf("  %s✗%s %s: %s\n",
				cli.Red, cli.Reset, name, err)
			if hint != "" {
				fmt.Printf("    → %s\n", hint)
			}
			failed++
		} else {
			if detail != "" {
				fmt.Printf("  %s✓%s %s (%s)\n",
					cli.Green, cli.Reset, name, detail)
			} else {
				fmt.Printf("  %s✓%s %s\n",
					cli.Green, cli.Reset, name)
			}
			passed++
		}
	}

	fmt.Printf("\n%sSAME Health Check%s\n\n", cli.Bold, cli.Reset)

	// 1. Vault path
	check("Vault path", "run 'same init' or set VAULT_PATH", func() (string, error) {
		vp := config.VaultPath()
		if vp == "" {
			return "", fmt.Errorf("no vault found")
		}
		info, err := os.Stat(vp)
		if err != nil {
			return "", fmt.Errorf("path does not exist")
		}
		if !info.IsDir() {
			return "", fmt.Errorf("not a directory")
		}
		return "", nil
	})

	// 2. Database
	check("Database", "run 'same init' or 'same reindex'", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		noteCount, err := db.NoteCount()
		if err != nil {
			return "", fmt.Errorf("cannot query")
		}
		chunkCount, err := db.ChunkCount()
		if err != nil {
			return "", fmt.Errorf("cannot query")
		}
		if noteCount == 0 {
			return "", fmt.Errorf("empty")
		}
		return fmt.Sprintf("%s notes, %s chunks",
			cli.FormatNumber(noteCount),
			cli.FormatNumber(chunkCount)), nil
	})

	// 3. Ollama
	check("Ollama", "start with 'ollama serve'", func() (string, error) {
		embedClient := embedding.NewClient()
		vec, err := embedClient.GetQueryEmbedding("test")
		if err != nil {
			return "", fmt.Errorf("connection refused")
		}
		return fmt.Sprintf("%d-dim embeddings", len(vec)), nil
	})

	// 4. Vector search
	check("Vector search", "run 'same reindex'", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", err
		}
		defer db.Close()

		embedClient := embedding.NewClient()
		vec, err := embedClient.GetQueryEmbedding("test query")
		if err != nil {
			return "", fmt.Errorf("embedding failed")
		}

		results, err := db.VectorSearch(vec, store.SearchOptions{TopK: 1})
		if err != nil {
			return "", fmt.Errorf("search failed")
		}
		if len(results) == 0 {
			return "", fmt.Errorf("no results")
		}
		return "", nil
	})

	// 5. Context surfacing
	check("Context surfacing", "'same search <query>' to test", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", err
		}
		defer db.Close()

		embedClient := embedding.NewClient()
		vec, err := embedClient.GetQueryEmbedding("what notes are in this vault")
		if err != nil {
			return "", fmt.Errorf("embedding failed")
		}

		raw, err := db.VectorSearchRaw(vec, 3)
		if err != nil {
			return "", fmt.Errorf("raw search failed")
		}
		if len(raw) == 0 {
			return "", fmt.Errorf("no results")
		}
		return "", nil
	})

	// 6. Private content excluded
	check("Private content excluded", "'same reindex --force' to purge", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", err
		}
		defer db.Close()

		var count int
		err = db.Conn().QueryRow("SELECT COUNT(*) FROM vault_notes WHERE path LIKE '_PRIVATE/%'").Scan(&count)
		if err != nil {
			return "", nil
		}
		if count > 0 {
			return "", fmt.Errorf("%d _PRIVATE/ entries in index", count)
		}
		return "", nil
	})

	// 7. Ollama localhost only
	check("Ollama localhost only", "set OLLAMA_URL to localhost", func() (string, error) {
		url := config.OllamaURL()
		if !strings.Contains(url, "localhost") && !strings.Contains(url, "127.0.0.1") && !strings.Contains(url, "::1") {
			return "", fmt.Errorf("non-localhost: %s", url)
		}
		return "", nil
	})

	fmt.Printf("\n  %d passed, %d failed\n\n", passed, failed)

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage hook plugins",
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

func benchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bench",
		Short: "Run performance benchmarks",
		Long:  "Measure cold-start, search, embedding, and database performance.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBench()
		},
	}
}

type benchResult struct {
	Name    string `json:"name"`
	Latency string `json:"latency_ms"`
	Detail  string `json:"detail,omitempty"`
}

func runBench() error {
	fmt.Println("SAME Performance Benchmark")
	fmt.Println("==========================")
	fmt.Println()

	var results []benchResult

	// 1. Database open (cold start)
	t0 := time.Now()
	db, err := store.Open()
	dbOpen := time.Since(t0)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	results = append(results, benchResult{
		Name:    "DB open (cold start)",
		Latency: fmt.Sprintf("%.1f", float64(dbOpen.Microseconds())/1000.0),
		Detail:  fmt.Sprintf("%d notes, %d chunks", noteCount, chunkCount),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 2. Embedding latency (single query)
	client := embedding.NewClient()
	testQuery := "what decisions were made about the memory system architecture"
	t0 = time.Now()
	queryVec, err := client.GetQueryEmbedding(testQuery)
	embedLatency := time.Since(t0)
	if err != nil {
		results = append(results, benchResult{
			Name:    "Embedding (Ollama)",
			Latency: "FAILED",
			Detail:  err.Error(),
		})
		fmt.Printf("  %-30s %8s     %s\n", "Embedding (Ollama)", "FAILED", err.Error())
	} else {
		results = append(results, benchResult{
			Name:    "Embedding (Ollama)",
			Latency: fmt.Sprintf("%.1f", float64(embedLatency.Microseconds())/1000.0),
			Detail:  fmt.Sprintf("%d dimensions", len(queryVec)),
		})
		fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)
	}

	if queryVec == nil {
		fmt.Println("\n  Skipping search benchmarks (embedding failed).")
		printBenchSummary(results)
		return nil
	}

	// 3. Vector search (vanilla, KNN only)
	t0 = time.Now()
	searchResults, err := db.VectorSearch(queryVec, store.SearchOptions{TopK: 10})
	searchLatency := time.Since(t0)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	results = append(results, benchResult{
		Name:    "Vector search (top-10)",
		Latency: fmt.Sprintf("%.1f", float64(searchLatency.Microseconds())/1000.0),
		Detail:  fmt.Sprintf("%d results", len(searchResults)),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 4. Raw search + composite scoring
	t0 = time.Now()
	rawResults, _ := db.VectorSearchRaw(queryVec, 50)
	_ = rawResults
	rawSearchLatency := time.Since(t0)
	results = append(results, benchResult{
		Name:    "Raw search (top-50)",
		Latency: fmt.Sprintf("%.1f", float64(rawSearchLatency.Microseconds())/1000.0),
		Detail:  fmt.Sprintf("%d raw results", len(rawResults)),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 5. Composite scoring (CPU only, no I/O)
	t0 = time.Now()
	for i := 0; i < 1000; i++ {
		for _, r := range rawResults {
			memory.CompositeScore(0.8, r.Modified, r.Confidence, r.ContentType, 0.5, 0.4, 0.1)
		}
	}
	compositeDur := time.Since(t0)
	opsPerSec := float64(1000*len(rawResults)) / compositeDur.Seconds()
	results = append(results, benchResult{
		Name:    "Composite scoring",
		Latency: fmt.Sprintf("%.3f", float64(compositeDur.Microseconds())/1000.0/1000.0),
		Detail:  fmt.Sprintf("%.0f scores/sec (1000 x %d)", opsPerSec, len(rawResults)),
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	// 6. End-to-end: embed + search + score (what a hook actually does)
	t0 = time.Now()
	vec2, _ := client.GetQueryEmbedding("recent session handoffs and decisions")
	raw2, _ := db.VectorSearchRaw(vec2, 12)
	for _, r := range raw2 {
		memory.CompositeScore(0.8, r.Modified, r.Confidence, r.ContentType, 0.5, 0.4, 0.1)
	}
	e2eLatency := time.Since(t0)
	results = append(results, benchResult{
		Name:    "End-to-end (hook sim)",
		Latency: fmt.Sprintf("%.1f", float64(e2eLatency.Microseconds())/1000.0),
		Detail:  "embed + search + score",
	})
	fmt.Printf("  %-30s %8s ms  %s\n", results[len(results)-1].Name, results[len(results)-1].Latency, results[len(results)-1].Detail)

	printBenchSummary(results)

	// Output JSON for programmatic consumption
	data, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println("\n" + string(data))
	return nil
}

func printBenchSummary(results []benchResult) {
	fmt.Println()
	fmt.Println("Summary:")

	// Find the embed and search latencies to calculate overhead
	var embedMs, searchMs, e2eMs float64
	for _, r := range results {
		var v float64
		fmt.Sscanf(r.Latency, "%f", &v)
		switch r.Name {
		case "Embedding (Ollama)":
			embedMs = v
		case "Vector search (top-10)":
			searchMs = v
		case "End-to-end (hook sim)":
			e2eMs = v
		}
	}

	if embedMs > 0 && searchMs > 0 {
		goOverhead := e2eMs - embedMs
		fmt.Printf("  Ollama embedding: %.0fms (network I/O, dominates latency)\n", embedMs)
		fmt.Printf("  Go overhead:      %.1fms (search + scoring + I/O)\n", goOverhead)
		fmt.Printf("  Total e2e:        %.0fms\n", e2eMs)
	}
}

// ---------- init ----------

func initCmd() *cobra.Command {
	var (
		yes     bool
		mcpOnly bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive setup wizard — get SAME running in one command",
		Long:  "Checks Ollama, detects your notes, indexes them, and configures Claude Code hooks and MCP.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setup.RunInit(setup.InitOptions{
				Yes:     yes,
				MCPOnly: mcpOnly,
				Version: Version,
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Accept all defaults without prompting")
	cmd.Flags().BoolVar(&mcpOnly, "mcp-only", false, "Skip hooks setup (for Cursor/Windsurf users)")
	return cmd
}

// ---------- status ----------

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show SAME status at a glance",
		Long:  "Like 'git status' — shows vault info, index state, Ollama, hooks, MCP, and last session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}
}

func runStatus() error {
	vp := config.VaultPath()
	if vp == "" {
		return userError("No vault found",
			"Run 'same init' to set up, or set VAULT_PATH")
	}

	fmt.Printf("\n%sSAME Status%s\n\n", cli.Bold, cli.Reset)

	fmt.Printf("  Vault:   %s\n", cli.ShortenHome(vp))

	db, err := store.Open()
	if err != nil {
		fmt.Printf("  DB:      %snot initialized%s\n\n",
			cli.Red, cli.Reset)
		fmt.Printf("  Run 'same init' to set up.\n\n")
		return nil
	}
	defer db.Close()

	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	fmt.Printf("  Notes:   %s indexed\n", cli.FormatNumber(noteCount))
	fmt.Printf("  Chunks:  %s\n", cli.FormatNumber(chunkCount))

	// Index age
	indexAge, _ := db.IndexAge()
	if indexAge > 0 {
		fmt.Printf("  Indexed: %s ago\n", formatDuration(indexAge))
	}

	// DB size
	dbPath := config.DBPath()
	if info, err := os.Stat(dbPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		fmt.Printf("  DB:      %.1f MB\n", sizeMB)
	}

	// Ollama (same line block, no extra blank line)
	httpClient := &http.Client{Timeout: time.Second}
	ollamaURL := config.OllamaURL()
	resp, err := httpClient.Get(ollamaURL + "/api/tags")
	if err != nil {
		fmt.Printf("  Ollama:  %snot running%s\n",
			cli.Red, cli.Reset)
	} else {
		resp.Body.Close()
		fmt.Printf("  Ollama:  %srunning%s (%s)\n",
			cli.Green, cli.Reset, config.EmbeddingModel)
	}

	// Hooks
	fmt.Printf("\n  Hooks:\n")
	hookStatus := setup.HooksInstalled(vp)
	hookNames := []string{
		"context-surfacing",
		"decision-extractor",
		"handoff-generator",
		"staleness-check",
	}
	for _, name := range hookNames {
		if hookStatus[name] {
			fmt.Printf("    %-22s %s✓ active%s\n",
				name, cli.Green, cli.Reset)
		} else {
			fmt.Printf("    %-22s %s- not configured%s\n",
				name, cli.Dim, cli.Reset)
		}
	}

	// MCP
	if setup.MCPInstalled(vp) {
		fmt.Printf("\n  MCP: registered\n")
	} else {
		fmt.Printf("\n  MCP: %snot registered%s\n",
			cli.Dim, cli.Reset)
	}

	fmt.Println()
	return nil
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

// ---------- log ----------

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
		return userError("Cannot open SAME database",
			"Run 'same init' to set up, or 'same doctor' to diagnose")
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

// ---------- config ----------

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage SAME configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(config.ShowConfig())
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print path to config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return fmt.Errorf("no vault found — run 'same init' or set VAULT_PATH")
			}
			fmt.Println(config.ConfigFilePath(vp))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "edit",
		Short: "Open config file in $EDITOR",
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return fmt.Errorf("no vault found — run 'same init' or set VAULT_PATH")
			}
			configPath := config.ConfigFilePath(vp)
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				fmt.Println("No config file found. Generating default...")
				if err := config.GenerateConfig(vp); err != nil {
					return err
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			fmt.Printf("Opening %s in %s...\n", configPath, editor)
			return runEditor(editor, configPath)
		},
	})

	return cmd
}

func runEditor(editor, path string) error {
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---------- setup ----------

func setupSubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up integrations (hooks, MCP)",
	}

	var removeHooks bool
	hooksCmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install or remove Claude Code hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return userError("No vault found",
					"Run 'same init' to set up, or set VAULT_PATH")
			}
			if removeHooks {
				return setup.RemoveHooks(vp)
			}
			return setup.SetupHooks(vp)
		},
	}
	hooksCmd.Flags().BoolVar(&removeHooks, "remove", false, "Remove SAME hooks")
	cmd.AddCommand(hooksCmd)

	var removeMCP bool
	mcpSetupCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Register or remove SAME MCP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			vp := config.VaultPath()
			if vp == "" {
				return userError("No vault found",
					"Run 'same init' to set up, or set VAULT_PATH")
			}
			if removeMCP {
				return setup.RemoveMCP(vp)
			}
			if err := setup.SetupMCP(vp); err != nil {
				return err
			}
			fmt.Println("\n  Available MCP tools:")
			fmt.Println("    search_notes          Semantic search")
			fmt.Println("    search_notes_filtered Search with filters")
			fmt.Println("    get_note              Read a note by path")
			fmt.Println("    find_similar_notes    Find related notes")
			fmt.Println("    reindex               Re-index the vault")
			fmt.Println("    index_stats           Index statistics")
			return nil
		},
	}
	mcpSetupCmd.Flags().BoolVar(&removeMCP, "remove", false, "Remove SAME MCP server")
	cmd.AddCommand(mcpSetupCmd)

	return cmd
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

