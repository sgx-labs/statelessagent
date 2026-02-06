// Package config provides configuration for the SAME binary.
// Loads from: CLI flags > env vars > .same/config.toml > built-in defaults.
package config

import (
	"bytes"
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
	Vault     VaultConfig     `toml:"vault"`
	Ollama    OllamaConfig    `toml:"ollama"`
	Embedding EmbeddingConfig `toml:"embedding"`
	Memory    MemoryConfig    `toml:"memory"`
	Hooks     HooksConfig     `toml:"hooks"`
	Display   DisplayConfig   `toml:"display"`
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

// EmbeddingConfig holds embedding provider settings.
type EmbeddingConfig struct {
	Provider   string `toml:"provider"`   // "ollama" (default), "openai"
	Model      string `toml:"model"`      // model name (provider-specific default if empty)
	APIKey     string `toml:"api_key"`    // API key for cloud providers
	Dimensions int    `toml:"dimensions"` // vector dimensions (0 = provider default)
}

// HooksConfig controls which hooks are enabled.
type HooksConfig struct {
	ContextSurfacing  bool `toml:"context_surfacing"`
	DecisionExtractor bool `toml:"decision_extractor"`
	HandoffGenerator  bool `toml:"handoff_generator"`
	StalenessCheck    bool `toml:"staleness_check"`
}

// DisplayConfig controls visual output settings.
type DisplayConfig struct {
	Mode string `toml:"mode"` // "full" (default), "compact", "quiet"
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
		Embedding: EmbeddingConfig{
			Provider: "ollama",
			Model:    "", // uses provider-specific default
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
		Display: DisplayConfig{
			Mode: "full",
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

	// Embedding provider overrides
	if v := os.Getenv("SAME_EMBED_PROVIDER"); v != "" {
		cfg.Embedding.Provider = v
	}
	if v := os.Getenv("SAME_EMBED_MODEL"); v != "" {
		cfg.Embedding.Model = v
	}
	if v := os.Getenv("SAME_EMBED_API_KEY"); v != "" {
		cfg.Embedding.APIKey = v
	}
	// Also check OPENAI_API_KEY as a convenience fallback
	if cfg.Embedding.APIKey == "" && cfg.Embedding.Provider == "openai" {
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			cfg.Embedding.APIKey = v
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

	b.WriteString("[embedding]\n")
	b.WriteString("# Embedding provider: \"ollama\" (default, local) or \"openai\" (cloud)\n")
	b.WriteString("provider = \"ollama\"\n")
	b.WriteString("# model = \"nomic-embed-text\"    # provider-specific model name\n")
	b.WriteString("# api_key = \"\"                  # required for cloud providers\n")
	b.WriteString("#                               # or set SAME_EMBED_API_KEY / OPENAI_API_KEY\n")
	b.WriteString("# dimensions = 0                # 0 = use provider default\n\n")

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

// EmbeddingProvider returns the configured embedding provider name.
func EmbeddingProvider() string {
	if v := os.Getenv("SAME_EMBED_PROVIDER"); v != "" {
		return v
	}
	if cfg := loadConfigSafe(); cfg != nil && cfg.Embedding.Provider != "" {
		return cfg.Embedding.Provider
	}
	return "ollama"
}

// EmbeddingProviderConfig returns the full embedding provider configuration.
func EmbeddingProviderConfig() EmbeddingConfig {
	cfg := loadConfigSafe()
	if cfg == nil {
		return EmbeddingConfig{Provider: "ollama"}
	}

	ec := cfg.Embedding
	if ec.Provider == "" {
		ec.Provider = "ollama"
	}

	// Env vars take precedence (already merged in LoadConfig, but handle
	// the case where loadConfigSafe is called without full LoadConfig)
	if v := os.Getenv("SAME_EMBED_PROVIDER"); v != "" {
		ec.Provider = v
	}
	if v := os.Getenv("SAME_EMBED_MODEL"); v != "" {
		ec.Model = v
	}
	if v := os.Getenv("SAME_EMBED_API_KEY"); v != "" {
		ec.APIKey = v
	}
	if ec.APIKey == "" && ec.Provider == "openai" {
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			ec.APIKey = v
		}
	}

	// For ollama provider, merge the legacy [ollama] section if embedding model is unset
	if ec.Provider == "ollama" && ec.Model == "" {
		if cfg.Ollama.Model != "" {
			ec.Model = cfg.Ollama.Model
		}
	}

	return ec
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

// SafeVaultSubpath resolves a relative path within the vault and validates
// that the result stays inside the vault root. Returns the absolute path and true
// if valid, or empty string and false if the path escapes the vault boundary.
// SECURITY: Prevents SAME_HANDOFF_DIR or SAME_DECISION_LOG from redirecting
// file writes outside the vault via path traversal (e.g., "../../etc").
func SafeVaultSubpath(relativePath string) (string, bool) {
	vaultRoot := VaultPath()
	if vaultRoot == "" {
		return "", false
	}
	absVault, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(filepath.Join(vaultRoot, filepath.FromSlash(relativePath)))
	if err != nil {
		return "", false
	}
	// The resolved path must be under the vault root
	if !strings.HasPrefix(absPath, absVault+string(filepath.Separator)) && absPath != absVault {
		return "", false
	}
	return absPath, true
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

// DisplayMode returns the current display mode from config.
// Returns "full" (default), "compact", or "quiet".
func DisplayMode() string {
	cfg := loadConfigSafe()
	if cfg == nil || cfg.Display.Mode == "" {
		return "full"
	}
	return cfg.Display.Mode
}

// Profile represents a preset configuration for memory engine behavior.
type Profile struct {
	Name               string
	Description        string
	MaxResults         int
	DistanceThreshold  float64
	CompositeThreshold float64
	TokenWarning       string // warning about token usage
}

// BuiltinProfiles defines the available profile presets.
var BuiltinProfiles = map[string]Profile{
	"precise": {
		Name:               "precise",
		Description:        "Fewer, highly relevant results",
		MaxResults:         2,
		DistanceThreshold:  14.0,
		CompositeThreshold: 0.75,
		TokenWarning:       "Uses fewer tokens per query",
	},
	"balanced": {
		Name:               "balanced",
		Description:        "Default balance of relevance and coverage",
		MaxResults:         2,
		DistanceThreshold:  16.2,
		CompositeThreshold: 0.65,
		TokenWarning:       "",
	},
	"broad": {
		Name:               "broad",
		Description:        "More context, casts a wider net",
		MaxResults:         4,
		DistanceThreshold:  18.0,
		CompositeThreshold: 0.55,
		TokenWarning:       "Uses ~2x more tokens per query",
	},
}

// CurrentProfile returns the name of the current profile based on config values,
// or "custom" if values don't match any builtin profile.
func CurrentProfile() string {
	cfg := loadConfigSafe()
	if cfg == nil {
		return "balanced"
	}

	for name, p := range BuiltinProfiles {
		if cfg.Memory.MaxResults == p.MaxResults &&
			cfg.Memory.DistanceThreshold == p.DistanceThreshold &&
			cfg.Memory.CompositeThreshold == p.CompositeThreshold {
			return name
		}
	}
	return "custom"
}

// SetProfile applies a profile's settings to the config file.
func SetProfile(vaultPath, profileName string) error {
	profile, ok := BuiltinProfiles[profileName]
	if !ok {
		return fmt.Errorf("unknown profile: %s (available: precise, balanced, broad)", profileName)
	}

	cfgPath := ConfigFilePath(vaultPath)

	// Load existing config or create default
	cfg, err := LoadConfig()
	if err != nil {
		cfg = DefaultConfig()
	}

	// Apply profile settings
	cfg.Memory.MaxResults = profile.MaxResults
	cfg.Memory.DistanceThreshold = profile.DistanceThreshold
	cfg.Memory.CompositeThreshold = profile.CompositeThreshold

	// Marshal to TOML
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	// Ensure directory exists
	os.MkdirAll(filepath.Dir(cfgPath), 0o755)

	// Write file
	return os.WriteFile(cfgPath, buf.Bytes(), 0o644)
}

// SetDisplayMode updates the display mode in the config file.
func SetDisplayMode(vaultPath, mode string) error {
	cfgPath := ConfigFilePath(vaultPath)

	// Load existing config or create default
	cfg, err := LoadConfig()
	if err != nil {
		cfg = DefaultConfig()
	}

	// Update display mode
	cfg.Display.Mode = mode

	// Marshal to TOML
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	// Ensure directory exists
	os.MkdirAll(filepath.Dir(cfgPath), 0o755)

	// Write file
	return os.WriteFile(cfgPath, buf.Bytes(), 0o644)
}

// VerboseEnabled returns true when verbose monitoring is active.
func VerboseEnabled() bool {
	if os.Getenv("SAME_VERBOSE") != "" {
		return true
	}
	_, err := os.Stat(filepath.Join(DataDir(), "verbose"))
	return err == nil
}

// MachineName returns the user-configured machine name, or falls back to hostname.
func MachineName() string {
	cfg := loadUserConfig()
	if cfg.MachineName != "" {
		return cfg.MachineName
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// SetMachineName saves the user's preferred machine name.
func SetMachineName(name string) error {
	cfg := loadUserConfig()
	cfg.MachineName = name
	return saveUserConfig(cfg)
}

// userConfig holds user-level preferences (not vault-specific).
type userConfig struct {
	MachineName string           `json:"machine_name,omitempty"`
	Guard       *json.RawMessage `json:"guard,omitempty"`
}

func userConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "same", "config.json")
}

func loadUserConfig() userConfig {
	data, err := os.ReadFile(userConfigPath())
	if err != nil {
		return userConfig{}
	}
	var cfg userConfig
	json.Unmarshal(data, &cfg)
	return cfg
}

func saveUserConfig(cfg userConfig) error {
	path := userConfigPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
