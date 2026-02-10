// Package main is the entrypoint for the SAME CLI.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

	// C3: Use semver comparison instead of string comparison
	if compareSemver(latestVer, currentVer) > 0 {
		// Output as hook-compatible JSON for SessionStart hook
		fmt.Printf(`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"\n\n**SAME update available:** %s → %s\nRun: same update\n\n"}}`, currentVer, latestVer)
		fmt.Println()
	}

	return nil
}

func updateCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update SAME to the latest version",
		Long: `Check for and install the latest version of SAME from GitHub releases.

This command will:
  1. Check the current version against GitHub releases
  2. Download the appropriate binary for your platform
  3. Replace the current binary with the new version

Example:
  same update          Check and install if newer version available
  same update --force  Force reinstall even if already on latest`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Force update even if already on latest version")
	return cmd
}

func runUpdate(force bool) error {
	cli.Header("SAME Update")
	fmt.Println()

	// Get current version
	currentVer := strings.TrimPrefix(Version, "v")
	fmt.Printf("  Current version: %s%s%s\n", cli.Bold, Version, cli.Reset)

	if Version == "dev" && !force {
		fmt.Printf("\n  %s⚠%s  Running dev build (built from source)\n", cli.Yellow, cli.Reset)
		fmt.Println("     Use --force to update anyway, or rebuild from source.")
		return nil
	}

	// Fetch latest release from GitHub
	fmt.Printf("  Checking GitHub releases...")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sgx-labs/statelessagent/releases/latest")
	if err != nil {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("parse release: %w", err)
	}

	latestVer := strings.TrimPrefix(release.TagName, "v")
	fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)
	fmt.Printf("  Latest version:  %s%s%s\n", cli.Bold, release.TagName, cli.Reset)

	// C3: Use semver comparison instead of string comparison
	cmp := compareSemver(latestVer, currentVer)
	if cmp == 0 && !force {
		fmt.Printf("\n  %s✓%s Already on the latest version.\n\n", cli.Green, cli.Reset)
		return nil
	}

	if cmp <= 0 && !force {
		fmt.Printf("\n  %s✓%s Already up to date.\n\n", cli.Green, cli.Reset)
		return nil
	}

	// Determine the asset to download
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	var assetName string
	switch {
	case goos == "darwin" && goarch == "arm64":
		assetName = "same-darwin-arm64"
	case goos == "darwin" && goarch == "amd64":
		assetName = "same-darwin-amd64"
	case goos == "linux" && goarch == "amd64":
		assetName = "same-linux-amd64"
	case goos == "windows" && goarch == "amd64":
		assetName = "same-windows-amd64.exe"
	default:
		return fmt.Errorf("unsupported platform: %s/%s", goos, goarch)
	}

	// Find the download URL
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no binary found for %s/%s in release %s", goos, goarch, release.TagName)
	}

	fmt.Printf("\n  Downloading %s...", assetName)

	// Download to temp file
	dlResp, err := client.Get(downloadURL)
	if err != nil {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("download: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("download returned %d", dlResp.StatusCode)
	}

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// Create temp file in same directory (for atomic rename)
	tmpFile, err := os.CreateTemp(filepath.Dir(execPath), "same-update-*")
	if err != nil {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Download the file
	_, err = io.Copy(tmpFile, dlResp.Body)
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("write file: %w", err)
	}

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("chmod: %w", err)
	}

	fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)

	// Replace the binary
	fmt.Printf("  Installing...")

	// On Windows, we need to rename the old binary first
	if goos == "windows" {
		oldPath := execPath + ".old"
		os.Remove(oldPath) // ignore error
		if err := os.Rename(execPath, oldPath); err != nil {
			os.Remove(tmpPath)
			fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
			return fmt.Errorf("backup old binary: %w", err)
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("install: %w", err)
	}

	fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)

	// Success message
	fmt.Println()
	fmt.Printf("  %s✓%s Updated to %s%s%s\n", cli.Green, cli.Reset, cli.Bold, release.TagName, cli.Reset)
	fmt.Println()
	fmt.Printf("  Run %ssame doctor%s to verify.\n", cli.Bold, cli.Reset)

	cli.Footer()
	return nil
}

func reindexCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Scan your notes and rebuild the search index",
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
		Short: "Show how many notes are indexed",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStats()
		},
	}
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Rebuild index from scratch (replaces old data)",
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
		Short: "Start the AI tool integration server (MCP)",
		RunE: func(cmd *cobra.Command, args []string) error {
			mcpserver.Version = Version
			return mcpserver.Serve()
		},
	}
}

func runReindex(force bool) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	indexer.Version = Version
	stats, err := indexer.Reindex(db, force)
	if err != nil {
		// If embedding provider failed, offer lite mode
		fmt.Fprintf(os.Stderr, "  Ollama not available — indexing with keyword search only.\n")
		fmt.Fprintf(os.Stderr, "  Start Ollama and run 'same reindex' again for semantic search.\n\n")
		stats, err = indexer.ReindexLite(db, force, nil)
		if err != nil {
			return err
		}
	}

	data, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(data))
	return nil
}

func runStats() error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	stats := indexer.GetStats(db)
	data, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(data))
	return nil
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
	if strings.TrimSpace(query) == "" {
		return userError("Empty search query", "Provide a search term: same search \"your query\"")
	}
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// Detect lite mode (no vectors) and fall back to FTS5
	var results []store.SearchResult
	if !db.HasVectors() {
		if db.FTSAvailable() {
			results, err = db.FTS5Search(query, store.SearchOptions{TopK: topK, Domain: domain})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
			if !jsonOut && len(results) > 0 {
				fmt.Printf("  %s(keyword search — install Ollama for semantic search)%s\n", cli.Dim, cli.Reset)
			}
		} else {
			return userError("No search index available", "run 'same reindex' with Ollama running for best results")
		}
	} else {
		client, err := newEmbedProvider()
		if err != nil {
			// Ollama went down — try FTS5 fallback
			if db.FTSAvailable() {
				results, err = db.FTS5Search(query, store.SearchOptions{TopK: topK, Domain: domain})
				if err != nil {
					return fmt.Errorf("search: %w", err)
				}
				if !jsonOut && len(results) > 0 {
					fmt.Printf("  %s(keyword fallback — Ollama not available)%s\n", cli.Dim, cli.Reset)
				}
			} else {
				return fmt.Errorf("can't connect to embedding provider — is Ollama running? (%w)", err)
			}
		} else {
			if mismatchErr := db.CheckEmbeddingMeta(client.Name(), client.Model(), client.Dimensions()); mismatchErr != nil {
				return mismatchErr
			}

			queryVec, err := client.GetQueryEmbedding(query)
			if err != nil {
				return fmt.Errorf("embed query: %w", err)
			}

			results, err = db.VectorSearch(queryVec, store.SearchOptions{
				TopK:   topK,
				Domain: domain,
			})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
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
		Short: "Find notes related to a given note",
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

	// Check for embedding model/dimension mismatch
	client, err := newEmbedProvider()
	if err != nil {
		return fmt.Errorf("embedding provider: %w", err)
	}
	if mismatchErr := db.CheckEmbeddingMeta(client.Name(), client.Model(), client.Dimensions()); mismatchErr != nil {
		return mismatchErr
	}

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
		Short: "Check system health and diagnose issues",
		Long:  "Runs health checks on your SAME setup: verifies Ollama is running, your notes are indexed, and search is working.",
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

	cli.Header("SAME Health Check")
	fmt.Println()

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

	// 2b. Index mode
	check("Index mode", "run 'same reindex' with Ollama for semantic search", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open database")
		}
		defer db.Close()
		if db.HasVectors() {
			return "semantic (Ollama embeddings)", nil
		}
		noteCount, _ := db.NoteCount()
		if noteCount > 0 {
			return "keyword-only (install Ollama + run 'same reindex' to upgrade)", nil
		}
		return "empty", nil
	})

	// 3. Embedding provider
	check("Ollama connection", "make sure Ollama is running (look for llama icon), or use keyword-only mode", func() (string, error) {
		embedClient, err := newEmbedProvider()
		if err != nil {
			return "", fmt.Errorf("not connected (keyword search still works)")
		}
		_, err = embedClient.GetQueryEmbedding("test")
		if err != nil {
			return "", fmt.Errorf("Ollama not responding - is it running?")
		}
		return fmt.Sprintf("connected via %s", embedClient.Name()), nil
	})

	// 4. Vector search
	check("Search working", "run 'same reindex' to rebuild", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", err
		}
		defer db.Close()

		embedClient, err := newEmbedProvider()
		if err != nil {
			return "", fmt.Errorf("provider error")
		}
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
	check("Finding relevant notes", "try 'same search <query>' to test", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", err
		}
		defer db.Close()

		embedClient, err := newEmbedProvider()
		if err != nil {
			return "", fmt.Errorf("provider error")
		}
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
	check("Private folders hidden", "'same reindex --force' to refresh", func() (string, error) {
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
	check("Data stays local", "Ollama should run on your computer, not a remote server", func() (string, error) {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			return "", err
		}
		if !strings.Contains(ollamaURL, "localhost") && !strings.Contains(ollamaURL, "127.0.0.1") && !strings.Contains(ollamaURL, "::1") {
			return "", fmt.Errorf("non-localhost: %s", ollamaURL)
		}
		return "", nil
	})

	// 8. Config file validity
	check("Config file", "check .same/config.toml for syntax errors", func() (string, error) {
		_, err := config.LoadConfig()
		if err != nil {
			return "", err
		}
		return "", nil
	})

	// 9. Hook installation
	check("Hooks installed", "run 'same init' or 'same setup hooks'", func() (string, error) {
		vp := config.VaultPath()
		if vp == "" {
			return "", fmt.Errorf("no vault to check")
		}
		settingsPath := filepath.Join(vp, ".claude", "settings.json")
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			return "", fmt.Errorf("no .claude/settings.json found")
		}
		hookStatus := setup.HooksInstalled(vp)
		activeCount := 0
		for _, v := range hookStatus {
			if v {
				activeCount++
			}
		}
		if activeCount == 0 {
			return "", fmt.Errorf("no SAME hooks found in settings")
		}
		return fmt.Sprintf("%d hooks active", activeCount), nil
	})

	// 10. Database integrity (orphaned chunks)
	check("Database integrity", "run 'same reindex' to rebuild", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		var orphaned int
		err = db.Conn().QueryRow(`
			SELECT COUNT(*) FROM vault_chunks c
			LEFT JOIN vault_notes n ON c.note_path = n.path AND c.chunk_id = n.chunk_id
			WHERE n.path IS NULL
		`).Scan(&orphaned)
		if err != nil {
			return "", nil // table may not exist yet, not an error
		}
		if orphaned > 0 {
			return "", fmt.Errorf("%d orphaned chunks", orphaned)
		}
		return "", nil
	})

	// 11. Index freshness
	check("Index freshness", "run 'same reindex' to update", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		age, err := db.IndexAge()
		if err != nil {
			return "", nil // no index yet
		}
		if age > 7*24*time.Hour {
			return "", fmt.Errorf("last indexed %s ago", formatDuration(age))
		}
		return fmt.Sprintf("last indexed %s ago", formatDuration(age)), nil
	})

	// 12. Log file size
	check("Log file size", "rotation keeps logs under 5MB automatically", func() (string, error) {
		logPath := filepath.Join(config.DataDir(), "verbose.log")
		info, err := os.Stat(logPath)
		if os.IsNotExist(err) {
			return "no log file", nil
		}
		if err != nil {
			return "", nil
		}
		sizeMB := float64(info.Size()) / (1024 * 1024)
		if sizeMB > 10 {
			return "", fmt.Errorf("verbose.log is %.1f MB", sizeMB)
		}
		return fmt.Sprintf("%.1f MB", sizeMB), nil
	})

	// 13. Embedding config
	check("Embedding config", "run 'same reindex --force' if model changed", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		embedClient, err := newEmbedProvider()
		if err != nil {
			return "", fmt.Errorf("cannot create provider: %v", err)
		}
		if mismatchErr := db.CheckEmbeddingMeta(embedClient.Name(), embedClient.Model(), embedClient.Dimensions()); mismatchErr != nil {
			return "", mismatchErr
		}
		provider, _ := db.GetMeta("embed_provider")
		dims, _ := db.GetMeta("embed_dims")
		if provider == "" {
			return "no metadata stored yet", nil
		}
		return fmt.Sprintf("%s, %s dims", provider, dims), nil
	})

	// 14. SQLite integrity (PRAGMA)
	check("SQLite integrity", "run 'same repair' to rebuild", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		return "", db.IntegrityCheck()
	})

	// 15. Retrieval utilization
	check("Retrieval utilization", "try different queries or adjust your profile", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		usage, err := db.GetRecentUsage(5)
		if err != nil || len(usage) == 0 {
			return "no usage data yet", nil
		}
		total := 0
		referenced := 0
		for _, u := range usage {
			total++
			if u.WasReferenced {
				referenced++
			}
		}
		rate := float64(referenced) / float64(total)
		detail := fmt.Sprintf("%.0f%% of injected context was used", rate*100)
		if rate < 0.20 {
			return fmt.Sprintf("%.0f%% — this improves as your AI references more notes", rate*100), nil
		}
		return detail, nil
	})

	cli.Box([]string{
		fmt.Sprintf("%d passed, %d failed", passed, failed),
	})

	cli.Footer()

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
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
		Short: "Run search performance benchmarks",
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
	client, provErr := newEmbedProvider()
	if provErr != nil {
		results = append(results, benchResult{
			Name:    "Embedding",
			Latency: "FAILED",
			Detail:  provErr.Error(),
		})
		fmt.Printf("  %-30s %8s     %s\n", "Embedding", "FAILED", provErr.Error())
		return nil
	}
	testQuery := "what decisions were made about the memory system architecture"
	t0 = time.Now()
	queryVec, err := client.GetQueryEmbedding(testQuery)
	embedLatency := time.Since(t0)
	embedLabel := fmt.Sprintf("Embedding (%s)", client.Name())
	if err != nil {
		results = append(results, benchResult{
			Name:    embedLabel,
			Latency: "FAILED",
			Detail:  err.Error(),
		})
		fmt.Printf("  %-30s %8s     %s\n", embedLabel, "FAILED", err.Error())
	} else {
		results = append(results, benchResult{
			Name:    embedLabel,
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
		switch {
		case strings.HasPrefix(r.Name, "Embedding"):
			embedMs = v
		case r.Name == "Vector search (top-10)":
			searchMs = v
		case r.Name == "End-to-end (hook sim)":
			e2eMs = v
		}
	}

	if embedMs > 0 && searchMs > 0 {
		goOverhead := e2eMs - embedMs
		fmt.Printf("  Embedding:        %.0fms (network I/O, dominates latency)\n", embedMs)
		fmt.Printf("  Go overhead:      %.1fms (search + scoring + I/O)\n", goOverhead)
		fmt.Printf("  Total e2e:        %.0fms\n", e2eMs)
	}
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

// ---------- status ----------

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "See what SAME is tracking in your project",
		Long: `Shows you the current state of SAME for your project:
  - How many notes are indexed
  - Whether Ollama is running
  - Which AI tool integrations are active

Run this anytime to see if SAME is working.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}
}

func runStatus() error {
	vp := config.VaultPath()
	if vp == "" {
		return config.ErrNoVault
	}

	cli.Header("SAME Status")

	cli.Section("Vault")
	fmt.Printf("  Path:    %s\n", cli.ShortenHome(vp))

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
	ollamaURL, ollamaErr := config.OllamaURL()
	if ollamaErr != nil {
		fmt.Printf("  Ollama:  %sinvalid URL%s (%v)\n",
			cli.Red, cli.Reset, ollamaErr)
	} else {
		httpClient := &http.Client{Timeout: time.Second}
		resp, err := httpClient.Get(ollamaURL + "/api/tags")
		if err != nil {
			fmt.Printf("  Ollama:  %snot running%s\n",
				cli.Red, cli.Reset)
		} else {
			resp.Body.Close()
			fmt.Printf("  Ollama:  %srunning%s (%s)\n",
				cli.Green, cli.Reset, config.EmbeddingModel)
		}
	}

	// Hooks
	cli.Section("Hooks")
	hookStatus := setup.HooksInstalled(vp)
	hookNames := []string{
		"context-surfacing",
		"decision-extractor",
		"handoff-generator",
		"staleness-check",
	}
	for _, name := range hookNames {
		if hookStatus[name] {
			fmt.Printf("  %-24s %s\u2713 active%s\n",
				name, cli.Green, cli.Reset)
		} else {
			fmt.Printf("  %-24s %s- not configured%s\n",
				name, cli.Dim, cli.Reset)
		}
	}

	// MCP
	cli.Section("MCP")
	if setup.MCPInstalled(vp) {
		fmt.Printf("  registered in .mcp.json\n")
	} else {
		fmt.Printf("  %snot registered%s\n",
			cli.Dim, cli.Reset)
	}

	// Config
	cli.Section("Config")
	if w := config.ConfigWarning(); w != "" {
		fmt.Printf("  %sconfig error:%s %s\n", cli.Red, cli.Reset, w)
		fmt.Printf("  (using defaults — check .same/config.toml)\n")
	} else if config.FindConfigFile() != "" {
		fmt.Printf("  Loaded:  %s\n", cli.ShortenHome(config.FindConfigFile()))
	} else {
		fmt.Printf("  %sno config file%s (using defaults)\n", cli.Dim, cli.Reset)
	}

	cli.Footer()
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
		return config.ErrNoDatabase
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
				return config.ErrNoVault
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
				return config.ErrNoVault
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

// ---------- display ----------

func displayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "display",
		Short: "Change how much SAME shows you",
		Long: `Control how much detail SAME shows when surfacing notes.

Modes:
  full     Show the full box with all details (default)
  compact  Show just a one-line summary
  quiet    Don't show anything

Example: same display compact`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "full",
		Short: "Show full details when surfacing (default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setDisplayMode("full")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "compact",
		Short: "Show just a one-line summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setDisplayMode("compact")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "quiet",
		Short: "Don't show surfacing output",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setDisplayMode("quiet")
		},
	})

	return cmd
}

func setDisplayMode(mode string) error {
	vp := config.VaultPath()
	if vp == "" {
		return config.ErrNoVault
	}

	// Update config file
	cfgPath := config.ConfigFilePath(vp)
	if err := config.SetDisplayMode(vp, mode); err != nil {
		return fmt.Errorf("update config: %w", err)
	}

	switch mode {
	case "full":
		fmt.Println("Display mode: full (show all details)")
		fmt.Println("\nSAME will show the complete box with included/excluded notes.")
	case "compact":
		fmt.Println("Display mode: compact (one-liner)")
		fmt.Println("\nSAME will show: ✦ SAME surfaced 3 of 847 memories")
	case "quiet":
		fmt.Println("Display mode: quiet (hidden)")
		fmt.Println("\nSAME will work silently in the background.")
	}

	fmt.Printf("\nSaved to: %s\n", cli.ShortenHome(cfgPath))
	fmt.Println("Change takes effect on next prompt.")
	return nil
}

// ---------- profile ----------

func profileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Switch between search precision presets",
		Long: `Control how SAME balances precision vs coverage when surfacing notes.

Profiles:
  precise   Fewer results, higher relevance threshold (uses fewer tokens)
  balanced  Default balance of relevance and coverage
  broad     More results, lower threshold (uses ~2x more tokens)

Example: same profile use precise`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showCurrentProfile()
		},
	}

	useCmd := &cobra.Command{
		Use:   "use [profile]",
		Short: "Switch to a profile (precise, balanced, broad)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setProfile(args[0])
		},
	}
	cmd.AddCommand(useCmd)

	return cmd
}

func showCurrentProfile() error {
	current := config.CurrentProfile()

	cli.Header("SAME Profile")
	fmt.Println()

	for _, name := range []string{"precise", "balanced", "broad"} {
		p := config.BuiltinProfiles[name]
		marker := "  "
		if name == current {
			marker = fmt.Sprintf("%s→%s ", cli.Cyan, cli.Reset)
		}

		tokenNote := ""
		if p.TokenWarning != "" {
			tokenNote = fmt.Sprintf(" %s(%s)%s", cli.Dim, p.TokenWarning, cli.Reset)
		}

		fmt.Printf("  %s%-10s %s%s\n", marker, name, p.Description, tokenNote)
	}

	if current == "custom" {
		fmt.Printf("\n  %s→ custom%s (manually configured values)\n", cli.Cyan, cli.Reset)
	}

	fmt.Println()
	fmt.Printf("  Change with: %ssame profile use <name>%s\n", cli.Bold, cli.Reset)
	fmt.Println()

	return nil
}

func setProfile(profileName string) error {
	vp := config.VaultPath()
	if vp == "" {
		return config.ErrNoVault
	}

	profile, ok := config.BuiltinProfiles[profileName]
	if !ok {
		return userError(
			fmt.Sprintf("Unknown profile: %s", profileName),
			"Available: precise, balanced, broad",
		)
	}

	// Show warning for broad profile
	if profileName == "broad" {
		fmt.Printf("\n  %s⚠ Token usage warning:%s\n", cli.Yellow, cli.Reset)
		fmt.Println("  The 'broad' profile surfaces more notes per query,")
		fmt.Println("  which uses approximately 2x more tokens.")
		fmt.Println()
	}

	if err := config.SetProfile(vp, profileName); err != nil {
		return fmt.Errorf("update config: %w", err)
	}

	fmt.Printf("\n  %s✓%s Profile set to: %s%s%s\n", cli.Green, cli.Reset, cli.Bold, profileName, cli.Reset)
	fmt.Printf("    %s\n", profile.Description)

	if profile.TokenWarning != "" {
		fmt.Printf("    %s%s%s\n", cli.Dim, profile.TokenWarning, cli.Reset)
	}

	fmt.Println()
	fmt.Printf("  Settings applied:\n")
	fmt.Printf("    max_results:         %d\n", profile.MaxResults)
	fmt.Printf("    distance_threshold:  %.1f\n", profile.DistanceThreshold)
	fmt.Printf("    composite_threshold: %.2f\n", profile.CompositeThreshold)
	fmt.Println()
	fmt.Println("  Change takes effect on next prompt.")

	return nil
}

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

// ---------- ask (RAG) ----------

func askCmd() *cobra.Command {
	var model string
	var topK int
	cmd := &cobra.Command{
		Use:   "ask [question]",
		Short: "Ask a question and get answers from your notes",
		Long: `Ask a natural language question and get an answer synthesized from your
indexed notes using a local LLM via Ollama.

Requires a chat model installed in Ollama (e.g., llama3.2, mistral, qwen2.5).
SAME will auto-detect the best available model.

Examples:
  same ask "what did we decide about authentication?"
  same ask "how does the deployment process work?"
  same ask "what are our coding standards?" --model mistral`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAsk(args[0], model, topK)
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Ollama model to use (auto-detected if empty)")
	cmd.Flags().IntVar(&topK, "top-k", 5, "Number of notes to use as context")
	return cmd
}

func runAsk(question, model string, topK int) error {
	if strings.TrimSpace(question) == "" {
		return userError("Empty question", "Ask something: same ask \"what did we decide about auth?\"")
	}
	// 1. Open database
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	fmt.Printf("\n  %s⦿%s Searching your notes...\n", cli.Cyan, cli.Reset)

	// 2. Search — vector if available, FTS5 fallback
	var results []store.SearchResult
	if db.HasVectors() {
		embedClient, err := newEmbedProvider()
		if err != nil {
			// Ollama down — try FTS5
			if db.FTSAvailable() {
				var ftsErr error
				results, ftsErr = db.FTS5Search(question, store.SearchOptions{TopK: topK})
				if ftsErr != nil {
					fmt.Fprintf(os.Stderr, "  FTS5 fallback failed: %v\n", ftsErr)
				}
			}
			if len(results) == 0 {
				return fmt.Errorf("can't connect to embedding provider — is Ollama running? (%w)", err)
			}
		} else {
			queryVec, err := embedClient.GetQueryEmbedding(question)
			if err != nil {
				return fmt.Errorf("embed query: %w", err)
			}
			results, err = db.VectorSearch(queryVec, store.SearchOptions{TopK: topK})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
	} else if db.FTSAvailable() {
		results, err = db.FTS5Search(question, store.SearchOptions{TopK: topK})
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
	}

	if len(results) == 0 {
		fmt.Printf("\n  No relevant notes found. Try indexing your notes first: same reindex\n\n")
		return nil
	}

	// 3. Connect to Ollama LLM
	llm, err := ollama.NewClient()
	if err != nil {
		return userError(
			"Ollama is not running",
			"same ask requires Ollama for answers. Start Ollama and try again, or install with: https://ollama.ai",
		)
	}

	// 4. Pick model
	if model == "" {
		model, err = llm.PickBestModel()
		if err != nil {
			return fmt.Errorf("can't list Ollama models: %w", err)
		}
		if model == "" {
			return userError(
				"No chat model found in Ollama",
				"Install one with: ollama pull llama3.2",
			)
		}
	}

	fmt.Printf("  %s⦿%s Thinking with %s (%d sources)...\n", cli.Cyan, cli.Reset, model, len(results))

	// 6. Build context from search results
	var context strings.Builder
	for i, r := range results {
		context.WriteString(fmt.Sprintf("--- Source %d: %s (%s) ---\n", i+1, r.Title, r.Path))
		snippet := r.Snippet
		if len(snippet) > 1000 {
			snippet = snippet[:1000]
		}
		context.WriteString(snippet)
		context.WriteString("\n\n")
	}

	// 7. Build prompt
	prompt := fmt.Sprintf(`You are a helpful assistant that answers questions using ONLY the provided notes.
If the notes don't contain enough information to answer, say so honestly.
Always cite which source(s) you used.

NOTES:
%s
QUESTION: %s

Answer concisely, citing sources by name:`, context.String(), question)

	// 8. Generate answer
	answer, err := llm.Generate(model, prompt)
	if err != nil {
		return fmt.Errorf("generate answer: %w", err)
	}

	// 9. Display answer
	fmt.Printf("\n  %s─── Answer ───────────────────────────────%s\n\n", cli.Cyan, cli.Reset)
	// Indent each line of the answer
	for _, line := range strings.Split(answer, "\n") {
		fmt.Printf("  %s\n", line)
	}

	// 10. Show sources
	fmt.Printf("\n  %s─── Sources ──────────────────────────────%s\n\n", cli.Dim, cli.Reset)
	for i, r := range results {
		fmt.Printf("  %d. %s %s(%s)%s\n", i+1, r.Title, cli.Dim, r.Path, cli.Reset)
	}
	fmt.Println()

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

