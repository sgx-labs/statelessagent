// Package setup implements the `same init` interactive setup wizard.
package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// InitOptions controls the init wizard behavior.
type InitOptions struct {
	Yes     bool // skip all prompts, accept defaults
	MCPOnly bool // skip hooks setup (for Cursor/Windsurf users)
	Version string
}

// RunInit executes the interactive setup wizard.
func RunInit(opts InitOptions) error {
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	cli.Banner(version)

	// Checking Ollama
	cli.Section("Ollama")
	if err := checkOllama(); err != nil {
		return err
	}

	// Finding notes
	cli.Section("Vault")
	vaultPath, err := detectVault(opts.Yes)
	if err != nil {
		return err
	}

	// Indexing
	cli.Section("Indexing")
	stats, err := runIndex(vaultPath)
	if err != nil {
		return err
	}

	// Config
	cli.Section("Config")
	if err := generateConfig(vaultPath); err != nil {
		return err
	}

	// Handle .gitignore
	handleGitignore(vaultPath, opts.Yes)

	// Register vault
	registerVault(vaultPath)

	// Integrations
	cli.Section("Integrations")
	if !opts.MCPOnly {
		setupHooksInteractive(vaultPath, opts.Yes)
	}
	setupMCPInteractive(vaultPath, opts.Yes)

	// Setup complete + summary box
	dbPath := filepath.Join(vaultPath, ".same", "data", "vault.db")
	var dbSizeMB float64
	if info, err := os.Stat(dbPath); err == nil {
		dbSizeMB = float64(info.Size()) / (1024 * 1024)
	}

	boxLines := []string{
		"Setup complete",
		"",
		fmt.Sprintf("Notes:    %s", cli.FormatNumber(stats.NotesInIndex)),
		fmt.Sprintf("Chunks:   %s", cli.FormatNumber(stats.ChunksInIndex)),
	}
	if dbSizeMB > 0 {
		boxLines = append(boxLines, fmt.Sprintf("Database: %.1f MB", dbSizeMB))
	}
	cli.Box(boxLines)

	// Access scope — show exactly what the agent can see
	cli.Section("Scope")
	fmt.Printf("  %sIndexed%s     %s .md files in %s\n",
		cli.Bold, cli.Reset,
		cli.FormatNumber(stats.NotesInIndex), cli.ShortenHome(vaultPath))
	fmt.Printf("  %sExcluded%s    _PRIVATE/, .obsidian/, .git/, .same/\n",
		cli.Bold, cli.Reset)
	fmt.Printf("  %sAgent sees%s  note title + path + 300-char snippet\n",
		cli.Bold, cli.Reset)
	fmt.Printf("  %sWrites to%s   %s (handoffs), %s (decisions)\n",
		cli.Bold, cli.Reset,
		config.HandoffDirectory(), config.DecisionLogPath())
	fmt.Println()
	fmt.Printf("  Run %ssame scope%s to review anytime.\n", cli.Bold, cli.Reset)

	fmt.Println()
	fmt.Printf("  %sSAME is now active.%s When you use Claude Code:\n", cli.Bold, cli.Reset)
	fmt.Println()
	fmt.Printf("  %s→%s Your prompts get matched to relevant notes\n", cli.Cyan, cli.Reset)
	fmt.Printf("  %s→%s Decisions get extracted and saved\n", cli.Cyan, cli.Reset)
	fmt.Printf("  %s→%s Session handoffs keep context across sessions\n", cli.Cyan, cli.Reset)
	fmt.Printf("  %s→%s Notes that help get boosted (feedback loop)\n", cli.Cyan, cli.Reset)
	fmt.Printf("  %s→%s Stale notes get flagged for review\n", cli.Cyan, cli.Reset)
	fmt.Println()
	fmt.Printf("  Try it: run %sclaude%s and ask about something\n", cli.Bold, cli.Reset)
	fmt.Printf("  in your notes.\n")
	fmt.Println()
	fmt.Printf("  Run %ssame status%s anytime.\n", cli.Bold, cli.Reset)

	// Privacy at the end
	cli.Section("Privacy")
	fmt.Printf("  All processing is local via Ollama.\n")
	fmt.Printf("  Context sent to your AI tool's API as\n")
	fmt.Printf("  part of your conversation.\n")

	cli.Footer()

	return nil
}

// checkOllama verifies Ollama is running and has the required model.
func checkOllama() error {
	ollamaURL := "http://localhost:11434"
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		ollamaURL = v
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}

	// Check if Ollama is running
	resp, err := httpClient.Get(ollamaURL + "/api/tags")
	if err != nil {
		fmt.Printf("  %s✗%s Ollama is not running\n\n",
			cli.Yellow, cli.Reset)
		fmt.Println("  SAME needs Ollama (a free app) to understand your notes.")
		fmt.Println()
		fmt.Println("  To fix this:")
		fmt.Println()
		fmt.Println("  If you haven't installed Ollama yet:")
		fmt.Println("    1. Go to https://ollama.ai")
		fmt.Println("    2. Download and install it (like any other app)")
		fmt.Println("    3. Open Ollama - you'll see a llama icon appear")
		fmt.Println()
		fmt.Println("  If Ollama is already installed:")
		fmt.Println("    - Look for the llama icon in your menu bar (Mac) or system tray (Windows)")
		fmt.Println("    - If you don't see it, open the Ollama app")
		fmt.Println()
		fmt.Println("  Once the llama icon appears, run 'same init' again.")
		fmt.Println()
		fmt.Println("  Need help? Join our Discord: https://discord.gg/GZGHtrrKF2")
		return fmt.Errorf("Ollama not running. Start Ollama and try 'same init' again")
	}
	defer resp.Body.Close()

	fmt.Printf("  %s✓%s Running at localhost:11434\n",
		cli.Green, cli.Reset)

	// Check if the model is available
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read Ollama response: %w", err)
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &tagsResp); err != nil {
		return fmt.Errorf("parse Ollama response: %w", err)
	}

	model := config.EmbeddingModel
	found := false
	for _, m := range tagsResp.Models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			found = true
			break
		}
	}

	if !found {
		fmt.Printf("  %s!%s %s not found — pulling...\n",
			cli.Yellow, cli.Reset, model)
		if err := pullModel(ollamaURL, model); err != nil {
			fmt.Printf("  %s✗%s Failed to pull: %v\n",
				cli.Yellow, cli.Reset, err)
			fmt.Printf("\n  Run manually: ollama pull %s\n", model)
			return fmt.Errorf("model '%s' not available", model)
		}
	}

	fmt.Printf("  %s✓%s %s available\n",
		cli.Green, cli.Reset, model)
	return nil
}

// pullModel pulls a model via the Ollama API with progress display.
func pullModel(ollamaURL, model string) error {
	reqBody := fmt.Sprintf(`{"name": %q, "stream": true}`, model)
	resp, err := http.Post(ollamaURL+"/api/pull", "application/json", strings.NewReader(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var progress struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &progress); err != nil {
			continue
		}
		if progress.Total > 0 {
			pct := float64(progress.Completed) / float64(progress.Total) * 100
			fmt.Printf("\r  %s... %.0f%%", progress.Status, pct)
		} else if progress.Status != "" {
			fmt.Printf("\r  %s", progress.Status)
		}
	}
	fmt.Println()
	return scanner.Err()
}

// detectVault finds or prompts for the vault path.
func detectVault(autoAccept bool) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	// Check CWD for markers
	for _, marker := range config.VaultMarkers {
		if _, err := os.Stat(filepath.Join(cwd, marker)); err == nil {
			markerName := strings.TrimPrefix(marker, ".")
			count := indexer.CountMarkdownFiles(cwd)
			fmt.Printf("  %s✓%s %s vault detected\n",
				cli.Green, cli.Reset, markerName)
			fmt.Printf("    %s\n", cli.ShortenHome(cwd))
			fmt.Printf("    %s markdown files\n",
				cli.FormatNumber(count))

			if count == 0 {
				fmt.Printf("  %s!%s No markdown files found\n",
					cli.Yellow, cli.Reset)
			}

			if !autoAccept && count > 0 {
				if !confirm("  Use this directory?", true) {
					return promptForPath()
				}
			}
			return cwd, nil
		}
	}

	// Check if CWD has markdown files even without a marker
	count := indexer.CountMarkdownFiles(cwd)
	if count > 0 {
		fmt.Printf("  Found %s markdown files\n",
			cli.FormatNumber(count))
		fmt.Printf("    %s\n", cli.ShortenHome(cwd))
		if autoAccept || confirm("  Use this directory?", true) {
			return cwd, nil
		}
	} else {
		fmt.Println("  No vault markers or markdown files found.")
	}

	// Check common locations
	home, _ := os.UserHomeDir()
	commonPaths := []string{
		filepath.Join(home, "Documents"),
		filepath.Join(home, "Notes"),
		filepath.Join(home, "notes"),
		filepath.Join(home, "Obsidian"),
		filepath.Join(home, "obsidian"),
	}

	for _, base := range commonPaths {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(base, e.Name())
			for _, marker := range config.VaultMarkers {
				if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
					count := indexer.CountMarkdownFiles(dir)
					if count > 0 {
						markerName := strings.TrimPrefix(marker, ".")
						fmt.Printf("  Found %s vault: %s (%s files)\n",
							markerName,
							cli.ShortenHome(dir),
							cli.FormatNumber(count))
						if autoAccept || confirm("  Use this directory?", true) {
							return dir, nil
						}
					}
				}
			}
		}
	}

	return promptForPath()
}

func promptForPath() (string, error) {
	fmt.Print("  Enter path to your notes: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	path := strings.TrimSpace(line)
	if path == "" {
		return "", fmt.Errorf("no path provided")
	}

	// Expand ~ to home dir
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("directory does not exist: %s", absPath)
	}

	count := indexer.CountMarkdownFiles(absPath)
	fmt.Printf("    %s\n", cli.ShortenHome(absPath))
	fmt.Printf("    %s markdown files\n", cli.FormatNumber(count))
	if count == 0 {
		fmt.Printf("  %s!%s No markdown files found\n",
			cli.Yellow, cli.Reset)
	}

	return absPath, nil
}

// runIndex indexes the vault with a progress bar.
func runIndex(vaultPath string) (*indexer.Stats, error) {
	// Ensure data dir exists
	dataDir := filepath.Join(vaultPath, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// Temporarily set the vault path for the indexer
	origVault := os.Getenv("VAULT_PATH")
	os.Setenv("VAULT_PATH", vaultPath)
	defer func() {
		if origVault != "" {
			os.Setenv("VAULT_PATH", origVault)
		} else {
			os.Unsetenv("VAULT_PATH")
		}
	}()

	db, err := store.Open()
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	barWidth := 40
	progress := func(current, total int, path string) {
		if total == 0 {
			return
		}
		filled := current * barWidth / total
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		fmt.Printf("\r  [%s] %d/%d", bar, current, total)
	}

	stats, err := indexer.ReindexWithProgress(db, true, progress)
	if err != nil {
		return nil, fmt.Errorf("indexing failed: %w", err)
	}

	fmt.Println() // newline after progress bar
	return stats, nil
}

// generateConfig writes the default config file.
func generateConfig(vaultPath string) error {
	configPath := config.ConfigFilePath(vaultPath)
	if err := config.GenerateConfig(vaultPath); err != nil {
		return fmt.Errorf("generate config: %w", err)
	}

	rel, _ := filepath.Rel(vaultPath, configPath)
	fmt.Printf("  → %s\n", rel)
	return nil
}

// handleGitignore checks and offers to add .same/data/ to .gitignore.
func handleGitignore(vaultPath string, autoAccept bool) {
	gitignorePath := filepath.Join(vaultPath, ".gitignore")

	// Only proceed if .gitignore exists (we don't create one)
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		return
	}

	// Check if .same/data/ is already ignored
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == ".same/data/" || line == ".same/data" || line == ".same/" || line == ".same" {
			return // already ignored
		}
	}

	if autoAccept || confirm("\n  Add .same/data/ to .gitignore?", true) {
		entry := "\n# SAME database (machine-specific)\n.same/data/\n"
		f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Printf("  %s!%s Could not update .gitignore: %v\n",
				cli.Yellow, cli.Reset, err)
			return
		}
		defer f.Close()
		f.WriteString(entry)
		fmt.Printf("  → Added .same/data/ to .gitignore\n")
	}
}

// registerVault adds the vault to the registry.
func registerVault(vaultPath string) {
	reg := config.LoadRegistry()
	name := filepath.Base(vaultPath)

	// Avoid duplicate registration
	for _, p := range reg.Vaults {
		if p == vaultPath {
			return
		}
	}

	// Find unique name
	baseName := name
	for i := 2; ; i++ {
		if _, exists := reg.Vaults[name]; !exists {
			break
		}
		name = fmt.Sprintf("%s-%d", baseName, i)
	}

	reg.Vaults[name] = vaultPath
	if reg.Default == "" {
		reg.Default = name
	}
	reg.Save()
}

// confirm asks a yes/no question. defaultYes controls the default.
func confirm(question string, defaultYes bool) bool {
	hint := "[Y/n]"
	if !defaultYes {
		hint = "[y/N]"
	}
	fmt.Printf("%s %s ", question, hint)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return defaultYes
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}
