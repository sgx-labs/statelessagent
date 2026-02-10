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
	"runtime"
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

// newEmbedProvider creates an embedding provider from config.
func newEmbedProvider() (embedding.Provider, error) {
	ec := config.EmbeddingProviderConfig()
	ollamaURL, err := config.OllamaURL()
	if err != nil {
		return nil, fmt.Errorf("ollama URL: %w", err)
	}
	return embedding.NewProvider(embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ollamaURL,
		Dimensions: ec.Dimensions,
	})
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

	// Compare versions
	if latestVer == currentVer && !force {
		fmt.Printf("\n  %s✓%s Already on the latest version.\n\n", cli.Green, cli.Reset)
		return nil
	}

	if latestVer <= currentVer && !force {
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
		return err
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
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	client, err := newEmbedProvider()
	if err != nil {
		return fmt.Errorf("embedding provider: %w", err)
	}

	// Check for embedding model/dimension mismatch
	if mismatchErr := db.CheckEmbeddingMeta(client.Name(), "", client.Dimensions()); mismatchErr != nil {
		return mismatchErr
	}

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
	if mismatchErr := db.CheckEmbeddingMeta(client.Name(), "", client.Dimensions()); mismatchErr != nil {
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

	// 3. Embedding provider
	check("Ollama connection", "make sure Ollama is running (look for llama icon)", func() (string, error) {
		embedClient, err := newEmbedProvider()
		if err != nil {
			return "", fmt.Errorf("cannot connect: %v", err)
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
		if mismatchErr := db.CheckEmbeddingMeta(embedClient.Name(), "", embedClient.Dimensions()); mismatchErr != nil {
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
			return "", fmt.Errorf("low utilization (%.0f%%) — most injected context is being ignored", rate*100)
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
		if err := os.WriteFile(bakPath, src, 0o644); err != nil {
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

