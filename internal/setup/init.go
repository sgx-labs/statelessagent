// Package setup implements the `same init` interactive setup wizard.
package setup

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/seed"
	"github.com/sgx-labs/statelessagent/internal/store"
)

//go:embed welcome/*.md
var welcomeNotes embed.FS

// InitOptions controls the init wizard behavior.
type InitOptions struct {
	Yes       bool // skip all prompts, accept defaults
	MCPOnly   bool // skip hooks setup (for Cursor/Windsurf users)
	HooksOnly bool // skip MCP setup (Claude Code only)
	Verbose   bool // show detailed progress (each file being processed)
	Version   string
	Provider  string // embedding provider override: ollama, openai, openai-compatible, none
}

// ExperienceLevel represents the user's coding experience.
type ExperienceLevel string

const (
	LevelVibeCoder ExperienceLevel = "vibe-coder"
	LevelDev       ExperienceLevel = "dev"
)

func normalizeEmbedProvider(provider string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" {
		return "ollama", nil
	}

	switch p {
	case "ollama", "openai", "openai-compatible", "none":
		return p, nil
	default:
		return "", fmt.Errorf("invalid embedding provider %q (valid: ollama, openai, openai-compatible, none)", provider)
	}
}

// checkDependencies verifies runtime dependencies (Node, embedding runtime) and
// optionally checks Go/CGO for users building from source.
// Warns but does not block setup for missing deps.
func checkDependencies(embedProvider string) {
	headerShown := false
	showHeader := func() {
		if !headerShown {
			cli.Section("Dependencies")
			headerShown = true
		}
	}

	// ── Runtime dependencies ──────────────────────────────

	// Check Node.js (only needed for npx-based MCP client installs, not for SAME itself)
	showHeader()
	if _, err := exec.LookPath("node"); err != nil {
		fmt.Printf("  %s·%s Node.js not found %s(optional — only needed for npx installs)%s\n", cli.Dim, cli.Reset, cli.Dim, cli.Reset)
	} else {
		fmt.Printf("  %s✓%s Node.js installed\n", cli.Green, cli.Reset)
	}

	// Check Ollama availability. It's required only when using provider=ollama.
	if _, err := exec.LookPath("ollama"); err != nil {
		showHeader()
		if embedProvider == "ollama" || embedProvider == "" {
			fmt.Printf("  %s!%s Ollama not found\n", cli.Yellow, cli.Reset)
			fmt.Println("    You selected provider=ollama for semantic search.")
			fmt.Println("    Install from: https://ollama.com")
			fmt.Println()
		} else {
			fmt.Printf("  %s·%s Ollama not found %s(optional for provider=%s)%s\n",
				cli.Dim, cli.Reset, cli.Dim, embedProvider, cli.Reset)
		}
	} else {
		showHeader()
		fmt.Printf("  %s✓%s Ollama installed %s(local semantic option)%s\n",
			cli.Green, cli.Reset, cli.Dim, cli.Reset)
	}

	// ── Build-from-source dependencies (Go, CGO) ─────────

	goPath, err := exec.LookPath("go")
	if err != nil {
		// No Go installed — that's fine if using a pre-built binary
		if headerShown {
			fmt.Println()
		}
		return
	}

	out, err := exec.Command(goPath, "version").Output()
	if err != nil {
		return
	}

	versionStr := string(out)
	re := regexp.MustCompile(`go(\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(versionStr)
	if len(matches) < 3 {
		return
	}

	var major, minor int
	major, _ = strconv.Atoi(matches[1])
	minor, _ = strconv.Atoi(matches[2])

	if major < 1 || (major == 1 && minor < 25) {
		showHeader()
		fmt.Printf("  %s!%s Go %d.%d detected (SAME requires Go 1.25+ for building from source)\n",
			cli.Yellow, cli.Reset, major, minor)
		fmt.Println("    If you installed SAME via a binary, you can ignore this.")
		fmt.Println("    Upgrade Go: https://go.dev/dl/")
		fmt.Println()
	}

	env := os.Getenv("CGO_ENABLED")
	if env == "0" {
		showHeader()
		fmt.Printf("  %s!%s CGO_ENABLED=0 detected (needed for SQLite with vector search)\n",
			cli.Yellow, cli.Reset)
		fmt.Println("    If you installed SAME via a binary, you can ignore this.")
		fmt.Println("    To fix: export CGO_ENABLED=1")
		fmt.Println()
	}

	if headerShown {
		fmt.Println()
	}
}

// acquireInitLock creates a lockfile to prevent concurrent init runs.
// Returns a cleanup function that removes the lockfile, or an error if
// another init is already running.
var initLockProcessExists = processExists

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EPERM
	}
	return false
}

func readInitLockPID(lockPath string) (int, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty lockfile")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid %q", fields[0])
	}
	return pid, nil
}

func acquireInitLock() (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		// Can't determine home dir — skip locking rather than blocking init
		return func() {}, nil
	}
	lockDir := filepath.Join(home, ".config", "same")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: init lock disabled (cannot create lock dir): %v\n", err)
		return func() {}, nil
	}
	lockPath := filepath.Join(lockDir, "init.lock")

	// Try to create the lockfile exclusively.
	// O_CREATE|O_EXCL fails atomically if the file already exists.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			stale := false
			if pid, pidErr := readInitLockPID(lockPath); pidErr == nil {
				stale = !initLockProcessExists(pid)
			} else if info, statErr := os.Stat(lockPath); statErr == nil {
				// Backward compatibility for older lockfiles that did not contain a PID.
				stale = time.Since(info.ModTime()) > 30*time.Minute
			}

			if stale {
				if rmErr := os.Remove(lockPath); rmErr != nil {
					return nil, fmt.Errorf("failed to remove stale init lockfile %s: %w", lockPath, rmErr)
				}
				f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err != nil {
					return nil, fmt.Errorf("another 'same init' is already running (lockfile: %s)", lockPath)
				}
			} else {
				return nil, fmt.Errorf("another 'same init' is already running (lockfile: %s)", lockPath)
			}
		}
		if f == nil {
			fmt.Fprintf(os.Stderr, "same: warning: init lock disabled (lockfile unavailable)\n")
			return func() {}, nil // can't lock, proceed anyway
		}
	}

	// Write PID for debugging
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		if rmErr := os.Remove(lockPath); rmErr != nil && !os.IsNotExist(rmErr) {
			fmt.Fprintf(os.Stderr, "same: warning: init lock cleanup failed (%v)\n", rmErr)
		}
		fmt.Fprintf(os.Stderr, "same: warning: init lock disabled (failed to write lockfile)\n")
		return func() {}, nil
	}
	if err := f.Close(); err != nil {
		if rmErr := os.Remove(lockPath); rmErr != nil && !os.IsNotExist(rmErr) {
			fmt.Fprintf(os.Stderr, "same: warning: init lock cleanup failed (%v)\n", rmErr)
		}
		fmt.Fprintf(os.Stderr, "same: warning: init lock disabled (failed to finalize lockfile)\n")
		return func() {}, nil
	}

	cleanup := func() {
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "same: warning: failed to remove init lockfile %s: %v\n", lockPath, err)
		}
	}
	return cleanup, nil
}

// RunInit executes the interactive setup wizard.
func RunInit(opts InitOptions) error {
	// S20: Prevent concurrent init runs with a lockfile
	unlock, err := acquireInitLock()
	if err != nil {
		return err
	}
	defer unlock()

	version := opts.Version
	if version == "" {
		version = "dev"
	}
	cli.Banner(version)

	// Checking embedding provider (--provider flag overrides config)
	embedProvider := config.EmbeddingProvider()
	if opts.Provider != "" {
		embedProvider = opts.Provider
	}
	embedProvider, err = normalizeEmbedProvider(embedProvider)
	if err != nil {
		return err
	}
	if opts.Provider != "" {
		prevProvider, hadProvider := os.LookupEnv("SAME_EMBED_PROVIDER")
		if err := os.Setenv("SAME_EMBED_PROVIDER", embedProvider); err != nil {
			return fmt.Errorf("set SAME_EMBED_PROVIDER: %w", err)
		}
		defer func() {
			if hadProvider {
				_ = os.Setenv("SAME_EMBED_PROVIDER", prevProvider)
			} else {
				_ = os.Unsetenv("SAME_EMBED_PROVIDER")
			}
		}()
	}

	// Check dependencies (Node, selected embedding runtime, Go version, CGO)
	checkDependencies(embedProvider)

	// Ask experience level first (unless auto-accepting)
	experience := LevelVibeCoder // default
	if !opts.Yes {
		experience = askExperienceLevel()
	}

	// Scan and show project context
	cwd, _ := os.Getwd()
	projectCtx := scanProjectContext(cwd)
	showProjectContext(projectCtx)

	providerReady := true
	var initDetection *ollamaDetection // set during Ollama path for smart config

	switch embedProvider {
	case "none":
		// Explicit keyword-only mode — skip Ollama entirely
		cli.Section("Embeddings")
		fmt.Printf("  %s✓%s Keyword-only mode (provider=none)\n", cli.Green, cli.Reset)
		fmt.Printf("  %s  Semantic search disabled. Switch to ollama/openai/openai-compatible later and run 'same reindex' to upgrade.%s\n", cli.Dim, cli.Reset)
		providerReady = false
	case "openai", "openai-compatible":
		// User has configured an alternate provider — skip Ollama check
		cli.Section("Embeddings")
		fmt.Printf("  %s✓%s Using %s provider\n", cli.Green, cli.Reset, embedProvider)
		ec := config.EmbeddingProviderConfig()
		if ec.Model != "" {
			fmt.Printf("  %s✓%s Model: %s\n", cli.Green, cli.Reset, ec.Model)
		}
		if ec.BaseURL != "" && ec.BaseURL != "https://api.openai.com" {
			fmt.Printf("  %s✓%s Endpoint: %s\n", cli.Green, cli.Reset, ec.BaseURL)
		}
	default:
		cli.Section("Embeddings")
		if opts.Yes {
			// Non-interactive: try Ollama with smart detection
			det, err := checkOllamaWithDetection()
			if err != nil {
				providerReady = false
				fmt.Printf("  %s⚠%s Ollama not detected. Using keyword-only mode (exact matches only).\n", cli.Yellow, cli.Reset)
				fmt.Printf("  %s  For semantic search, install Ollama: https://ollama.com%s\n", cli.Dim, cli.Reset)
				fmt.Printf("  %s  Then run: same reindex%s\n", cli.Dim, cli.Reset)
			} else {
				// Auto-configure best embedding model
				autoConfigureEmbedding(det)
			}
			initDetection = det
		} else {
			// Interactive: probe Ollama, then let user choose provider
			reader := bufio.NewReader(os.Stdin)
			ollamaDetected := probeOllama()
			chosen := offerProviderChoice(ollamaDetected)

			switch chosen {
			case "ollama":
				det, err := checkOllamaWithDetection()
				if err != nil {
					providerReady = false
				} else {
					// Auto-configure best embedding model
					autoConfigureEmbedding(det)
				}
				initDetection = det
			case "openai":
				embedProvider = chosen
				_ = os.Setenv("SAME_EMBED_PROVIDER", chosen)
				// Check for API key
				apiKey := os.Getenv("SAME_EMBED_API_KEY")
				if apiKey == "" {
					apiKey = os.Getenv("OPENAI_API_KEY")
				}
				if apiKey == "" {
					fmt.Printf("\n  Enter your OpenAI API key %s(or set OPENAI_API_KEY env var)%s\n", cli.Dim, cli.Reset)
					fmt.Printf("  API key: ")
					keyInput, _ := reader.ReadString('\n')
					apiKey = strings.TrimSpace(keyInput)
					if apiKey == "" {
						return fmt.Errorf("OpenAI API key required — set OPENAI_API_KEY and run 'same init' again")
					}
					_ = os.Setenv("OPENAI_API_KEY", apiKey)
				}
				fmt.Printf("\n  %s✓%s Using OpenAI API (model: text-embedding-3-small)\n", cli.Green, cli.Reset)
				_ = os.Setenv("SAME_EMBED_MODEL", "text-embedding-3-small")
			case "openai-compatible":
				embedProvider = chosen
				_ = os.Setenv("SAME_EMBED_PROVIDER", chosen)
				baseURL := os.Getenv("SAME_EMBED_BASE_URL")
				ec := config.EmbeddingProviderConfig()
				if baseURL == "" && ec.BaseURL != "" {
					baseURL = ec.BaseURL
				}
				if baseURL == "" {
					type endpoint struct {
						url   string
						label string
					}
					endpoints := []endpoint{
						{"http://localhost:1234", "LM Studio"},
						{"http://localhost:8080", "llama.cpp / LocalAI"},
						{"http://localhost:11434/v1", "Ollama (OpenAI-compatible mode)"},
						{"https://openrouter.ai/api/v1", "OpenRouter (cloud)"},
					}

					fmt.Printf("\n  %sPick your endpoint:%s\n\n", cli.Bold, cli.Reset)
					for i, ep := range endpoints {
						fmt.Printf("    %s%d%s) %-36s %s%s%s\n",
							cli.Cyan, i+1, cli.Reset, ep.url, cli.Dim, ep.label, cli.Reset)
					}
					fmt.Printf("    %s%d%s) Custom URL\n", cli.Cyan, len(endpoints)+1, cli.Reset)
					fmt.Printf("\n  Choice: ")
					urlInput, _ := reader.ReadString('\n')
					pick := strings.TrimSpace(urlInput)

					var n int
					if _, err := fmt.Sscanf(pick, "%d", &n); err == nil && n >= 1 && n <= len(endpoints) {
						baseURL = endpoints[n-1].url
					} else if n == len(endpoints)+1 {
						fmt.Printf("  URL: ")
						customInput, _ := reader.ReadString('\n')
						baseURL = strings.TrimSpace(customInput)
					} else if pick != "" {
						// Treat raw input as a URL
						baseURL = pick
					}

					if baseURL == "" {
						return fmt.Errorf("base URL required — set SAME_EMBED_BASE_URL and run 'same init' again")
					}
					_ = os.Setenv("SAME_EMBED_BASE_URL", baseURL)
				}
				fmt.Printf("\n  %s✓%s Using OpenAI-compatible endpoint: %s\n", cli.Green, cli.Reset, baseURL)

				// Prompt for API key if this looks like a remote endpoint
				apiKey := os.Getenv("SAME_EMBED_API_KEY")
				if apiKey == "" {
					apiKey = os.Getenv("OPENAI_API_KEY")
				}
				if apiKey == "" && !strings.Contains(baseURL, "localhost") && !strings.Contains(baseURL, "127.0.0.1") {
					fmt.Printf("\n  Enter API key for this endpoint %s(or Enter to skip if not required)%s\n", cli.Dim, cli.Reset)
					fmt.Printf("  API key: ")
					keyInput, _ := reader.ReadString('\n')
					apiKey = strings.TrimSpace(keyInput)
					if apiKey != "" {
						_ = os.Setenv("OPENAI_API_KEY", apiKey)
					}
				}
			case "none":
				embedProvider = "none"
				_ = os.Setenv("SAME_EMBED_PROVIDER", "none")
				providerReady = false
				fmt.Printf("\n  %s✓%s Keyword-only mode\n", cli.Green, cli.Reset)
				fmt.Printf("  %s  Semantic search is disabled — recall will only match exact keywords.%s\n", cli.Dim, cli.Reset)
				fmt.Printf("  %s  Add an embedding provider anytime and run 'same reindex' to upgrade.%s\n", cli.Dim, cli.Reset)
			}
		}
	}

	// Offer model selection (interactive only, skip if smart detection already picked)
	if !opts.Yes && embedProvider != "none" && providerReady && initDetection == nil {
		offerModelChoice(embedProvider)
	}

	// Finding notes
	cli.Section("Vault")
	vaultPath, err := detectVault(opts.Yes)
	if err != nil {
		return err
	}

	// Warn about cloud sync
	if !warnCloudSync(vaultPath, opts.Yes) {
		return fmt.Errorf("setup canceled")
	}

	// Copy welcome notes (before indexing so they get included)
	copyWelcomeNotes(vaultPath)

	// Create seed directories
	createSeedStructure(vaultPath, experience)

	// Create default .sameignore (only for new vaults — don't overwrite existing)
	createDefaultSameignore(vaultPath)

	// Indexing — use full mode if any embedding provider is available
	useEmbeddings := embedProvider != "none" && providerReady
	cli.Section("Indexing")
	stats, err := runIndex(vaultPath, opts.Verbose, useEmbeddings)
	if err != nil {
		return err
	}

	// Config (with experience-based defaults)
	cli.Section("Config")
	if err := generateConfigWithExperience(vaultPath, experience); err != nil {
		return err
	}

	// Offer graph LLM extraction (after config exists, before integrations)
	graphLLMEnabled := offerGraphLLMWithDetection(vaultPath, embedProvider, providerReady, opts.Yes, initDetection)
	_ = graphLLMEnabled // used in post-init summary

	// Handle .gitignore
	handleGitignore(vaultPath, opts.Yes)

	// Register vault
	registerVault(vaultPath)

	// Integrations
	cli.Section("Integrations")
	if !opts.MCPOnly {
		setupHooksInteractive(vaultPath, opts.Yes)
	}
	if !opts.HooksOnly {
		setupMCPInteractive(vaultPath, opts.Yes)
	}
	setupGuardInteractive(vaultPath, opts.Yes)

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

	// Configuration summary — show what was auto-detected
	if initDetection != nil && initDetection.Running {
		showConfigurationSummary(initDetection, embedProvider, useEmbeddings)
	}

	// Compact summary — collapsed Scope + Modes + Privacy into a few lines
	searchMode := "keyword-only"
	if useEmbeddings {
		searchMode = fmt.Sprintf("semantic (%s)", embedProvider)
	}
	cli.Section("Summary")
	fmt.Printf("  %sIndexed:%s  %s files in %s\n",
		cli.Bold, cli.Reset,
		cli.FormatNumber(stats.NotesInIndex), cli.ShortenHome(vaultPath))
	fmt.Printf("  %sPrivate:%s  _PRIVATE/ %s(never indexed)%s, research/ %s(indexed, not committed)%s\n",
		cli.Bold, cli.Reset, cli.Dim, cli.Reset, cli.Dim, cli.Reset)
	if experience == LevelVibeCoder {
		// Vibe-coders: just show search mode, skip graph/endpoint details
		fmt.Printf("  %sSearch:%s   %s\n",
			cli.Bold, cli.Reset, searchMode)
	} else {
		// Devs: show full mode details
		fmt.Printf("  %sSearch:%s   %s\n",
			cli.Bold, cli.Reset, searchMode)
		graphMode := "regex"
		switch config.GraphLLMMode() {
		case "local-only":
			graphMode = "LLM local-only + regex"
		case "on":
			graphMode = "LLM + regex"
		}
		fmt.Printf("  %sGraph:%s    %s  %s|  Ask: ready%s\n",
			cli.Bold, cli.Reset, graphMode, cli.Dim, cli.Reset)
	}
	// Privacy — inline instead of a separate section
	ec := config.EmbeddingProviderConfig()
	if ec.Provider == "openai" || ec.Provider == "openai-compatible" {
		fmt.Printf("  %sPrivacy:%s  embeddings via %s; raw notes stay local\n",
			cli.Bold, cli.Reset, ec.Provider)
	} else {
		fmt.Printf("  %sPrivacy:%s  all processing is local\n",
			cli.Bold, cli.Reset)
	}
	fmt.Println()
	fmt.Printf("  Run %ssame status%s to review anytime.\n", cli.Bold, cli.Reset)

	// Test search to prove it works — show snippet + score
	testResult := runTestSearch(vaultPath)
	if testResult != nil {
		fmt.Println()
		fmt.Printf("  %s✓%s Search working! %s\"how does SAME work\"%s\n", cli.Green, cli.Reset, cli.Dim, cli.Reset)
		// Truncate snippet to ~80 chars for compact display
		snippet := testResult.Snippet
		if len(snippet) > 80 {
			snippet = snippet[:77] + "..."
		}
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		fmt.Printf("    %s→%s %s%s%s %s(%.0f%% match)%s\n",
			cli.Cyan, cli.Reset, cli.Bold, testResult.Title, cli.Reset,
			cli.Dim, testResult.Score*100, cli.Reset)
		if snippet != "" {
			fmt.Printf("      %s%s%s\n", cli.Dim, snippet, cli.Reset)
		}
	}

	// Seeds (interactive install for empty vaults, intro for populated ones)
	if stats.NotesInIndex > 0 {
		showSeedIntro(opts)
	} else {
		offerSeedInstall(opts)
	}

	// The big moment — right before Get Started for maximum impact
	fmt.Println()
	fmt.Printf("  %s══════════════════════════════════════════════════════%s\n", cli.Cyan, cli.Reset)
	fmt.Println()
	if stats.NotesInIndex > 0 {
		fmt.Printf("  %s%s  ✦  NOW YOUR AI REMEMBERS  ✦  %s\n", cli.Bold, cli.Cyan, cli.Reset)
	} else {
		fmt.Printf("  %s%s  ✦  YOUR VAULT IS READY  ✦  %s\n", cli.Bold, cli.Cyan, cli.Reset)
	}
	fmt.Println()
	fmt.Printf("  %s══════════════════════════════════════════════════════%s\n", cli.Cyan, cli.Reset)
	fmt.Println()

	// Smart seed hints based on detected project
	showSmartSeedHints(projectCtx)

	// Model awareness — only show for smaller/local models
	showModelAwareness(embedProvider)

	// Getting started — what you can do now
	cli.Section("Getting Started")
	fmt.Printf("    %ssame search \"query\"%s           Search your notes semantically\n",
		cli.Cyan, cli.Reset)
	fmt.Printf("    %ssame stale%s                    Check for outdated notes\n",
		cli.Cyan, cli.Reset)
	fmt.Printf("    %ssame health%s                   Vault health + trust overview\n",
		cli.Cyan, cli.Reset)
	fmt.Printf("    %ssame brief%s                    AI-generated orientation briefing\n",
		cli.Cyan, cli.Reset)
	fmt.Printf("    %ssame web --open%s               Visual dashboard with knowledge graph\n",
		cli.Cyan, cli.Reset)
	fmt.Printf("    %ssame ignore%s                   Manage file exclusions\n",
		cli.Cyan, cli.Reset)
	if !graphLLMEnabled && config.GraphLLMMode() == "off" {
		fmt.Printf("    %ssame graph enable%s             Richer graph extraction %s(best with 7B+ models)%s\n",
			cli.Cyan, cli.Reset, cli.Dim, cli.Reset)
	}
	fmt.Println()
	fmt.Printf("  Your AI agent has 17 MCP tools available automatically.\n")
	fmt.Printf("  Run %ssame demo%s to see everything in action.\n", cli.Cyan, cli.Reset)
	fmt.Printf("\n  %sTip:%s Restart your editor (Claude Code, Cursor, etc.) to pick up the new MCP configuration.\n",
		cli.Bold, cli.Reset)
	fmt.Println()
	fmt.Printf("  %sNew?%s %ssame tutorial%s  %s|%s  %sMore projects?%s %ssame init%s in any directory.\n",
		cli.Bold, cli.Reset, cli.Cyan, cli.Reset,
		cli.Dim, cli.Reset,
		cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
	fmt.Printf("\n  %sSecurity: Run 'same guard settings set push-protect on' to enable PII scanning and push protection.%s\n",
		cli.Dim, cli.Reset)

	cli.Footer()

	return nil
}

// offerSeedInstall prompts the user to install a seed vault when the vault is empty.
// The flow is opt-in at every step: Enter always skips.
// Returns true if a seed was successfully installed.
func offerSeedInstall(opts InitOptions) bool {
	if opts.Yes {
		return false // non-interactive mode, skip
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("  %sYour vault is ready but empty.%s\n", cli.Bold, cli.Reset)
	fmt.Println()
	fmt.Printf("  Want to explore seed vaults? Pre-built knowledge bases your AI can search.\n")
	fmt.Printf("  Browse seeds? [y/N]: ")

	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println() // handle EOF/Ctrl+D gracefully
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "y" && line != "yes" {
		// User skipped — show tips
		fmt.Println()
		fmt.Printf("  No problem! Add markdown (.md) files to this directory, then either:\n")
		fmt.Printf("  %s→%s Run %ssame reindex%s to update manually\n", cli.Cyan, cli.Reset, cli.Bold, cli.Reset)
		fmt.Printf("  %s→%s Or just start a Claude/Cursor session — SAME picks up new files automatically\n", cli.Cyan, cli.Reset)
		fmt.Println()
		fmt.Printf("  %sInstall seeds anytime with: same seed list%s\n", cli.Dim, cli.Reset)
		return false
	}

	// Fetch manifest — gracefully handle network failure
	manifest, err := seed.FetchManifest(false)
	if err != nil {
		fmt.Printf("\n  %s!%s Could not fetch seed list %s(check your connection)%s\n",
			cli.Yellow, cli.Reset, cli.Dim, cli.Reset)
		fmt.Printf("  %sInstall seeds later with: same seed list%s\n\n", cli.Dim, cli.Reset)
		return false
	}

	if len(manifest.Seeds) == 0 {
		fmt.Printf("\n  %s!%s No seeds available\n", cli.Yellow, cli.Reset)
		return false
	}

	// Show numbered list grouped by category
	fmt.Println()
	printSeedsByCategory(manifest.Seeds)
	fmt.Println()

	fmt.Printf("  Pick numbers to install (e.g. 1,3,8), or Enter to skip: ")
	line, err = reader.ReadString('\n')
	if err != nil {
		fmt.Println() // handle EOF/Ctrl+D
		return false
	}
	line = strings.TrimSpace(line)

	if line == "" {
		fmt.Printf("\n  No problem! Install seeds anytime with %ssame seed install <name>%s\n", cli.Bold, cli.Reset)
		return false
	}

	// Parse comma-separated or single number
	var choices []int
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err != nil || n < 1 || n > len(manifest.Seeds) {
			fmt.Printf("  %s!%s Invalid choice: %s\n", cli.Yellow, cli.Reset, part)
			return false
		}
		choices = append(choices, n-1)
	}

	if len(choices) == 0 {
		fmt.Printf("\n  No problem! Install seeds anytime with %ssame seed install <name>%s\n", cli.Bold, cli.Reset)
		return false
	}

	installed := false
	for _, idx := range choices {
		chosen := manifest.Seeds[idx]

		// Skip if already installed
		if seed.IsInstalled(chosen.Name) {
			fmt.Printf("\n  %s✓%s %s already installed — skipping\n",
				cli.Green, cli.Reset, chosen.DisplayName)
			installed = true
			continue
		}

		destDir := filepath.Join(seed.DefaultSeedDir(), chosen.Name)
		fmt.Println()
		fmt.Printf("  Installing %s%s%s to %s...\n\n",
			cli.Bold, chosen.DisplayName, cli.Reset, cli.ShortenHome(destDir))

		installOpts := seed.InstallOptions{
			Name:    chosen.Name,
			Version: opts.Version,
			OnDownloadStart: func() {
				fmt.Printf("  Downloading...               ")
			},
			OnDownloadDone: func(sizeKB int) {
				fmt.Printf("done (%d KB)\n", sizeKB)
			},
			OnExtractDone: func(fileCount int) {
				fmt.Printf("  Extracting %d files...       done\n", fileCount)
			},
			OnIndexDone: func(chunks int) {
				if chunks > 0 {
					fmt.Printf("  Indexing...                  done (%d chunks)\n", chunks)
				} else {
					fmt.Printf("  Indexing...                  skipped\n")
				}
			},
		}

		result, err := seed.Install(installOpts)
		if err != nil {
			if strings.Contains(err.Error(), "already installed") {
				fmt.Printf("  %s✓%s %v\n\n", cli.Green, cli.Reset, err)
			} else {
				fmt.Printf("  %s!%s %v\n\n", cli.Yellow, cli.Reset, err)
			}
			continue
		}

		fmt.Printf("  Registered as vault %q\n", chosen.Name)
		fmt.Printf("  Installed to %s\n", cli.ShortenHome(result.DestDir))
		installed = true
	}

	if installed {
		seed.PrintLegalNotice()
		fmt.Printf("\n  %sSearch seeds with:%s same search \"your query\" --vault <name>\n\n",
			cli.Bold, cli.Reset)
	}
	return installed
}

// showSeedIntro displays the seed vaults section during init.
// seedCategoryOf maps seed names to display categories.
var seedCategoryOf = map[string]string{
	"claude-code-power-user":          "Developer Tools",
	"ai-agent-architecture":           "Developer Tools",
	"security-audit-framework":        "Developer Tools",
	"devops-runbooks":                 "Developer Tools",
	"api-design-patterns":             "Developer Tools",
	"typescript-fullstack-patterns":   "Developer Tools",
	"devcontainer-quickstart":         "Developer Tools",
	"indie-hacker-playbook":           "Career & Business",
	"open-source-launch-kit":          "Career & Business",
	"freelancer-business-kit":         "Career & Business",
	"resume-interview-prep":           "Career & Business",
	"engineering-management-playbook": "Career & Business",
	"personal-productivity-os":        "Personal & Life",
	"home-chef-essentials":            "Personal & Life",
	"fitness-and-wellness":            "Personal & Life",
	"same-getting-started":            "Getting Started",
	"technical-writing-toolkit":       "Getting Started",
}

// seedCategoryOrder controls the display order of categories.
var seedCategoryOrder = []string{"Developer Tools", "Career & Business", "Personal & Life", "Getting Started"}

// printSeedsByCategory displays featured/essential seeds first, then a hint to browse the rest.
func printSeedsByCategory(seeds []seed.Seed) {
	// Always show these seeds regardless of featured flag
	alwaysShow := map[string]bool{"same-getting-started": true}

	var pinned []int
	var featured []int
	var rest int
	for i, s := range seeds {
		if alwaysShow[s.Name] {
			pinned = append(pinned, i)
		} else if s.Featured {
			featured = append(featured, i)
		} else {
			rest++
		}
	}
	highlighted := append(pinned, featured...)

	if len(highlighted) > 0 {
		fmt.Printf("  %sRecommended%s\n", cli.Bold, cli.Reset)
		for _, i := range highlighted {
			s := seeds[i]
			marker := "★"
			if !s.Featured {
				marker = "›"
			}
			fmt.Printf("    %s%2d%s)%s %-30s %3d notes   %s%s%s\n",
				cli.Cyan, i+1, cli.Reset, marker, s.Name, s.NoteCount, cli.Dim, s.Description, cli.Reset)
		}
		fmt.Println()
	}

	if rest > 0 {
		fmt.Printf("  %s+%d more seeds (career, personal, devops, security) — run 'same seed list' to browse all%s\n",
			cli.Dim, rest, cli.Reset)
		fmt.Println()
	}
}

// Shows available seeds and lets users pick one to install, or skip.
// In non-interactive mode (--yes), shows the list without prompting.
func showSeedIntro(opts InitOptions) {
	cli.Section("Seed Vaults")
	fmt.Printf("  Add expert knowledge alongside your own notes.\n")
	fmt.Printf("  Pre-built, domain-specific — each installs to its own directory in %s~/same-seeds/%s.\n", cli.Dim, cli.Reset)
	fmt.Println()

	manifest, err := seed.FetchManifest(false)
	if err != nil || len(manifest.Seeds) == 0 {
		// Offline or empty manifest — just show commands
		fmt.Printf("  %sBrowse:%s  same seed list\n", cli.Bold, cli.Reset)
		fmt.Printf("  %sInstall:%s same seed install <name>\n", cli.Bold, cli.Reset)
		fmt.Println()
		return
	}

	// Show numbered list grouped by category
	printSeedsByCategory(manifest.Seeds)
	fmt.Println()

	if opts.Yes {
		// Non-interactive — show commands only
		fmt.Printf("  %sInstall:%s same seed install <name>\n", cli.Bold, cli.Reset)
		fmt.Println()
		return
	}

	// Interactive — let user pick (supports comma-separated multi-select)
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("  Pick numbers to install (e.g. 1,3,8), or Enter to skip: ")
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println()
		return
	}
	line = strings.TrimSpace(line)

	if line == "" {
		fmt.Printf("\n  %sInstall seeds anytime with: same seed install <name>%s\n", cli.Dim, cli.Reset)
		return
	}

	// Parse comma-separated or single number
	var choices []int
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err != nil || n < 1 || n > len(manifest.Seeds) {
			fmt.Printf("  %s!%s Invalid choice: %s\n", cli.Yellow, cli.Reset, part)
			return
		}
		choices = append(choices, n-1)
	}

	if len(choices) == 0 {
		fmt.Printf("\n  %sInstall seeds anytime with: same seed install <name>%s\n", cli.Dim, cli.Reset)
		return
	}

	for _, idx := range choices {
		chosen := manifest.Seeds[idx]

		// Skip if already installed
		if seed.IsInstalled(chosen.Name) {
			fmt.Printf("\n  %s✓%s %s already installed — skipping\n",
				cli.Green, cli.Reset, chosen.DisplayName)
			continue
		}

		destDir := filepath.Join(seed.DefaultSeedDir(), chosen.Name)
		fmt.Println()
		fmt.Printf("  Installing %s%s%s to %s...\n\n",
			cli.Bold, chosen.DisplayName, cli.Reset, cli.ShortenHome(destDir))

		installOpts := seed.InstallOptions{
			Name:    chosen.Name,
			Version: opts.Version,
			OnDownloadStart: func() {
				fmt.Printf("  Downloading...               ")
			},
			OnDownloadDone: func(sizeKB int) {
				fmt.Printf("done (%d KB)\n", sizeKB)
			},
			OnExtractDone: func(fileCount int) {
				fmt.Printf("  Extracting %d files...       done\n", fileCount)
			},
			OnIndexDone: func(chunks int) {
				if chunks > 0 {
					fmt.Printf("  Indexing...                  done (%d chunks)\n", chunks)
				} else {
					fmt.Printf("  Indexing...                  skipped\n")
				}
			},
		}

		result, err := seed.Install(installOpts)
		if err != nil {
			if strings.Contains(err.Error(), "already installed") {
				fmt.Printf("  %s✓%s %v\n\n", cli.Green, cli.Reset, err)
			} else {
				fmt.Printf("  %s!%s %v\n\n", cli.Yellow, cli.Reset, err)
			}
			continue
		}

		fmt.Printf("  Registered as vault %q\n", chosen.Name)
		fmt.Printf("  Installed to %s\n", cli.ShortenHome(result.DestDir))
	}

	if len(choices) > 0 {
		seed.PrintLegalNotice()
		fmt.Printf("\n  %sSearch seeds with:%s same search \"your query\" --vault <name>\n\n",
			cli.Bold, cli.Reset)
	}
}

// offerModelChoice shows available embedding models and lets the user pick one.
// Only shown during interactive init (not --yes). The default model is pre-selected.
func offerModelChoice(provider string) {
	// Filter models for this provider
	var models []config.ModelInfo
	for _, m := range config.KnownModels {
		if provider == "ollama" && m.Provider == "ollama" {
			models = append(models, m)
		} else if (provider == "openai" || provider == "openai-compatible") && m.Provider == "openai" {
			models = append(models, m)
		}
	}

	// For openai-compatible, show all ollama models too (they work via any server)
	if provider == "openai-compatible" {
		for _, m := range config.KnownModels {
			if m.Provider == "ollama" {
				models = append(models, m)
			}
		}
	}

	if len(models) <= 1 {
		return // nothing to choose
	}

	current := config.EmbeddingModel
	ec := config.EmbeddingProviderConfig()
	if ec.Model != "" {
		current = ec.Model
	}

	fmt.Println()
	fmt.Printf("  %sEmbedding model:%s %s\n", cli.Bold, cli.Reset, current)
	fmt.Printf("  %sAlternatives available — pick a number to switch, or Enter to keep:%s\n\n", cli.Dim, cli.Reset)

	for i, m := range models {
		marker := "  "
		if m.Name == current {
			marker = fmt.Sprintf("%s→%s ", cli.Cyan, cli.Reset)
		}
		fmt.Printf("    %s%s%2d%s) %-28s %4d dims  %s%s%s\n",
			marker, cli.Cyan, i+1, cli.Reset, m.Name, m.Dims, cli.Dim, m.Description, cli.Reset)
	}
	fmt.Println()
	fmt.Printf("  Choice [Enter = keep %s]: ", current)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println()
		return
	}
	line = strings.TrimSpace(line)

	if line == "" {
		return // keep current
	}

	var n int
	if _, err := fmt.Sscanf(line, "%d", &n); err != nil || n < 1 || n > len(models) {
		fmt.Printf("  %s!%s Invalid choice — keeping %s\n", cli.Yellow, cli.Reset, current)
		return
	}

	chosen := models[n-1]
	if chosen.Name == current {
		return // already selected
	}

	// Persist model choice in env so it's visible for the rest of init
	// (config file write may fail if vault path isn't known yet).
	_ = os.Setenv("SAME_EMBED_MODEL", chosen.Name)

	// Also write to config file if vault is known
	vp := config.VaultPath()
	if vp != "" {
		if err := config.SetEmbeddingModel(vp, chosen.Name); err != nil {
			fmt.Printf("  %s!%s Could not save model choice: %v\n", cli.Yellow, cli.Reset, err)
		}
	}

	// For Ollama, pull the model if not already available
	if provider == "ollama" {
		ollamaURL := "http://localhost:11434"
		if v := os.Getenv("OLLAMA_URL"); v != "" {
			ollamaURL = v
		}
		httpClient := &http.Client{Timeout: 5 * time.Second}
		resp, err := httpClient.Get(ollamaURL + "/api/tags")
		if err == nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			var tagsResp struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			_ = json.Unmarshal(body, &tagsResp)
			found := false
			for _, m := range tagsResp.Models {
				if m.Name == chosen.Name || strings.HasPrefix(m.Name, chosen.Name+":") {
					found = true
					break
				}
			}
			if !found {
				fmt.Printf("  %s!%s %s not found — pulling...\n", cli.Yellow, cli.Reset, chosen.Name)
				if err := pullModel(ollamaURL, chosen.Name); err != nil {
					fmt.Printf("  %s✗%s Failed to pull: %v\n", cli.Yellow, cli.Reset, err)
					fmt.Printf("\n  Run manually: ollama pull %s\n", chosen.Name)
					return
				}
			}
		}
	}

	fmt.Printf("  %s✓%s Switched to %s%s%s (%d dims)\n",
		cli.Green, cli.Reset, cli.Bold, chosen.Name, cli.Reset, chosen.Dims)
}

// probeOllama silently checks if Ollama is responding on localhost.
func probeOllama() bool {
	ollamaURL := "http://localhost:11434"
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		ollamaURL = v
	}
	u, err := url.Parse(ollamaURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "host.docker.internal" {
		return false
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ollamaURL + "/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// offerProviderChoice presents an interactive provider picker.
// Returns the chosen provider name: "ollama", "openai", "openai-compatible", or "none".
func offerProviderChoice(ollamaDetected bool) string {
	fmt.Println()
	// Show container environment notice if detected
	if ci := config.DetectContainer(); ci.Detected {
		fmt.Printf("  %sDetected: container environment (%s)%s\n\n", cli.Dim, ci.Type, cli.Reset)
		fmt.Printf("  %sEmbedding with Ollama may be slower in containers.%s\n", cli.Dim, cli.Reset)
		fmt.Printf("  %sConsider a remote endpoint or keyword-only mode if performance is an issue.%s\n\n", cli.Dim, cli.Reset)
	}
	if ollamaDetected {
		fmt.Printf("  %s✓%s Ollama detected at localhost:11434\n\n", cli.Green, cli.Reset)
	}
	fmt.Printf("  %sChoose your embedding provider:%s\n\n", cli.Bold, cli.Reset)

	var ollamaLabel string
	if ollamaDetected {
		ollamaLabel = "Ollama (detected — local, private, recommended)"
	} else {
		ollamaLabel = "Ollama (requires install — ollama.com)"
	}

	options := []struct {
		name  string
		label string
	}{
		{"ollama", ollamaLabel},
		{"openai", "OpenAI API (requires OPENAI_API_KEY)"},
		{"openai-compatible", "Other local/remote (LM Studio, llama.cpp, vLLM, Jan, OpenRouter — any OpenAI-compatible API)"},
		{"none", "None (keyword-only — no semantic search, exact matches only)"},
	}

	for i, opt := range options {
		fmt.Printf("    %s%d%s) %s\n", cli.Cyan, i+1, cli.Reset, opt.label)
	}

	defaultHint := ""
	if ollamaDetected {
		defaultHint = " [1]"
	}
	fmt.Printf("\n  Pick%s: ", defaultHint)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		if ollamaDetected {
			return "ollama"
		}
		return "none"
	}
	line = strings.TrimSpace(line)

	if line == "" {
		if ollamaDetected {
			return "ollama"
		}
		// No default when Ollama not detected — re-prompt
		fmt.Printf("  %s!%s Please pick a number (1-%d): ", cli.Yellow, cli.Reset, len(options))
		line, err = reader.ReadString('\n')
		if err != nil {
			return "none"
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return "none"
		}
	}

	var n int
	if _, err := fmt.Sscanf(line, "%d", &n); err != nil || n < 1 || n > len(options) {
		fmt.Printf("  %s!%s Invalid choice — defaulting to keyword-only\n", cli.Yellow, cli.Reset)
		return "none"
	}
	return options[n-1].name
}

// offerGraphLLM optionally prompts to enable graph LLM extraction when a
// capable chat model is available. In --yes mode it prints a tip instead.
// Returns true if graph LLM was enabled during this call.
func offerGraphLLM(vaultPath, embedProvider string, providerReady, autoYes bool) bool {
	// Only suggest if graph LLM is currently off
	if config.GraphLLMMode() != "off" {
		return true // already enabled
	}

	// Detect whether any chat model is reachable
	chatAvailable := false
	isLocal := false
	client, err := llm.NewClient()
	if err == nil {
		model, modelErr := client.PickBestModel()
		if modelErr == nil && strings.TrimSpace(model) != "" {
			chatAvailable = true
			isLocal = client.Provider() == "ollama"
		}
	}

	if !chatAvailable {
		return false // no model to recommend
	}

	if autoYes {
		// Non-interactive: don't enable by default, just print a tip
		fmt.Printf("\n  %sTip:%s Run %ssame graph enable%s for richer knowledge graph extraction. Best results with 7B+ models.\n",
			cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
		return false
	}

	// Interactive prompt
	fmt.Println()
	fmt.Printf("  Graph extraction uses your model to find richer connections\n")
	fmt.Printf("  between notes (decisions, dependencies, references).\n\n")
	if confirm("  Enable graph LLM extraction?", true) {
		mode := "on"
		if isLocal {
			mode = "local-only"
		}
		if err := config.SetGraphLLMMode(vaultPath, mode); err != nil {
			fmt.Printf("  %s!%s Could not update config: %v\n", cli.Yellow, cli.Reset, err)
			return false
		}
		fmt.Printf("  %s✓%s Graph LLM extraction enabled (%s)\n", cli.Green, cli.Reset, mode)
		return true
	}

	fmt.Printf("  %sOK. Run %ssame graph enable%s%s anytime to turn it on.%s\n",
		cli.Dim, cli.Cyan, cli.Reset, cli.Dim, cli.Reset)
	return false
}

// offerGraphLLMWithDetection is like offerGraphLLM but uses pre-detected model info
// to auto-enable graph in --yes mode when a capable model is available.
func offerGraphLLMWithDetection(vaultPath, embedProvider string, providerReady, autoYes bool, det *ollamaDetection) bool {
	// Only suggest if graph LLM is currently off
	if config.GraphLLMMode() != "off" {
		return true // already enabled
	}

	// If we have detection data, use it for smarter auto-configuration
	if det != nil && det.Running {
		if det.ChatIs7BPlus && det.BestChat != "" {
			if autoYes {
				// Auto-enable graph with 7B+ model
				if err := config.SetGraphLLMMode(vaultPath, "local-only"); err != nil {
					fmt.Printf("  %s!%s Could not update config: %v\n", cli.Yellow, cli.Reset, err)
					return false
				}
				chatName := det.BestChat
				if idx := strings.Index(chatName, ":"); idx > 0 {
					chatName = chatName[:idx]
				}
				fmt.Printf("  %s✓%s Found %s — enabling graph extraction\n",
					cli.Green, cli.Reset, chatName)
				fmt.Printf("    %s(Graph works best with 7B+ models)%s\n", cli.Dim, cli.Reset)
				return true
			}
			// Interactive with 7B+ available — offer with strong default
			fmt.Println()
			chatName := det.BestChat
			if idx := strings.Index(chatName, ":"); idx > 0 {
				chatName = chatName[:idx]
			}
			fmt.Printf("  Found %s%s%s — graph extraction can find richer connections\n",
				cli.Bold, chatName, cli.Reset)
			fmt.Printf("  between notes (decisions, dependencies, references).\n\n")
			if confirm("  Enable graph LLM extraction?", true) {
				if err := config.SetGraphLLMMode(vaultPath, "local-only"); err != nil {
					fmt.Printf("  %s!%s Could not update config: %v\n", cli.Yellow, cli.Reset, err)
					return false
				}
				fmt.Printf("  %s✓%s Graph LLM extraction enabled (local-only)\n", cli.Green, cli.Reset)
				return true
			}
			fmt.Printf("  %sOK. Run %ssame graph enable%s%s anytime to turn it on.%s\n",
				cli.Dim, cli.Cyan, cli.Reset, cli.Dim, cli.Reset)
			return false
		}

		// Chat models exist but all are small (<7B)
		if len(det.ChatModels) > 0 {
			if autoYes {
				fmt.Printf("  %s✓%s Graph available (regex-only mode)\n", cli.Green, cli.Reset)
				fmt.Printf("    %sFor richer extraction, pull a 7B+ model:%s\n", cli.Dim, cli.Reset)
				fmt.Printf("    %sollama pull qwen2.5:7b%s\n", cli.Dim, cli.Reset)
				return false
			}
			// Interactive — still offer but note it's small models only
			fmt.Println()
			fmt.Printf("  Graph extraction uses your model to find richer connections\n")
			fmt.Printf("  between notes (decisions, dependencies, references).\n")
			fmt.Printf("  %sNote: Best results with 7B+ models. Your models are smaller.%s\n\n", cli.Dim, cli.Reset)
			if confirm("  Enable graph LLM extraction?", false) {
				if err := config.SetGraphLLMMode(vaultPath, "local-only"); err != nil {
					fmt.Printf("  %s!%s Could not update config: %v\n", cli.Yellow, cli.Reset, err)
					return false
				}
				fmt.Printf("  %s✓%s Graph LLM extraction enabled (local-only)\n", cli.Green, cli.Reset)
				return true
			}
			fmt.Printf("  %sOK. Using regex-only graph extraction.%s\n", cli.Dim, cli.Reset)
			return false
		}

		// No chat models at all
		if autoYes {
			return false
		}
		return false
	}

	// No detection data — fall back to the original offerGraphLLM logic
	return offerGraphLLM(vaultPath, embedProvider, providerReady, autoYes)
}

// autoConfigureEmbedding sets the SAME_EMBED_MODEL env var based on detection,
// so that the config generation picks up the best available model automatically.
func autoConfigureEmbedding(det *ollamaDetection) {
	if det == nil || det.BestEmbedding == "" {
		return
	}
	_ = os.Setenv("SAME_EMBED_MODEL", det.BestEmbedding)
}

// ollamaDetection holds the results of model detection during init.
type ollamaDetection struct {
	Running         bool
	EmbeddingModels []string // available embedding models (by preference order)
	ChatModels      []ollamaChatModel
	BestEmbedding   string // best available embedding model
	BestChat        string // best available chat model (empty if none)
	ChatIs7BPlus    bool   // whether the best chat model is >= 7B
	EmbeddingSource string // "auto-detected", "default", "pulled"
}

type ollamaChatModel struct {
	Name string
	Size int64 // bytes
}

// embeddingModelRanking defines preference order for embedding models (best first).
var embeddingModelRanking = []string{
	"qwen3-embedding",
	"bge-m3",
	"snowflake-arctic-embed2",
	"nomic-embed-text-v2-moe",
	"nomic-embed-text",
}

// knownEmbeddingModels is the set of models that are embedding-only.
var knownEmbeddingModels = map[string]bool{
	"nomic-embed-text":        true,
	"nomic-embed-text-v2-moe": true,
	"mxbai-embed-large":       true,
	"all-minilm":              true,
	"snowflake-arctic-embed":  true,
	"snowflake-arctic-embed2": true,
	"embeddinggemma":          true,
	"qwen3-embedding":         true,
	"bge-base-en":             true,
	"bge-large-en":            true,
	"bge-m3":                  true,
}

// detectOllamaModels queries Ollama's /api/tags and classifies available models.
func detectOllamaModels(ollamaURL string) *ollamaDetection {
	det := &ollamaDetection{}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(strings.TrimRight(ollamaURL, "/") + "/api/tags")
	if err != nil {
		return det
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return det
	}

	det.Running = true

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return det
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &tagsResp); err != nil {
		return det
	}

	// Build lookup of available model base names
	available := make(map[string]bool, len(tagsResp.Models))
	for _, m := range tagsResp.Models {
		baseName := m.Name
		if idx := strings.Index(baseName, ":"); idx > 0 {
			baseName = baseName[:idx]
		}
		available[baseName] = true

		if knownEmbeddingModels[baseName] {
			det.EmbeddingModels = append(det.EmbeddingModels, baseName)
		} else {
			det.ChatModels = append(det.ChatModels, ollamaChatModel{
				Name: m.Name,
				Size: m.Size,
			})
		}
	}

	// Pick best embedding model by ranking
	for _, candidate := range embeddingModelRanking {
		if available[candidate] {
			det.BestEmbedding = candidate
			det.EmbeddingSource = "auto-detected"
			break
		}
	}

	// Check for 7B+ chat model (size > 3.5GB typically means 7B+)
	const size7BThreshold int64 = 3_500_000_000
	for _, cm := range det.ChatModels {
		if det.BestChat == "" {
			det.BestChat = cm.Name
		}
		if cm.Size >= size7BThreshold {
			det.ChatIs7BPlus = true
			det.BestChat = cm.Name
			break
		}
	}

	return det
}

// checkOllama verifies Ollama is running and has the required model.
func checkOllama() error {
	_, err := checkOllamaWithDetection()
	return err
}

// checkOllamaWithDetection verifies Ollama is running, detects available models,
// and ensures at least one embedding model is available. Returns the detection
// result for use in auto-configuration.
func checkOllamaWithDetection() (*ollamaDetection, error) {
	ollamaURL := "http://localhost:11434"
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		ollamaURL = v
	}

	// SECURITY: validate that the URL points to localhost before making any request.
	// This prevents SSRF if OLLAMA_URL is set to an external host.
	u, err := url.Parse(ollamaURL)
	if err != nil {
		return nil, fmt.Errorf("invalid OLLAMA_URL: %w", err)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "host.docker.internal" {
		return nil, fmt.Errorf("OLLAMA_URL must point to localhost (got %s)", host)
	}

	det := detectOllamaModels(ollamaURL)

	if !det.Running {
		fmt.Printf("  %s✗%s Ollama is not running\n\n",
			cli.Yellow, cli.Reset)
		fmt.Println("  To fix this:")
		fmt.Println()
		fmt.Println("  If you haven't installed Ollama yet:")
		fmt.Println("    1. Go to https://ollama.com")
		fmt.Println("    2. Download and install it (like any other app)")
		fmt.Println("    3. Open Ollama - you'll see a llama icon appear")
		fmt.Println()
		fmt.Println("  If Ollama is already installed:")
		fmt.Println("    - Look for the llama icon in your menu bar (Mac) or system tray (Windows)")
		fmt.Println("    - If you don't see it, open the Ollama app")
		fmt.Println()
		fmt.Println("  Need help? Join our Discord: https://discord.gg/9KfTkcGs7g")
		return nil, fmt.Errorf("ollama not running: start Ollama and try 'same init' again")
	}

	fmt.Printf("  %s✓%s Running at localhost:11434\n",
		cli.Green, cli.Reset)

	// Use the best detected embedding model, or fall back to default
	model := det.BestEmbedding
	if model == "" {
		model = config.EmbeddingModel // nomic-embed-text
	}

	// Check if the selected model is actually pulled
	found := false
	for _, em := range det.EmbeddingModels {
		if em == model {
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
			return nil, fmt.Errorf("model '%s' not available", model)
		}
		det.BestEmbedding = model
		det.EmbeddingSource = "pulled"
	}

	// Print what was detected
	if det.BestEmbedding != "" && det.BestEmbedding != config.EmbeddingModel {
		fmt.Printf("  %s✓%s Using %s %s(best available)%s\n",
			cli.Green, cli.Reset, det.BestEmbedding, cli.Dim, cli.Reset)
	} else {
		fmt.Printf("  %s✓%s %s available\n",
			cli.Green, cli.Reset, model)
	}

	// Suggest upgrade if only nomic-embed-text is available
	if det.BestEmbedding == "nomic-embed-text" && len(det.EmbeddingModels) == 1 {
		fmt.Printf("    %sTip: For better search quality, run:%s\n", cli.Dim, cli.Reset)
		fmt.Printf("    %sollama pull nomic-embed-text-v2-moe && same model use nomic-embed-text-v2-moe%s\n", cli.Dim, cli.Reset)
	}

	return det, nil
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

// isCloudSyncedPath checks if a path is inside a cloud-synced folder.
func isCloudSyncedPath(path string) (bool, string) {
	absPath, _ := filepath.Abs(path)
	lowerPath := strings.ToLower(absPath)

	cloudIndicators := map[string]string{
		"dropbox":          "Dropbox",
		"onedrive":         "OneDrive",
		"google drive":     "Google Drive",
		"icloud":           "iCloud",
		"mobile documents": "iCloud",
	}

	for indicator, name := range cloudIndicators {
		if strings.Contains(lowerPath, indicator) {
			return true, name
		}
	}
	return false, ""
}

// warnCloudSync warns about cloud-synced folders if detected.
func warnCloudSync(vaultPath string, autoAccept bool) bool {
	isCloud, provider := isCloudSyncedPath(vaultPath)
	if !isCloud {
		return true // proceed
	}

	fmt.Printf("\n  %s⚠%s This folder appears to be in %s.\n\n",
		cli.Yellow, cli.Reset, provider)
	fmt.Println("  Cloud-synced folders can cause database conflicts when")
	fmt.Println("  multiple devices access the same SAME database.")
	fmt.Println()
	fmt.Println("  Recommendations:")
	fmt.Println("    • Use SAME from one computer at a time")
	fmt.Println("    • Add .same/ to your cloud service's ignore list")
	fmt.Println("    • Or use Obsidian Sync instead — it handles vault")
	fmt.Println("      syncing properly and won't conflict with SAME")
	fmt.Println()

	if autoAccept {
		return true
	}
	return confirm("  Continue anyway?", false)
}

// copyWelcomeNotes copies the embedded welcome notes to the vault.
// Notes go to welcome/ at vault root (not .same/welcome/) so they get indexed.
func copyWelcomeNotes(vaultPath string) {
	destDir := filepath.Join(vaultPath, "welcome")

	// Also check legacy location to avoid duplicating
	legacyDir := filepath.Join(vaultPath, ".same", "welcome")
	if _, err := os.Stat(destDir); err == nil {
		// Already copied, skip
		return
	}
	if _, err := os.Stat(legacyDir); err == nil {
		// Legacy location exists, skip
		return
	}

	// Skip welcome notes if the vault already has markdown content.
	// Governed vaults (with CLAUDE.md, README.md, etc.) don't need starter notes.
	if vaultHasNotes(vaultPath) {
		return
	}

	// Create the directory
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		// Silently skip if we can't create the directory
		return
	}

	// Read and copy each welcome note
	entries, err := welcomeNotes.ReadDir("welcome")
	if err != nil {
		return
	}

	copied := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		content, err := welcomeNotes.ReadFile("welcome/" + entry.Name())
		if err != nil {
			continue
		}

		destPath := filepath.Join(destDir, entry.Name())
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			continue
		}
		copied++
	}

	if copied > 0 {
		fmt.Printf("  %s✓%s Added %d welcome notes to welcome/\n",
			cli.Green, cli.Reset, copied)
		fmt.Printf("    %sThese get indexed so your first search finds results%s\n",
			cli.Dim, cli.Reset)
	}
}

// vaultHasNotes checks if the vault root already contains markdown files.
// Used to skip welcome note generation for vaults with existing content.
func vaultHasNotes(vaultPath string) bool {
	return indexer.CountMarkdownFiles(vaultPath) > 0
}

// detectVault finds or prompts for the vault path.
func detectVault(autoAccept bool) (string, error) {
	if override := strings.TrimSpace(config.VaultOverride); override != "" {
		resolved := override
		if resolvedFromRegistry := config.LoadRegistry().ResolveVault(override); resolvedFromRegistry != "" {
			resolved = resolvedFromRegistry
		}
		if strings.HasPrefix(resolved, "~/") || strings.HasPrefix(resolved, `~\`) {
			home, _ := os.UserHomeDir()
			resolved = filepath.Join(home, resolved[2:])
		}
		absPath, err := filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("resolve --vault path: %w", err)
		}
		info, err := os.Stat(absPath)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("vault override path does not exist or is not a directory: %s", absPath)
		}
		count := indexer.CountMarkdownFiles(absPath)
		fmt.Printf("  %s✓%s Vault override (--vault)\n", cli.Green, cli.Reset)
		fmt.Printf("    %s\n", cli.ShortenHome(absPath))
		fmt.Printf("    %s markdown files\n", cli.FormatNumber(count))
		if count == 0 {
			fmt.Printf("  %s!%s No markdown files found\n", cli.Yellow, cli.Reset)
		}
		return absPath, nil
	}

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

	// Check for project documentation (README, docs/, ARCHITECTURE.md, etc.)
	projectDocs := detectProjectDocs(cwd)
	if len(projectDocs) > 0 {
		fmt.Printf("  %s✓%s Detected project documentation:\n", cli.Green, cli.Reset)
		for _, doc := range projectDocs {
			info, err := os.Stat(filepath.Join(cwd, doc))
			if err == nil {
				sizeKB := float64(info.Size()) / 1024
				fmt.Printf("    %s (%s%.1f KB%s)\n", doc, cli.Dim, sizeKB, cli.Reset)
			} else {
				fmt.Printf("    %s\n", doc)
			}
		}
		fmt.Println()
		fmt.Printf("  %sYour AI will be able to search these docs.%s\n", cli.Dim, cli.Reset)
		if autoAccept || confirm("  Index these files?", true) {
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
	} else if len(projectDocs) == 0 {
		fmt.Println("  No vault markers or markdown files found.")
		fmt.Println()
		fmt.Printf("  %sYou can use this directory as a fresh vault.%s\n", cli.Dim, cli.Reset)
		fmt.Printf("  %sSAME will create starter notes and directories for you.%s\n", cli.Dim, cli.Reset)
		fmt.Println()
		if confirm("  Set up SAME in this directory?", true) {
			return cwd, nil
		}
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
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
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
// If useEmbeddings is false, uses lite mode (keyword search only, no embeddings).
func runIndex(vaultPath string, verbose, useEmbeddings bool) (*indexer.Stats, error) {
	// Count files first for time estimate
	noteCount := indexer.CountMarkdownFiles(vaultPath)

	// Show time estimate for large vaults.
	// With embeddings: ~800ms/note (chunking + HTTP embedding calls); ~200ms with batching.
	// Keyword-only: ~50ms/note (just parsing + keyword extraction).
	// Container environments with local Ollama get a 2x multiplier for more honest estimates.
	if noteCount > 100 {
		var msPerNote int
		if useEmbeddings {
			msPerNote = 200 // batched embedding estimate (50 per request)
			// Local Ollama in containers is typically slower due to virtualization overhead
			if config.IsContainer() {
				ec := config.EmbeddingProviderConfig()
				provider := ec.Provider
				if provider == "" || provider == "ollama" {
					msPerNote *= 2
				}
			}
		} else {
			msPerNote = 50 // keyword-only mode
		}
		estSeconds := (noteCount * msPerNote) / 1000
		if estSeconds < 60 {
			fmt.Printf("  Found %s notes. Estimated time: ~%d seconds\n\n",
				cli.FormatNumber(noteCount), estSeconds)
		} else {
			estMinutes := (estSeconds + 30) / 60 // round to nearest minute
			if estMinutes < 1 {
				estMinutes = 1
			}
			fmt.Printf("  Found %s notes. Estimated time: ~%d minute(s)\n\n",
				cli.FormatNumber(noteCount), estMinutes)
		}
	}

	if noteCount > 5000 {
		fmt.Printf("  %s⚠%s Large vault detected.\n", cli.Yellow, cli.Reset)
		if useEmbeddings {
			largeMs := 200
			if config.IsContainer() {
				ec := config.EmbeddingProviderConfig()
				if ec.Provider == "" || ec.Provider == "ollama" {
					largeMs = 400
				}
			}
			estLargeMin := (noteCount * largeMs) / 1000 / 60
			fmt.Printf("  Initial indexing may take %d+ minutes.\n", estLargeMin)
		} else {
			fmt.Println("  Initial indexing may take several minutes.")
		}
		fmt.Println("  After this, SAME only re-indexes changed files.")
		fmt.Println()
	}

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

	// Delete existing DB to ensure clean schema (init always does a full reindex).
	// This prevents dimension mismatches when the user switches embedding models.
	dbPath := config.DBPath()
	if _, err := os.Stat(dbPath); err == nil {
		_ = os.Remove(dbPath)
		// Also remove WAL/SHM files
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	}

	db, err := store.Open()
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	barWidth := 40
	startTime := time.Now()
	progress := func(current, total int, path string) {
		if total == 0 {
			return
		}
		elapsed := time.Since(startTime)
		elapsedStr := formatDuration(elapsed)

		// Estimate remaining time based on progress so far
		var remainStr string
		if current > 0 {
			perNote := elapsed / time.Duration(current)
			remaining := perNote * time.Duration(total-current)
			remainStr = formatDuration(remaining)
		}

		if verbose {
			// Show each file being processed
			shortPath := path
			if len(path) > 50 {
				shortPath = "..." + path[len(path)-47:]
			}
			if remainStr != "" {
				fmt.Printf("\r  [%d/%d] %s elapsed · ~%s remaining · %s\033[K\n",
					current, total, elapsedStr, remainStr, shortPath)
			} else {
				fmt.Printf("\r  [%d/%d] %s\033[K\n", current, total, shortPath)
			}
		} else {
			// Show progress bar with timing
			filled := current * barWidth / total
			bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
			if remainStr != "" {
				fmt.Printf("\r  [%s] %d/%d · %s elapsed · ~%s remaining\033[K",
					bar, current, total, elapsedStr, remainStr)
			} else {
				fmt.Printf("\r  [%s] %d/%d\033[K", bar, current, total)
			}
		}
	}

	// Set up context with signal handling for graceful cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\n  Stopping... press Ctrl+C again to force quit\n")
			cancel()
			// Wait for second signal to force quit
			<-sigCh
			os.Exit(1)
		case <-ctx.Done():
		}
	}()
	defer signal.Stop(sigCh)

	var stats *indexer.Stats
	if useEmbeddings {
		// Progressive mode: FTS5 first (fast), then embeddings (slow).
		// Keyword search works immediately after Phase 1.
		embedProgress := func(completed, total int) {
			if total > 0 {
				fmt.Fprintf(os.Stderr, "\r  Embedding: %d/%d notes (keyword search active)\033[K", completed, total)
			}
		}
		var embResult *indexer.EmbeddingProgress
		stats, embResult, err = indexer.ReindexProgressive(ctx, db, true, progress, embedProgress)
		if err != nil && !errors.Is(err, indexer.ErrCanceled) {
			return nil, fmt.Errorf("indexing failed: %w", err)
		}

		if !verbose {
			fmt.Println() // newline after progress bar
		}

		// Report embedding result
		if embResult != nil && embResult.Total > 0 {
			fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", 70))
			if embResult.Completed == embResult.Total {
				fmt.Printf("  All notes embedded. Semantic search ready.\n")
			} else if errors.Is(err, indexer.ErrCanceled) {
				fmt.Printf("  Embedding paused: %d/%d notes done. Resume with 'same reindex'.\n",
					embResult.Completed, embResult.Total)
			}
		}
	} else {
		stats, err = indexer.ReindexLite(ctx, db, true, progress)
		if err != nil && !errors.Is(err, indexer.ErrCanceled) {
			return nil, fmt.Errorf("indexing failed: %w", err)
		}

		if !verbose {
			fmt.Println() // newline after progress bar
		}
	}
	if stats != nil && stats.Canceled {
		fmt.Printf("\n  %sReindex canceled by user. %d of %d notes indexed.%s\n",
			cli.Yellow, stats.NewlyIndexed, stats.TotalFiles, cli.Reset)
	}
	return stats, nil
}

// formatDuration returns a human-friendly duration string like "2m12s" or "45s".
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// sameGitignoreTemplate is the recommended .gitignore content for SAME vaults.
const sameGitignoreTemplate = `# SAME — Privacy-first .gitignore
# Three tiers: system (never commit), private (never index), local-only (indexed but not committed)

# SAME system data (machine-specific, contains embeddings and DB)
.same/data/

# SAME plugin manifest (vault-local, must be explicitly trusted per-machine)
.same/plugins.json

# Welcome notes (generated by 'same init', indexed but not committed)
welcome/

# Private — never commit, never indexed by SAME
# Put API keys, credentials, and truly secret notes here
_PRIVATE/

# Local research — indexed by SAME but not committed to git
# Your AI can search these notes, but they stay on your machine
# Remove this line if you WANT to version-control your research
research/
`

// handleGitignore ensures the vault has a .gitignore with SAME privacy rules.
// Creates one if it doesn't exist, or appends SAME rules to an existing one.
func handleGitignore(vaultPath string, autoAccept bool) {
	gitignorePath := filepath.Join(vaultPath, ".gitignore")

	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		// No .gitignore exists — create one with the full template
		fmt.Println()
		fmt.Printf("  %sA .gitignore tells git which files to keep private.%s\n", cli.Dim, cli.Reset)
		fmt.Printf("  %sThis protects your database, API keys, and private notes%s\n", cli.Dim, cli.Reset)
		fmt.Printf("  %sfrom accidentally being shared if you use git.%s\n", cli.Dim, cli.Reset)
		if autoAccept || confirm("\n  Create .gitignore with privacy rules?", true) {
			if err := os.WriteFile(gitignorePath, []byte(sameGitignoreTemplate), 0o644); err != nil {
				fmt.Printf("  %s!%s Could not create .gitignore: %v\n",
					cli.Yellow, cli.Reset, err)
				return
			}
			fmt.Printf("  → Created .gitignore with privacy rules\n")
			fmt.Printf("    %s.same/data/ (system), _PRIVATE/ (secret), research/ (local-only)%s\n",
				cli.Dim, cli.Reset)
		}
		return
	}

	// .gitignore exists — check if SAME rules are already present
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == ".same/data/" || line == ".same/data" || line == ".same/" || line == ".same" {
			return // already has SAME rules
		}
	}

	fmt.Printf("\n  %sThis keeps SAME's database and private notes out of git.%s\n", cli.Dim, cli.Reset)
	if autoAccept || confirm("  Add SAME privacy rules to .gitignore?", true) {
		f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Printf("  %s!%s Could not update .gitignore: %v\n",
				cli.Yellow, cli.Reset, err)
			return
		}
		if _, err := f.WriteString("\n" + sameGitignoreTemplate); err != nil {
			fmt.Printf("  %s!%s Could not update .gitignore: %v\n",
				cli.Yellow, cli.Reset, err)
			_ = f.Close()
			return
		}
		if err := f.Close(); err != nil {
			fmt.Printf("  %s!%s Could not update .gitignore: %v\n",
				cli.Yellow, cli.Reset, err)
			return
		}
		fmt.Printf("  → Added SAME privacy rules to .gitignore\n")
		fmt.Printf("    %s.same/data/ (system), _PRIVATE/ (secret), research/ (local-only)%s\n",
			cli.Dim, cli.Reset)
	}
}

// createDefaultSameignore creates a .sameignore file with sensible defaults.
// Only creates the file if it doesn't already exist — never overwrites.
func createDefaultSameignore(vaultPath string) {
	sameignorePath := filepath.Join(vaultPath, ".sameignore")
	if _, err := os.Stat(sameignorePath); err == nil {
		return // already exists, don't overwrite
	}

	if err := indexer.WriteSameignore(vaultPath, indexer.DefaultSameignore); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] Could not create .sameignore: %v\n", err)
		return
	}
	fmt.Printf("  %s✓%s Created .sameignore with default exclusion patterns\n", cli.Green, cli.Reset)
}

// createSeedStructure creates the recommended vault directory structure.
// Only creates directories that don't already exist. Never overwrites.
// For new users (vibe-coder), each directory gets a one-line explanation.
func createSeedStructure(vaultPath string, experience ExperienceLevel) {
	type seedDir struct {
		path string
		hint string // shown only for vibe-coder level
	}
	seedDirs := []seedDir{
		{"sessions", "Session handoffs live here. Your AI writes what's in progress at end of session."},
		{"_PRIVATE", "Never indexed. Safe for credentials, personal notes, anything sensitive."},
		{"decisions", "Tracked decisions with rationale. Prevents relitigating settled choices."},
	}

	created := 0
	for _, d := range seedDirs {
		dir := filepath.Join(vaultPath, d.path)
		if _, err := os.Stat(dir); err == nil {
			continue // already exists
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		created++
		if experience == LevelVibeCoder {
			fmt.Printf("  %s✓%s %s/\n", cli.Green, cli.Reset, d.path)
			fmt.Printf("    %s%s%s\n", cli.Dim, d.hint, cli.Reset)
		}
	}

	if created > 0 && experience == LevelDev {
		fmt.Printf("  %s✓%s Created directories: sessions/, _PRIVATE/, decisions/\n",
			cli.Green, cli.Reset)
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
	reg.Default = name
	_ = reg.Save()
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
	return line == "y" || line == "yes" || line == "1"
}

// askExperienceLevel asks the user about their experience level.
func askExperienceLevel() ExperienceLevel {
	cli.Section("About You")
	fmt.Println()
	fmt.Printf("  %sWhat's your experience level?%s\n", cli.Bold, cli.Reset)
	fmt.Println()
	fmt.Printf("    %s1%s) I'm new to coding / using AI to build %s(recommended)%s\n",
		cli.Cyan, cli.Reset, cli.Dim, cli.Reset)
	fmt.Printf("       %s→ Full details, visual feedback box%s\n", cli.Dim, cli.Reset)
	fmt.Println()
	fmt.Printf("    %s2%s) I'm an experienced developer\n",
		cli.Cyan, cli.Reset)
	fmt.Printf("       %s→ Compact output, less noise%s\n", cli.Dim, cli.Reset)
	fmt.Println()
	fmt.Print("  Choice [1]: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return LevelVibeCoder
	}
	line = strings.TrimSpace(line)

	if line == "2" {
		fmt.Printf("\n  %s→ Developer mode: compact output%s\n", cli.Green, cli.Reset)
		fmt.Printf("    %sUse 'same display full' for the visual box, 'same display quiet' for silent%s\n", cli.Dim, cli.Reset)
		return LevelDev
	}

	fmt.Printf("\n  %s→ Full mode: visual feedback box showing what SAME surfaced%s\n", cli.Green, cli.Reset)
	fmt.Printf("    %sUse 'same display compact' for less output%s\n", cli.Dim, cli.Reset)
	return LevelVibeCoder
}

// generateConfigWithExperience writes the config file with experience-based defaults.
func generateConfigWithExperience(vaultPath string, experience ExperienceLevel) error {
	configPath := config.ConfigFilePath(vaultPath)
	if err := config.GenerateConfig(vaultPath); err != nil {
		return fmt.Errorf("generate config: %w", err)
	}

	// Set display mode based on experience
	displayMode := "full"
	if experience == LevelDev {
		displayMode = "compact"
	}
	if err := config.SetDisplayMode(vaultPath, displayMode); err != nil {
		return fmt.Errorf("set display mode: %w", err)
	}

	rel, _ := filepath.Rel(vaultPath, configPath)
	fmt.Printf("  → %s\n", rel)
	if experience == LevelDev {
		fmt.Printf("  → Display mode: compact %s(change with 'same display full')%s\n",
			cli.Dim, cli.Reset)
	} else {
		fmt.Printf("  → Display mode: full %s(change with 'same display compact')%s\n",
			cli.Dim, cli.Reset)
	}
	return nil
}

// runTestSearch performs a quick search to verify everything works.
// Returns the title of the first result, or empty string on failure.
func runTestSearch(vaultPath string) *store.SearchResult {
	// Open the database
	db, err := store.Open()
	if err != nil {
		return nil
	}
	defer db.Close()

	// Create embedding provider
	ec := config.EmbeddingProviderConfig()
	provCfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
	}
	// For ollama provider, use the legacy [ollama] URL if no base_url is set
	if (provCfg.Provider == "ollama" || provCfg.Provider == "") && provCfg.BaseURL == "" {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			return nil
		}
		provCfg.BaseURL = ollamaURL
	}
	provider, err := embedding.NewProvider(provCfg)
	if err != nil {
		return nil
	}

	// Embed a test query
	vec, err := provider.GetQueryEmbedding("how does SAME work")
	if err != nil {
		return nil
	}

	// Search
	results, err := db.VectorSearch(vec, store.SearchOptions{TopK: 1})
	if err != nil || len(results) == 0 {
		return nil
	}

	return &results[0]
}

// ProjectContext holds detected project characteristics.
type ProjectContext struct {
	Language   string   // e.g. "Go", "TypeScript", "Python"
	LangFile   string   // e.g. "go.mod", "package.json"
	AITools    []string // e.g. "Claude Code (.claude/)", "Cursor (.cursorrules)"
	Docs       []string // e.g. "CLAUDE.md (2.9 KB)", "README.md (4.1 KB)"
	HasGit     bool
	GitBranch  string
	GitCommits int
}

// scanProjectContext detects the language, AI tools, docs, and git state of the current directory.
func scanProjectContext(dir string) *ProjectContext {
	ctx := &ProjectContext{}

	// Detect language
	langFiles := []struct {
		file string
		lang string
	}{
		{"go.mod", "Go"},
		{"package.json", "TypeScript/Node"},
		{"tsconfig.json", "TypeScript"},
		{"Cargo.toml", "Rust"},
		{"pyproject.toml", "Python"},
		{"requirements.txt", "Python"},
		{"setup.py", "Python"},
		{"Gemfile", "Ruby"},
		{"pom.xml", "Java"},
		{"build.gradle", "Java/Kotlin"},
		{"composer.json", "PHP"},
		{"mix.exs", "Elixir"},
	}
	for _, lf := range langFiles {
		if _, err := os.Stat(filepath.Join(dir, lf.file)); err == nil {
			if ctx.Language == "" {
				ctx.Language = lf.lang
				ctx.LangFile = lf.file
			}
			break
		}
	}

	// Detect AI tools
	aiMarkers := []struct {
		path  string
		label string
	}{
		{".claude", "Claude Code (.claude/)"},
		{".cursorrules", "Cursor (.cursorrules)"},
		{".cursor", "Cursor (.cursor/)"},
		{".windsurfrules", "Windsurf (.windsurfrules)"},
		{".github/copilot-instructions.md", "Copilot (.github/copilot-instructions.md)"},
		{".aider.conf.yml", "Aider (.aider.conf.yml)"},
	}
	for _, m := range aiMarkers {
		if _, err := os.Stat(filepath.Join(dir, m.path)); err == nil {
			ctx.AITools = append(ctx.AITools, m.label)
		}
	}

	// Detect docs with sizes
	docFiles := []string{
		"CLAUDE.md", "README.md", "ARCHITECTURE.md", "DESIGN.md",
		"CONTRIBUTING.md", "CHANGELOG.md", ".cursorrules", ".windsurfrules",
	}
	for _, name := range docFiles {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			sizeKB := float64(info.Size()) / 1024
			ctx.Docs = append(ctx.Docs, fmt.Sprintf("%s (%.1f KB)", name, sizeKB))
		}
	}

	// Detect git
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		ctx.HasGit = true
		if out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
			ctx.GitBranch = strings.TrimSpace(string(out))
		}
		if out, err := exec.Command("git", "-C", dir, "rev-list", "--count", "HEAD").Output(); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
				ctx.GitCommits = n
			}
		}
	}

	// Only return if we found something interesting
	if ctx.Language == "" && len(ctx.AITools) == 0 && len(ctx.Docs) == 0 && !ctx.HasGit {
		return nil
	}
	return ctx
}

// showProjectContext prints the detected project context during init.
func showProjectContext(ctx *ProjectContext) {
	if ctx == nil {
		return
	}
	fmt.Println()
	fmt.Printf("  %sDetected project context:%s\n", cli.Bold, cli.Reset)
	if ctx.Language != "" {
		fmt.Printf("    Language: %s (%s found)\n", ctx.Language, ctx.LangFile)
	}
	if len(ctx.AITools) > 0 {
		fmt.Printf("    AI tools: %s\n", strings.Join(ctx.AITools, ", "))
	}
	if len(ctx.Docs) > 0 {
		fmt.Printf("    Existing docs: %s\n", strings.Join(ctx.Docs, ", "))
	}
	if ctx.HasGit {
		gitInfo := "yes"
		if ctx.GitBranch != "" {
			gitInfo += fmt.Sprintf(" (%s branch", ctx.GitBranch)
			if ctx.GitCommits > 0 {
				gitInfo += fmt.Sprintf(", %d commits", ctx.GitCommits)
			}
			gitInfo += ")"
		}
		fmt.Printf("    Git: %s\n", gitInfo)
	}
	fmt.Println()
}

// showSmartSeedHints prints contextual seed suggestions based on detected project.
func showSmartSeedHints(ctx *ProjectContext) {
	if ctx == nil {
		return
	}

	// Cross-tool memory hint
	hasClaude := false
	hasCursor := false
	for _, t := range ctx.AITools {
		if strings.Contains(t, "Claude") {
			hasClaude = true
		}
		if strings.Contains(t, "Cursor") {
			hasCursor = true
		}
	}
	if hasClaude && hasCursor {
		fmt.Printf("  %s✦%s Both Cursor and Claude Code detected. They'll share this vault automatically.\n",
			cli.Cyan, cli.Reset)
		fmt.Println()
	}

	// Language-specific seed hints
	switch ctx.Language {
	case "TypeScript/Node", "TypeScript":
		fmt.Printf("  %sTip:%s TypeScript project detected. Try: %ssame seed install typescript-fullstack-patterns%s\n",
			cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
	case "Go":
		fmt.Printf("  %sTip:%s Go project detected. Seed vaults like %sapi-design-patterns%s and %sdevops-runbooks%s pair well.\n",
			cli.Bold, cli.Reset, cli.Cyan, cli.Reset, cli.Cyan, cli.Reset)
	case "Python":
		fmt.Printf("  %sTip:%s Python project detected. Try: %ssame seed install ai-agent-architecture%s for AI/ML patterns.\n",
			cli.Bold, cli.Reset, cli.Cyan, cli.Reset)
	}
}

// showModelAwareness shows a tip about model quality when using smaller/local models.
// Only displayed for non-OpenAI, non-none providers where the user might benefit
// from knowing that larger models produce richer output.
// showConfigurationSummary prints a boxed summary of what was auto-configured
// during init based on model detection.
func showConfigurationSummary(det *ollamaDetection, embedProvider string, useEmbeddings bool) {
	if det == nil || !det.Running {
		return
	}

	fmt.Println()
	fmt.Printf("  %s── Configuration ─────────────────────%s\n", cli.Dim, cli.Reset)

	// Embedding line
	if useEmbeddings && det.BestEmbedding != "" {
		source := det.EmbeddingSource
		if source == "" {
			source = "default"
		}
		fmt.Printf("  Embedding: %s%s%s %s(%s)%s\n",
			cli.Bold, det.BestEmbedding, cli.Reset, cli.Dim, source, cli.Reset)
	} else if useEmbeddings {
		fmt.Printf("  Embedding: %s%s%s\n", cli.Bold, config.EmbeddingModel, cli.Reset)
	} else {
		fmt.Printf("  Embedding: %snone (keyword-only)%s\n", cli.Dim, cli.Reset)
	}

	// Graph line
	graphMode := config.GraphLLMMode()
	switch graphMode {
	case "local-only":
		chatName := det.BestChat
		if idx := strings.Index(chatName, ":"); idx > 0 {
			chatName = chatName[:idx]
		}
		fmt.Printf("  Graph:     LLM-enhanced %s(%s detected)%s\n",
			cli.Dim, chatName, cli.Reset)
	case "on":
		fmt.Printf("  Graph:     LLM-enhanced\n")
	default:
		if len(det.ChatModels) > 0 {
			fmt.Printf("  Graph:     regex-only %s(enable with 'same graph enable')%s\n",
				cli.Dim, cli.Reset)
		} else {
			fmt.Printf("  Graph:     regex-only\n")
		}
	}

	// Search line
	if useEmbeddings {
		fmt.Printf("  Search:    semantic\n")
	} else {
		fmt.Printf("  Search:    keyword-only\n")
	}
	fmt.Println()
	fmt.Printf("  Everything configured. Run %ssame demo%s to try it.\n", cli.Bold, cli.Reset)
	fmt.Println()
}

func showModelAwareness(embedProvider string) {
	// Don't show for OpenAI users (they're already on capable models)
	// Don't show for none (no embeddings at all)
	if embedProvider == "openai" || embedProvider == "none" {
		return
	}

	// Check if the user has Claude or GPT-4 available via AI tool detection.
	// If they have .claude/ or known large-model configs, skip the hint.
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	// Users with Claude Code or Cursor likely have access to strong models
	for _, marker := range []string{".claude", ".cursor"} {
		if _, err := os.Stat(filepath.Join(cwd, marker)); err == nil {
			return // they're using a strong model, no need for the tip
		}
	}

	fmt.Println()
	fmt.Printf("  %sTip:%s Larger models (Claude Opus, GPT-4) produce richer handoffs\n",
		cli.Bold, cli.Reset)
	fmt.Printf("  and graph extraction. Your current setup works great for search\n")
	fmt.Printf("  and decisions.\n")
}

// detectProjectDocs scans a directory for common project documentation files.
// Returns relative paths of found docs, or nil if none found.
func detectProjectDocs(dir string) []string {
	// Known documentation files (check root)
	rootFiles := []string{
		"README.md", "readme.md", "Readme.md",
		"ARCHITECTURE.md", "DESIGN.md", "CONTRIBUTING.md",
		"CHANGELOG.md", "CLAUDE.md",
		".cursorrules", ".windsurfrules",
	}

	// Known documentation directories
	docDirs := []string{
		"docs", "documentation", "doc",
		"ADR", "adr",
	}

	var found []string
	seen := make(map[string]bool)

	// Check root-level doc files
	for _, name := range rootFiles {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if !seen[name] {
				seen[name] = true
				found = append(found, name)
			}
		}
	}

	// Check doc directories (list .md files inside)
	for _, docDir := range docDirs {
		dirPath := filepath.Join(dir, docDir)
		info, err := os.Stat(dirPath)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			relPath := filepath.Join(docDir, e.Name())
			if !seen[relPath] {
				seen[relPath] = true
				found = append(found, relPath)
			}
		}
	}

	return found
}
