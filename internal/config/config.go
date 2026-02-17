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
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Embedding model settings.
const (
	EmbeddingModel = "nomic-embed-text"
)

// EmbeddingDim returns the configured embedding dimensions. It checks the
// embedding provider config for an explicit dimensions setting, then falls
// back to provider-specific defaults. This replaces the old hard-coded 768
// constant so that non-Ollama providers (e.g., OpenAI at 1536) work correctly.
func EmbeddingDim() int {
	ec := EmbeddingProviderConfig()
	if ec.Dimensions > 0 {
		return ec.Dimensions
	}
	// Provider-specific defaults (must match embedding package defaults)
	switch ec.Provider {
	case "openai":
		model := ec.Model
		if model == "" {
			model = "text-embedding-3-small"
		}
		switch model {
		case "text-embedding-3-small":
			return 1536
		case "text-embedding-3-large":
			return 3072
		case "text-embedding-ada-002":
			return 1536
		default:
			return 1536
		}
	default: // "ollama" or ""
		model := ec.Model
		if model == "" {
			model = EmbeddingModel
		}
		switch model {
		case "nomic-embed-text":
			return 768
		case "mxbai-embed-large":
			return 1024
		case "all-minilm":
			return 384
		case "snowflake-arctic-embed":
			return 1024
		case "snowflake-arctic-embed2":
			return 768
		case "embeddinggemma":
			return 768
		case "qwen3-embedding":
			return 1024
		case "nomic-embed-text-v2-moe":
			return 768
		case "bge-m3":
			return 1024
		default:
			return 768
		}
	}
}

// ModelInfo describes a known embedding model.
type ModelInfo struct {
	Name        string
	Dims        int
	Provider    string // "ollama", "openai"
	Description string
}

// KnownModels lists supported embedding models with metadata.
var KnownModels = []ModelInfo{
	{"nomic-embed-text", 768, "ollama", "Default. Great balance of quality and speed"},
	{"snowflake-arctic-embed2", 768, "ollama", "Best retrieval in its size class"},
	{"mxbai-embed-large", 1024, "ollama", "Highest overall MTEB average"},
	{"all-minilm", 384, "ollama", "Lightweight (~90MB). Good for constrained hardware"},
	{"snowflake-arctic-embed", 1024, "ollama", "v1 large model"},
	{"embeddinggemma", 768, "ollama", "Google's Gemma-based embeddings"},
	{"qwen3-embedding", 1024, "ollama", "Qwen3 with 32K context"},
	{"nomic-embed-text-v2-moe", 768, "ollama", "MoE upgrade from nomic"},
	{"bge-m3", 1024, "ollama", "Multilingual (BAAI)"},
	{"text-embedding-3-small", 1536, "openai", "OpenAI cloud API"},
}

// IsKnownModel returns true if the model name is in the known models list.
func IsKnownModel(name string) bool {
	for _, m := range KnownModels {
		if m.Name == name {
			return true
		}
	}
	return false
}

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
	Graph     GraphConfig     `toml:"graph"`
	Memory    MemoryConfig    `toml:"memory"`
	Hooks     HooksConfig     `toml:"hooks"`
	Display   DisplayConfig   `toml:"display"`
}

// VaultConfig holds vault-related settings.
type VaultConfig struct {
	Path        string   `toml:"path"`
	SkipDirs    []string `toml:"skip_dirs"`
	NoisePaths  []string `toml:"noise_paths"`
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
	Provider   string `toml:"provider"`   // "ollama" (default), "openai", "openai-compatible"
	Model      string `toml:"model"`      // model name (provider-specific default if empty)
	APIKey     string `toml:"api_key"`    // API key (required for openai, optional for openai-compatible)
	BaseURL    string `toml:"base_url"`   // base URL for embedding API (provider-specific default if empty)
	Dimensions int    `toml:"dimensions"` // vector dimensions (0 = provider default)
}

// GraphConfig holds knowledge-graph extraction settings.
type GraphConfig struct {
	LLMMode string `toml:"llm_mode"` // "off" (default), "local-only", "on"
}

// HooksConfig controls which hooks are enabled.
type HooksConfig struct {
	ContextSurfacing  bool `toml:"context_surfacing"`
	DecisionExtractor bool `toml:"decision_extractor"`
	HandoffGenerator  bool `toml:"handoff_generator"`
	StalenessCheck    bool `toml:"staleness_check"`
	HandoffMaxAgeDays int  `toml:"handoff_max_age_days"` // Max age in days for loading handoffs (default 2)
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
			Model:    EmbeddingModel,
		},
		Graph: GraphConfig{
			LLMMode: "off",
		},
		Memory: MemoryConfig{
			MaxTokenBudget:     1600,
			MaxResults:         4,
			DistanceThreshold:  16.2,
			CompositeThreshold: 0.35,
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
		meta, err := toml.DecodeFile(configPath, cfg)
		if err != nil {
			return nil, fmt.Errorf("parse config %s: %w", configPath, err)
		}
		warnUnknownKeys(meta, configPath)
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
	if v := os.Getenv("SAME_NOISE_PATHS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.Vault.NoisePaths = append(cfg.Vault.NoisePaths, p)
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
	if v := os.Getenv("SAME_EMBED_BASE_URL"); v != "" {
		cfg.Embedding.BaseURL = v
	}
	if v := os.Getenv("SAME_EMBED_API_KEY"); v != "" {
		cfg.Embedding.APIKey = v
	}
	if v := os.Getenv("SAME_GRAPH_LLM"); v != "" {
		cfg.Graph.LLMMode = v
	}
	// Also check OPENAI_API_KEY as a convenience fallback
	if cfg.Embedding.APIKey == "" && (cfg.Embedding.Provider == "openai" || cfg.Embedding.Provider == "openai-compatible") {
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			cfg.Embedding.APIKey = v
		}
	}

	// Apply TOML skip_dirs to the global SkipDirs map.
	// Previously parsed but never applied — this fixes the bug.
	if len(cfg.Vault.SkipDirs) > 0 {
		RebuildSkipDirs(cfg.Vault.SkipDirs)
	}

	return cfg, nil
}

// LoadConfigFrom loads configuration from a specific file path, merging with
// defaults and env vars. Use this instead of LoadConfig() when you know exactly
// which config file to load (e.g., after writing a config during init).
func LoadConfigFrom(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			meta, err := toml.DecodeFile(configPath, cfg)
			if err != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, err)
			}
			warnUnknownKeys(meta, configPath)
		}
	}

	// Environment variables override TOML values (same as LoadConfig)
	if v := os.Getenv("VAULT_PATH"); v != "" {
		cfg.Vault.Path = v
	}
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		cfg.Ollama.URL = v
	}
	if v := os.Getenv("SAME_EMBED_PROVIDER"); v != "" {
		cfg.Embedding.Provider = v
	}
	if v := os.Getenv("SAME_EMBED_MODEL"); v != "" {
		cfg.Embedding.Model = v
	}
	if v := os.Getenv("SAME_EMBED_BASE_URL"); v != "" {
		cfg.Embedding.BaseURL = v
	}
	if v := os.Getenv("SAME_EMBED_API_KEY"); v != "" {
		cfg.Embedding.APIKey = v
	}
	if v := os.Getenv("SAME_GRAPH_LLM"); v != "" {
		cfg.Graph.LLMMode = v
	}
	if cfg.Embedding.APIKey == "" && (cfg.Embedding.Provider == "openai" || cfg.Embedding.Provider == "openai-compatible") {
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
	return os.WriteFile(configPath, []byte(content), 0o600)
}

func generateTOMLContent(vaultPath string) string {
	var b strings.Builder
	b.WriteString("# SAME Configuration\n")
	b.WriteString("# https://github.com/sgx-labs/statelessagent\n")
	b.WriteString("#\n")
	b.WriteString("# Priority: CLI flags > environment variables > this file > built-in defaults\n")
	b.WriteString("# Environment variables: VAULT_PATH, OLLAMA_URL, SAME_HANDOFF_DIR,\n")
	b.WriteString("#   SAME_DECISION_LOG, SAME_SKIP_DIRS, SAME_NOISE_PATHS, SAME_DATA_DIR,\n")
	b.WriteString("#   SAME_GRAPH_LLM\n\n")

	b.WriteString("[vault]\n")
	if vaultPath != "" {
		b.WriteString(fmt.Sprintf("path = %q\n", vaultPath))
	} else {
		b.WriteString("# path = \"/path/to/your/notes\"  # auto-detected if unset\n")
	}
	b.WriteString("# skip_dirs = [\".venv\", \"build\"]  # added to built-in exclusions\n")
	b.WriteString("# noise_paths = [\"experiments/\", \"raw_outputs/\"]  # paths filtered from context surfacing\n")
	b.WriteString("handoff_dir = \"sessions\"\n")
	b.WriteString("decision_log = \"decisions.md\"\n\n")

	b.WriteString("[ollama]\n")
	b.WriteString("url = \"http://localhost:11434\"\n")
	b.WriteString("model = \"nomic-embed-text\"\n\n")

	b.WriteString("[embedding]\n")
	b.WriteString("# Embedding provider: \"ollama\" (default), \"openai\", \"openai-compatible\", or \"none\" (keyword-only)\n")
	activeProvider := EmbeddingProvider()
	if activeProvider == "" {
		activeProvider = "ollama"
	}
	b.WriteString(fmt.Sprintf("provider = %q\n", activeProvider))
	b.WriteString(fmt.Sprintf("model = %q\n", EmbeddingModel))
	b.WriteString("# api_key = \"\"                  # required for cloud providers\n")
	b.WriteString("#                               # or set SAME_EMBED_API_KEY / OPENAI_API_KEY\n")
	b.WriteString("# dimensions = 0                # 0 = use provider default\n\n")

	b.WriteString("[graph]\n")
	b.WriteString("# LLM extraction policy:\n")
	b.WriteString("#   \"off\"        = regex-only graph extraction (default)\n")
	b.WriteString("#   \"local-only\" = allow LLM extraction only with local chat endpoints\n")
	b.WriteString("#   \"on\"         = allow LLM extraction with any configured chat provider\n")
	b.WriteString("llm_mode = \"off\"\n\n")

	b.WriteString("[memory]\n")
	b.WriteString("# Presets: same profile use precise|balanced|broad|pi\n")
	b.WriteString("max_token_budget = 1600\n")
	b.WriteString("max_results = 4\n")
	b.WriteString("distance_threshold = 16.2\n")
	b.WriteString("composite_threshold = 0.35\n\n")

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

// HandoffMaxAge returns the maximum age in hours for loading handoff notes.
// Defaults to 48 hours (2 days). Configurable via hooks.handoff_max_age_days.
func HandoffMaxAge() int {
	if cfg := loadConfigSafe(); cfg != nil && cfg.Hooks.HandoffMaxAgeDays > 0 {
		return cfg.Hooks.HandoffMaxAgeDays * 24
	}
	return 48 // 2 days default
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

// NoisePaths returns the configured list of path prefixes to filter from surfacing.
// Returns nil (no filtering) if unconfigured.
func NoisePaths() []string {
	if v := os.Getenv("SAME_NOISE_PATHS"); v != "" {
		var paths []string
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				paths = append(paths, p)
			}
		}
		return paths
	}
	if cfg := loadConfigSafe(); cfg != nil && len(cfg.Vault.NoisePaths) > 0 {
		return cfg.Vault.NoisePaths
	}
	return nil
}

// MemoryMaxResults returns the configured maximum number of results to surface.
func MemoryMaxResults() int {
	if cfg := loadConfigSafe(); cfg != nil && cfg.Memory.MaxResults > 0 {
		return cfg.Memory.MaxResults
	}
	return 4
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
	return 0.35
}

// MemoryMaxTokenBudget returns the configured maximum token budget for context injection.
func MemoryMaxTokenBudget() int {
	if cfg := loadConfigSafe(); cfg != nil && cfg.Memory.MaxTokenBudget > 0 {
		return cfg.Memory.MaxTokenBudget
	}
	return 1600
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
	if v := os.Getenv("SAME_EMBED_BASE_URL"); v != "" {
		ec.BaseURL = v
	}
	if v := os.Getenv("SAME_EMBED_API_KEY"); v != "" {
		ec.APIKey = v
	}
	if ec.APIKey == "" && (ec.Provider == "openai" || ec.Provider == "openai-compatible") {
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			ec.APIKey = v
		}
	}

	// For ollama provider, merge the legacy [ollama] section if the embedding
	// model is unset or still the default. This ensures that users who set
	// [ollama] model = "mxbai-embed-large" (without an [embedding] section)
	// still get their override applied.
	if ec.Provider == "ollama" && cfg.Ollama.Model != "" {
		if ec.Model == "" || ec.Model == EmbeddingModel {
			// Only override if the user's [ollama].model differs from default
			if cfg.Ollama.Model != EmbeddingModel {
				ec.Model = cfg.Ollama.Model
			}
		}
	}

	return ec
}

// GraphLLMMode returns the graph LLM extraction policy:
// "off" (default), "local-only", or "on".
func GraphLLMMode() string {
	mode := ""
	if v := os.Getenv("SAME_GRAPH_LLM"); v != "" {
		mode = v
	} else if cfg := loadConfigSafe(); cfg != nil {
		mode = cfg.Graph.LLMMode
	}

	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "off", "false", "0", "disabled":
		return "off"
	case "local-only", "local":
		return "local-only"
	case "on", "true", "1", "enabled":
		return "on"
	default:
		// Fail closed for unknown values.
		return "off"
	}
}

// loadConfigSafe loads config without risking recursion. Returns nil on error.
func loadConfigSafe() *Config {
	cfg, err := LoadConfig()
	if err != nil {
		return nil
	}
	return cfg
}

// ConfigWarning returns any config file parse error, or empty string if OK.
func ConfigWarning() string {
	_, err := LoadConfig()
	if err != nil {
		return err.Error()
	}
	return ""
}

// FindConfigFile returns the path to the active config file, or empty string if none found.
func FindConfigFile() string {
	return findConfigFile()
}

// configSuggestions maps common wrong keys to the correct TOML key name.
var configSuggestions = map[string]string{
	"exclude_paths": "skip_dirs",
	"exclude_dirs":  "skip_dirs",
	"skip_paths":    "skip_dirs",
	"ignored_dirs":  "skip_dirs",
	"ignore_dirs":   "skip_dirs",
	"excludes":      "skip_dirs",
	"noise":         "noise_paths",
	"apikey":        "api_key",
	"api-key":       "api_key",
	"baseurl":       "base_url",
	"base-url":      "base_url",
	"token_budget":  "max_token_budget",
	"budget":        "max_token_budget",
}

// warnUnknownKeys prints warnings for unrecognized config keys.
func warnUnknownKeys(meta toml.MetaData, configPath string) {
	undecoded := meta.Undecoded()
	if len(undecoded) == 0 {
		return
	}

	fname := filepath.Base(configPath)
	for _, key := range undecoded {
		keyStr := key.String()
		lastPart := key[len(key)-1]

		if suggestion, ok := configSuggestions[lastPart]; ok {
			fmt.Fprintf(os.Stderr, "same: WARNING: unknown key %q in %s — did you mean %q?\n",
				keyStr, fname, suggestion)
		} else {
			fmt.Fprintf(os.Stderr, "same: WARNING: unknown key %q in %s (will be ignored)\n",
				keyStr, fname)
		}
	}
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

// SkipFiles are filenames excluded from indexing (meta-docs, not project knowledge).
var SkipFiles = map[string]bool{
	"CLAUDE.md": true,
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
	// CLI flag should always have highest priority.
	if VaultOverride != "" {
		reg := LoadRegistry()
		if resolved := reg.ResolveVault(VaultOverride); resolved != "" {
			path = resolved
		} else {
			path = VaultOverride
		}
	} else if v := os.Getenv("VAULT_PATH"); v != "" {
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

// validateVaultPath rejects vault paths that are too broad (e.g., /, /home, /Users)
// and resolves symlinks to prevent symlink-based escapes.
func validateVaultPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	// Block filesystem roots and shallow system directories.
	// On Windows, also block drive roots (C:\) and system directories.
	dangerous := []string{"/", "/home", "/Users", "/tmp", "/var", "/etc", "/opt"}
	if runtime.GOOS == "windows" && len(abs) >= 3 {
		// Add common Windows drive roots and system paths
		for _, letter := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
			dangerous = append(dangerous, string(letter)+":\\")
		}
		driveRoot := abs[:3] // e.g. "C:\"
		dangerous = append(dangerous, filepath.Join(driveRoot, "Users"), filepath.Join(driveRoot, "Windows"))
	}
	for _, d := range dangerous {
		if abs == d {
			fmt.Fprintf(os.Stderr, "WARNING: VAULT_PATH=%q is too broad, ignoring.\n", abs)
			return ""
		}
	}

	// SECURITY: resolve symlinks and re-check the real path against dangerous roots.
	// A symlink could point vault operations at /, /home, etc.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Path may not exist yet (e.g., during init); skip symlink check
		return path
	}
	for _, d := range dangerous {
		if resolved == d {
			fmt.Fprintf(os.Stderr, "WARNING: VAULT_PATH=%q resolves to %q which is too broad, ignoring.\n", abs, resolved)
			return ""
		}
		// Also resolve the dangerous path itself through symlinks (e.g., on macOS
		// /tmp -> /private/tmp) so we catch indirect matches.
		if resolvedDangerous, err := filepath.EvalSymlinks(d); err == nil {
			if resolved == resolvedDangerous {
				fmt.Fprintf(os.Stderr, "WARNING: VAULT_PATH=%q resolves to %q which is too broad, ignoring.\n", abs, resolved)
				return ""
			}
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

// Sentinel errors for consistent messaging across CLI and hooks.
var (
	// ErrNoVault is returned when no vault path can be resolved.
	ErrNoVault = fmt.Errorf("no vault found — run 'same init' or set VAULT_PATH")
	// ErrNoDatabase is returned when the SAME database cannot be opened.
	ErrNoDatabase = fmt.Errorf("cannot open SAME database — run 'same init', 'same reindex', or 'same doctor' to diagnose")
	// ErrOllamaNotLocal is returned when the Ollama URL points to a non-localhost host.
	ErrOllamaNotLocal = fmt.Errorf("OLLAMA_URL must point to localhost for security")
)

// OllamaURL returns the validated Ollama API URL.
// Returns an error if the URL is invalid or does not point to localhost.
func OllamaURL() (string, error) {
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
		return "", fmt.Errorf("invalid OLLAMA_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("OLLAMA_URL must use http or https scheme, got: %s", u.Scheme)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		// SECURITY: Don't leak the hostname in error message
		return "", ErrOllamaNotLocal
	}
	return raw, nil
}

// DBPath returns the path to the SQLite database file.
func DBPath() string {
	return filepath.Join(DataDir(), "vault.db")
}

// DataDir returns the data directory for the same binary.
// SECURITY: Validates SAME_DATA_DIR is an existing, writable directory.
func DataDir() string {
	if v := os.Getenv("SAME_DATA_DIR"); v != "" {
		return validateDataDir(v)
	}
	return filepath.Join(VaultPath(), ".same", "data")
}

// validateDataDir checks that the given path is a valid directory (or can be
// created). Falls back to the default data dir if the path is invalid.
func validateDataDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: SAME_DATA_DIR=%q is not a valid path, using default.\n", dir)
		return filepath.Join(VaultPath(), ".same", "data")
	}

	info, err := os.Stat(abs)
	if err == nil {
		// Path exists — must be a directory
		if !info.IsDir() {
			fmt.Fprintf(os.Stderr, "WARNING: SAME_DATA_DIR=%q is not a directory, using default.\n", abs)
			return filepath.Join(VaultPath(), ".same", "data")
		}
		// Check writable by attempting to create a temp file
		testFile := filepath.Join(abs, ".same_write_test")
		if f, err := os.Create(testFile); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: SAME_DATA_DIR=%q is not writable, using default.\n", abs)
			return filepath.Join(VaultPath(), ".same", "data")
		} else {
			f.Close()
			os.Remove(testFile)
		}
		return abs
	}

	// Path doesn't exist — try to create it
	if err := os.MkdirAll(abs, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: SAME_DATA_DIR=%q cannot be created (%v), using default.\n", abs, err)
		return filepath.Join(VaultPath(), ".same", "data")
	}
	return abs
}

// VaultRegistry holds registered vault paths with aliases.
type VaultRegistry struct {
	Vaults  map[string]string `json:"vaults"`  // alias -> path
	Default string            `json:"default"` // alias of default vault
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
// C12: Uses a lockfile to prevent TOCTOU races when multiple processes
// read and write vaults.json concurrently.
func (r *VaultRegistry) Save() error {
	path := RegistryPath()
	os.MkdirAll(filepath.Dir(path), 0o755)

	// Acquire lockfile
	lockPath := path + ".lock"
	unlock, err := acquireFileLock(lockPath)
	if err != nil {
		// If locking fails, proceed without it (best effort)
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
	defer unlock()

	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// acquireFileLock creates a lockfile using O_EXCL for atomic creation.
// Returns a cleanup function and nil on success, or an error if the lock
// cannot be acquired within a timeout.
func acquireFileLock(lockPath string) (func(), error) {
	const maxRetries = 20
	const retryDelay = 50 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Check for stale lock (older than 10 seconds)
		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > 10*time.Second {
				os.Remove(lockPath)
				continue
			}
		}
		time.Sleep(retryDelay)
	}
	return nil, fmt.Errorf("could not acquire lock on %s", lockPath)
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

	// Auto-detect: check CWD for any known marker (before registry default)
	if cwd, err := os.Getwd(); err == nil {
		for _, marker := range VaultMarkers {
			if _, err := os.Stat(filepath.Join(cwd, marker)); err == nil {
				return cwd
			}
		}
	}

	// Check registry default
	reg := LoadRegistry()
	if reg.Default != "" {
		if p, ok := reg.Vaults[reg.Default]; ok {
			return p
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
		MaxResults:         4,
		DistanceThreshold:  16.2,
		CompositeThreshold: 0.35,
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
	"pi": {
		Name:               "pi",
		Description:        "Raspberry Pi / low-resource optimization",
		MaxResults:         2,
		DistanceThreshold:  15.0,
		CompositeThreshold: 0.65,
		TokenWarning:       "Minimizes CPU/RAM pressure and token usage",
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
		return fmt.Errorf("unknown profile: %s (available: precise, balanced, broad, pi)", profileName)
	}

	cfgPath := ConfigFilePath(vaultPath)

	// Load from the target vault's config file to avoid clobbering
	// settings when CWD != vaultPath (e.g., during init).
	cfg, err := LoadConfigFrom(cfgPath)
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
	return os.WriteFile(cfgPath, buf.Bytes(), 0o600)
}

// SetDisplayMode updates the display mode in the config file.
func SetDisplayMode(vaultPath, mode string) error {
	cfgPath := ConfigFilePath(vaultPath)

	// Load from the target vault's config file to avoid clobbering
	// settings when CWD != vaultPath (e.g., during init).
	cfg, err := LoadConfigFrom(cfgPath)
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
	return os.WriteFile(cfgPath, buf.Bytes(), 0o600)
}

// SetEmbeddingModel updates the embedding model in the config file.
func SetEmbeddingModel(vaultPath, model string) error {
	cfgPath := ConfigFilePath(vaultPath)

	cfg, err := LoadConfigFrom(cfgPath)
	if err != nil {
		cfg = DefaultConfig()
	}

	cfg.Embedding.Model = model
	// Also update legacy [ollama].model for compatibility
	cfg.Ollama.Model = model

	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	os.MkdirAll(filepath.Dir(cfgPath), 0o755)
	return os.WriteFile(cfgPath, buf.Bytes(), 0o600)
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
	return os.WriteFile(path, data, 0o600)
}
