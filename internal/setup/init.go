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

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// ANSI color codes.
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
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
	fmt.Printf("\n%sSAME %s%s — Stateless Agent Memory Engine\n\n", colorBold, version, colorReset)

	// Step 1: Check Ollama
	fmt.Printf("%sStep 1/5:%s Checking Ollama...\n", colorBold, colorReset)
	if err := checkOllama(); err != nil {
		return err
	}

	// Step 2: Find notes
	fmt.Printf("\n%sStep 2/5:%s Finding your notes...\n", colorBold, colorReset)
	vaultPath, err := detectVault(opts.Yes)
	if err != nil {
		return err
	}

	// Privacy notice
	fmt.Printf("\n%s  Privacy:%s SAME runs entirely on your machine. Embeddings are generated\n", colorDim, colorReset)
	fmt.Printf("%s  by Ollama locally, and your notes index is stored in .same/data/.\n", colorDim)
	fmt.Printf("  Note: Context injected into your AI tool is sent to that tool's API\n")
	fmt.Printf("  (e.g., Anthropic for Claude Code) as part of your conversation.%s\n", colorReset)

	// Step 3: Index
	fmt.Printf("\n%sStep 3/5:%s Indexing...\n", colorBold, colorReset)
	stats, err := runIndex(vaultPath)
	if err != nil {
		return err
	}

	// Step 4: Generate config
	fmt.Printf("\n%sStep 4/5:%s Generating config...\n", colorBold, colorReset)
	if err := generateConfig(vaultPath); err != nil {
		return err
	}

	// Handle .gitignore
	handleGitignore(vaultPath, opts.Yes)

	// Register vault
	registerVault(vaultPath)

	// Step 5: Integrations
	fmt.Printf("\n%sStep 5/5:%s Integrations (optional)\n", colorBold, colorReset)
	if !opts.MCPOnly {
		setupHooksInteractive(vaultPath, opts.Yes)
	}
	setupMCPInteractive(vaultPath, opts.Yes)

	// Done
	fmt.Printf("\n%s%sDone!%s", colorBold, colorGreen, colorReset)
	fmt.Printf(" Run 'claude' in this directory — SAME will surface relevant notes automatically.\n")
	fmt.Printf("Run '%ssame status%s' to see what SAME is doing at any time.\n\n", colorCyan, colorReset)

	// Print summary
	dbPath := filepath.Join(vaultPath, ".same", "data", "vault.db")
	var dbSizeMB float64
	if info, err := os.Stat(dbPath); err == nil {
		dbSizeMB = float64(info.Size()) / (1024 * 1024)
	}
	fmt.Printf("  Notes indexed: %d\n", stats.NotesInIndex)
	fmt.Printf("  Chunks:        %d\n", stats.ChunksInIndex)
	if dbSizeMB > 0 {
		fmt.Printf("  Database:      %.1f MB\n", dbSizeMB)
	}
	fmt.Println()

	return nil
}

// checkOllama verifies Ollama is running and has the required model.
func checkOllama() error {
	ollamaURL := "http://localhost:11434"
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		ollamaURL = v
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Check if Ollama is running
	resp, err := client.Get(ollamaURL + "/api/tags")
	if err != nil {
		fmt.Printf("  %s✗%s Ollama not running at %s\n\n", colorYellow, colorReset, ollamaURL)
		fmt.Println("  Install Ollama:")
		fmt.Println("    macOS:   brew install ollama && ollama serve")
		fmt.Println("    Linux:   curl -fsSL https://ollama.ai/install.sh | sh")
		fmt.Println("    Windows: https://ollama.ai/download")
		fmt.Println()
		fmt.Println("  Then run: ollama pull nomic-embed-text")
		return fmt.Errorf("Ollama is required but not running. Start it and run 'same init' again")
	}
	defer resp.Body.Close()

	fmt.Printf("  %s✓%s Ollama running at %s\n", colorGreen, colorReset, ollamaURL)

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
		// Match both "nomic-embed-text" and "nomic-embed-text:latest"
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			found = true
			break
		}
	}

	if !found {
		fmt.Printf("  %s!%s Model '%s' not found — pulling...\n", colorYellow, colorReset, model)
		if err := pullModel(ollamaURL, model); err != nil {
			fmt.Printf("  %s✗%s Failed to pull model: %v\n", colorYellow, colorReset, err)
			fmt.Printf("\n  Run manually: ollama pull %s\n", model)
			return fmt.Errorf("required model '%s' not available", model)
		}
	}

	fmt.Printf("  %s✓%s %s model available\n", colorGreen, colorReset, model)
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
			fmt.Printf("  Found %s marker in current directory\n", markerName)
			fmt.Printf("  → Using %s (%d markdown files)\n", cwd, count)

			if count == 0 {
				fmt.Printf("  %s!%s No markdown files found. Is this the right directory?\n", colorYellow, colorReset)
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
		fmt.Printf("  Found %d markdown files in current directory\n", count)
		fmt.Printf("  → %s\n", cwd)
		if autoAccept || confirm("  Use this directory?", true) {
			return cwd, nil
		}
	} else {
		fmt.Println("  No vault markers or markdown files found in current directory.")
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
						fmt.Printf("  Found %s vault: %s (%d files)\n", markerName, dir, count)
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
	fmt.Print("  Enter path to your notes directory: ")
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
	fmt.Printf("  → %s (%d markdown files)\n", absPath, count)
	if count == 0 {
		fmt.Printf("  %s!%s Warning: no markdown files found\n", colorYellow, colorReset)
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
		fmt.Printf("\r  [%s] %d/%d files", bar, current, total)
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

	if autoAccept || confirm("\n  Add .same/data/ to .gitignore? (The database is machine-specific)", true) {
		entry := "\n# SAME database (machine-specific)\n.same/data/\n"
		f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Printf("  %s!%s Could not update .gitignore: %v\n", colorYellow, colorReset, err)
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
