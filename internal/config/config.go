// Package config provides configuration for the SAME binary.
// Loads from: CLI flags > env vars > .same/config.toml > built-in defaults.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Embedding model settings.
const (
	EmbeddingModel = "nomic-embed-text"
	EmbeddingDim   = 768
)

// Indexing settings.
const (
	ChunkTokenThreshold = 6000 // chunk notes longer than ~6K chars by H2 headings
	MaxEmbedChars       = 7500 // nomic-embed-text context limit ~8192 tokens
	MaxSnippetLength    = 500
)

// Memory engine settings.
const (
	SessionLogTable           = "session_log"
	ContextUsageTable         = "context_usage"
	MaxContextInjectionTokens = 1000
	ContextSurfacingMinChars  = 20
)

// Config holds all SAME configuration, loaded from TOML + env + flags.
type Config struct {
	Vault  VaultConfig  `toml:"vault"`
	Ollama OllamaConfig `toml:"ollama"`
	Memory MemoryConfig `toml:"memory"`
	Hooks  HooksConfig  `toml:"hooks"`
}

// VaultConfig holds vault-related settings.
type VaultConfig struct {
	Path        string   `toml:"path"`
	SkipDirs    []string `toml:"skip_dirs"`
	HandoffDir  string   `toml:"handoff_dir"`
	DecisionLog string   `toml:"decision_log"`
}

// OllamaConfig holds Ollama connection settings.
type OllamaConfig struct {
	URL   string `toml:"url"`
	Model string `toml:"model"`
}

// MemoryConfig holds memory engine tuning parameters.
type MemoryConfig struct {
	MaxTokenBudget     int     `toml:"max_token_budget"`
	MaxResults         int     `toml:"max_results"`
	DistanceThreshold  float64 `toml:"distance_threshold"`
	CompositeThreshold float64 `toml:"composite_threshold"`
}

// HooksConfig controls which hooks are enabled.
type HooksConfig struct {
	ContextSurfacing  bool `toml:"context_surfacing"`
	DecisionExtractor bool `toml:"decision_extractor"`
	HandoffGenerator  bool `toml:"handoff_generator"`
	StalenessCheck    bool `toml:"staleness_check"`
}

// DefaultConfig returns a Config with all built-in defaults.
func DefaultConfig() *Config {
	return &Config{
		Vault: VaultConfig{
			HandoffDir:  "sessions",
			DecisionLog: "decisions.md",
		},
		Ollama: OllamaConfig{
			URL:   "http://localhost:11434",
			Model: EmbeddingModel,
		},
		Memory: MemoryConfig{
			MaxTokenBudget:     800,
			MaxResults:         2,
			DistanceThreshold:  16.2,
			CompositeThreshold: 0.65,
		},
		Hooks: HooksConfig{
			ContextSurfacing:  true,
			DecisionExtractor: true,
			HandoffGenerator:  true,
			StalenessCheck:    true,
		},
	}
}

// LoadConfig merges all configuration sources: defaults < TOML file < env vars.
// CLI flags (VaultOverride) are handled separately by the existing VaultPath() logic.
func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	// Try to load TOML config file
	configPath := findConfigFile()
	if configPath != "" {
		if _, err := toml.DecodeFile(configPath, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", configPath, err)
		}
	}

	// Environment variables override TOML values
	if v := os.Getenv("VAULT_PATH"); v != "" {
		cfg.Vault.Path = v
	}
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		cfg.Ollama.URL = v
	}
	if v := os.Getenv("SAME_HANDOFF_DIR"); v != "" {
		cfg.Vault.HandoffDir = v
	}
	if v := os.Getenv("SAME_DECISION_LOG"); v != "" {
		cfg.Vault.DecisionLog = v
	}
	if v := os.Getenv("SAME_SKIP_DIRS"); v != "" {
		for _, d := range strings.Split(v, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				cfg.Vault.SkipDirs = append(cfg.Vault.SkipDirs, d)
			}
		}
	}

	return cfg, nil
}

// findConfigFile looks for .same/config.toml starting from vault path, then CWD.
func findConfigFile() string {
	// Check vault path first (if already resolved)
	if vp := resolveVaultForConfig(); vp != "" {
		p := filepath.Join(vp, ".same", "config.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Check CWD
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, ".same", "config.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

// resolveVaultForConfig resolves the vault path for config loading without
// calling VaultPath() to avoid circular dependency with config loading.
func resolveVaultForConfig() string {
	if VaultOverride != "" {
		reg := LoadRegistry()
		if resolved := reg.ResolveVault(VaultOverride); resolved != "" {
			return resolved
		}
		return VaultOverride
	}
	if v := os.Getenv("VAULT_PATH"); v != "" {
		return v
	}
	return ""
}

// ConfigFilePath returns the path where the config file should be written
// for the given vault path.
func ConfigFilePath(vaultPath string) string {
	return filepath.Join(vaultPath, ".same", "config.toml")
}

// GenerateConfig writes a default .same/config.toml with comments.
// If vaultPath is provided, it's included in the generated config.
func GenerateConfig(vaultPath string) error {
	configPath := ConfigFilePath(vaultPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	content := generateTOMLContent(vaultPath)
	return os.WriteFile(configPath, []byte(content), 0o644)
}

func generateTOMLContent(vaultPath string) string {
	var b strings.Builder
	b.WriteString("# SAME Configuration\n")
	b.WriteString("# https://github.com/sgx-labs/statelessagent\n")
	b.WriteString("#\n")
	b.WriteString("# Priority: CLI flags > environment variables > this file > built-in defaults\n")
	b.WriteString("# Environment variables: VAULT_PATH, OLLAMA_URL, SAME_HANDOFF_DIR,\n")
	b.WriteString("#   SAME_DECISION_LOG, SAME_SKIP_DIRS, SAME_DATA_DIR\n\n")

	b.WriteString("[vault]\n")
	if vaultPath != "" {
		b.WriteString(fmt.Sprintf("path = %q\n", vaultPath))
	} else {
		b.WriteString("# path = \"/path/to/your/notes\"  # auto-detected if unset\n")
	}
	b.WriteString("# skip_dirs = [\".venv\", \"build\"]  # added to built-in exclusions\n")
	b.WriteString("handoff_dir = \"sessions\"\n")
	b.WriteString("decision_log = \"decisions.md\"\n\n")

	b.WriteString("[ollama]\n")
	b.WriteString("url = \"http://localhost:11434\"\n")
	b.WriteString("model = \"nomic-embed-text\"\n\n")

	b.WriteString("[memory]\n")
	b.WriteString("max_token_budget = 800\n")
	b.WriteString("max_results = 2\n")
	b.WriteString("distance_threshold = 16.2\n")
	b.WriteString("composite_threshold = 0.65\n\n")

	b.WriteString("[hooks]\n")
	b.WriteString("context_surfacing = true\n")
	b.WriteString("decision_extractor = true\n")
	b.WriteString("handoff_generator = true\n")
	b.WriteString("staleness_check = true\n")

	return b.String()
}

// ShowConfig returns the current effective configuration as TOML.
func ShowConfig() string {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Sprintf("# Error loading config: %v\n", err)
	}

	// Fill in the effective vault path if not explicitly set
	if cfg.Vault.Path == "" {
		cfg.Vault.Path = VaultPath()
	}

	var b strings.Builder
	b.WriteString("# Effective SAME configuration (merged from all sources)\n\n")
	enc := toml.NewEncoder(&b)
	enc.Encode(cfg)
	return b.String()
}

// --- Existing API (preserved for backward compatibility) ---

// HandoffDirectory returns the directory for session handoff notes.
func HandoffDirectory() string {
	if v := os.Getenv("SAME_HANDOFF_DIR"); v != "" {
		return v
	}
	if cfg := loadConfigSafe(); cfg != nil && cfg.Vault.HandoffDir != "" {
		return cfg.Vault.HandoffDir
	}
	return "sessions"
}

// DecisionLogPath returns the path (relative to vault root) for the decision log.
func DecisionLogPath() string {
	if v := os.Getenv("SAME_DECISION_LOG"); v != "" {
		return v
	}
	if cfg := loadConfigSafe(); cfg != nil && cfg.Vault.DecisionLog != "" {
		return cfg.Vault.DecisionLog
	}
	return "decisions.md"
}

// MemoryMaxResults returns the configured maximum number of results to surface.
func MemoryMaxResults() int {
	if cfg := loadConfigSafe(); cfg != nil && cfg.Memory.MaxResults > 0 {
		return cfg.Memory.MaxResults
	}
	return 2
}

// MemoryDistanceThreshold returns the configured maximum L2 distance threshold.
func MemoryDistanceThreshold() float64 {
	if cfg := loadConfigSafe(); cfg != nil && cfg.Memory.DistanceThreshold > 0 {
		return cfg.Memory.DistanceThreshold
	}
	return 16.2
}

// MemoryCompositeThreshold returns the configured minimum composite score.
func MemoryCompositeThreshold() float64 {
	if cfg := loadConfigSafe(); cfg != nil && cfg.Memory.CompositeThreshold > 0 {
		return cfg.Memory.CompositeThreshold
	}
	return 0.65
}

// MemoryMaxTokenBudget returns the configured maximum token budget for context injection.
func MemoryMaxTokenBudget() int {
	if cfg := loadConfigSafe(); cfg != nil && cfg.Memory.MaxTokenBudget > 0 {
		return cfg.Memory.MaxTokenBudget
	}
	return 800
}

// loadConfigSafe loads config without risking recursion. Returns nil on error.
func loadConfigSafe() *Config {
	cfg, err := LoadConfig()
	if err != nil {
		return nil
	}
	return cfg
}

// defaultSkipDirs are directories to skip during vault walks.
// SECURITY: _PRIVATE contains client-sensitive content and must never be indexed
// or auto-surfaced. Access to _PRIVATE requires explicit MCP tool calls.
var defaultSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".smart-env":   true,
	".obsidian":    true,
	".logseq":      true,
	".same":        true,
	".claude":      true,
	".trash":       true,
	"_PRIVATE":     true,
}

// SkipDirs returns the set of directories to skip during vault walks.
var SkipDirs = buildSkipDirs()

func buildSkipDirs() map[string]bool {
	dirs := make(map[string]bool)
	for k, v := range defaultSkipDirs {
		dirs[k] = v
	}
	if extra := os.Getenv("SAME_SKIP_DIRS"); extra != "" {
		for _, d := range strings.Split(extra, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				dirs[d] = true
			}
		}
	}
	return dirs
}

// RebuildSkipDirs rebuilds the SkipDirs map, incorporating config file settings.
// Should be called after config is loaded if skip_dirs is set in TOML.
func RebuildSkipDirs(extra []string) {
	dirs := make(map[string]bool)
	for k, v := range defaultSkipDirs {
		dirs[k] = v
	}
	if envExtra := os.Getenv("SAME_SKIP_DIRS"); envExtra != "" {
		for _, d := range strings.Split(envExtra, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				dirs[d] = true
			}
		}
	}
	for _, d := range extra {
		d = strings.TrimSpace(d)
		if d != "" {
			dirs[d] = true
		}
	}
	SkipDirs = dirs
}

// VaultPath returns the vault root directory.
// SECURITY: Validates the path is a reasonable vault root (not / or other
// dangerous top-level paths that would cause the indexer to walk the entire filesystem).
func VaultPath() string {
	var path string
	if v := os.Getenv("VAULT_PATH"); v != "" {
		path = v
	} else if cfg := loadConfigSafe(); cfg != nil && cfg.Vault.Path != "" {
		path = cfg.Vault.Path
	} else {
		path = defaultVaultPath()
	}
	if path != "" {
		path = validateVaultPath(path)
	}
	return path
}

// validateVaultPath rejects vault paths that are too broad (e.g., /, /home, /Users).
func validateVaultPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	// Block filesystem roots and shallow system directories
	dangerous := []string{"/", "/home", "/Users", "/tmp", "/var", "/etc", "/opt"}
	for _, d := range dangerous {
		if abs == d {
			fmt.Fprintf(os.Stderr, "WARNING: VAULT_PATH=%q is too broad, ignoring.\n", abs)
			return ""
		}
	}
	return path
}

// OllamaURL returns the validated Ollama API URL.
// Panics if the URL does not point to localhost.
func OllamaURL() string {
	raw := os.Getenv("OLLAMA_URL")
	if raw == "" {
		if cfg := loadConfigSafe(); cfg != nil && cfg.Ollama.URL != "" {
			raw = cfg.Ollama.URL
		} else {
			raw = "http://localhost:11434"
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("invalid OLLAMA_URL: %v", err))
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		panic(fmt.Sprintf("OLLAMA_URL must point to localhost for security. Got: %s", host))
	}
	return raw
}

// DBPath returns the path to the SQLite database file.
func DBPath() string {
	return filepath.Join(DataDir(), "vault.db")
}

// DataDir returns the data directory for the same binary.
func DataDir() string {
	if v := os.Getenv("SAME_DATA_DIR"); v != "" {
		return v
	}
	return filepath.Join(VaultPath(), ".same", "data")
}

// VaultRegistry holds registered vault paths with aliases.
type VaultRegistry struct {
	Vaults  map[string]string `json:"vaults"`  // alias -> path
	Default string            `json:"default"`  // alias of default vault
}

// RegistryPath returns the path to the vault registry file.
func RegistryPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "same", "vaults.json")
}

// LoadRegistry loads or creates the vault registry.
func LoadRegistry() *VaultRegistry {
	data, err := os.ReadFile(RegistryPath())
	if err != nil {
		return &VaultRegistry{Vaults: make(map[string]string)}
	}
	var reg VaultRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return &VaultRegistry{Vaults: make(map[string]string)}
	}
	if reg.Vaults == nil {
		reg.Vaults = make(map[string]string)
	}
	return &reg
}

// Save writes the registry to disk.
func (r *VaultRegistry) Save() error {
	path := RegistryPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ResolveVault resolves a vault alias to a path. Returns empty string if not found.
func (r *VaultRegistry) ResolveVault(alias string) string {
	if p, ok := r.Vaults[alias]; ok {
		return p
	}
	// Maybe it's already a path
	if info, err := os.Stat(alias); err == nil && info.IsDir() {
		return alias
	}
	return ""
}

// VaultOverride is set by the --vault global flag.
var VaultOverride string

// VaultMarkers are dotfiles/directories that indicate a knowledge base root.
// Checked in priority order: SAME's own marker first, then common tools.
var VaultMarkers = []string{".same", ".obsidian", ".logseq", ".foam", ".dendron"}

func defaultVaultPath() string {
	// Check --vault flag override first
	if VaultOverride != "" {
		reg := LoadRegistry()
		if resolved := reg.ResolveVault(VaultOverride); resolved != "" {
			return resolved
		}
		// Treat as direct path
		return VaultOverride
	}

	// Check registry default
	reg := LoadRegistry()
	if reg.Default != "" {
		if p, ok := reg.Vaults[reg.Default]; ok {
			return p
		}
	}

	// Auto-detect: check CWD for any known marker
	if cwd, err := os.Getwd(); err == nil {
		for _, marker := range VaultMarkers {
			if _, err := os.Stat(filepath.Join(cwd, marker)); err == nil {
				return cwd
			}
		}
	}

	// Walk up from binary location looking for any marker
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 5; i++ {
			for _, marker := range VaultMarkers {
				if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
					return dir
				}
			}
			dir = filepath.Dir(dir)
		}
	}

	// No vault found — return empty string (caller should show helpful error)
	return ""
}
