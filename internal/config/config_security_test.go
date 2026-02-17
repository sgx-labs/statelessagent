package config

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Vault path validation (dangerous roots) ---

func TestValidateVaultPath_DangerousRoots(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"filesystem root", "/"},
		{"home root", "/home"},
		{"users root", "/Users"},
		{"tmp root", "/tmp"},
		{"var root", "/var"},
		{"etc root", "/etc"},
		{"opt root", "/opt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateVaultPath(tt.path)
			if result != "" {
				t.Errorf("expected empty for dangerous path %q, got %q", tt.path, result)
			}
		})
	}
}

func TestValidateVaultPath_AllowsReasonable(t *testing.T) {
	dir := t.TempDir()
	result := validateVaultPath(dir)
	if result == "" {
		t.Errorf("expected valid result for reasonable path %q, got empty", dir)
	}
}

func TestValidateVaultPath_SymlinkToDangerousRoot(t *testing.T) {
	// Create a symlink that resolves to /tmp (a dangerous root)
	dir := t.TempDir()
	link := filepath.Join(dir, "evil-link")
	err := os.Symlink("/tmp", link)
	if err != nil {
		t.Skip("Cannot create symlinks on this platform")
	}

	result := validateVaultPath(link)
	if result != "" {
		t.Errorf("expected empty for symlink to /tmp, got %q", result)
	}
}

func TestSafeVaultSubpath_BoundaryChecks(t *testing.T) {
	vault := t.TempDir()
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = "" })
	t.Setenv("VAULT_PATH", vault)

	valid, ok := SafeVaultSubpath("sessions/next-handoff.md")
	if !ok {
		t.Fatal("expected valid subpath to succeed")
	}
	if !pathWithinBase(vault, valid) {
		t.Fatalf("expected resolved path within vault: %s", valid)
	}

	if _, ok := SafeVaultSubpath("../escape.md"); ok {
		t.Fatal("expected traversal subpath to be rejected")
	}

	if _, ok := SafeVaultSubpath("/etc/passwd"); ok {
		t.Fatal("expected absolute path subpath to be rejected")
	}
}

// --- Config file handling with malformed data ---

func TestLoadConfig_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".same")
	os.MkdirAll(configDir, 0o755)

	// Write garbage TOML
	os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte(`[this is {{ not valid TOML !!! `), 0o644)

	t.Setenv("VAULT_PATH", dir)
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	_, err := LoadConfig()
	if err == nil {
		t.Error("expected error for malformed TOML config")
	}
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".same")
	os.MkdirAll(configDir, 0o755)

	// Empty TOML file should be fine (defaults used)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(""), 0o644)

	t.Setenv("VAULT_PATH", dir)
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error for empty config: %v", err)
	}
	// Should get defaults
	if cfg.Ollama.URL != "http://localhost:11434" {
		t.Errorf("expected default Ollama URL, got %q", cfg.Ollama.URL)
	}
}

func TestLoadConfig_PartialTOML(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".same")
	os.MkdirAll(configDir, 0o755)

	// Partial config: only set one section
	os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte(`[ollama]
url = "http://localhost:9999"
`), 0o644)

	t.Setenv("VAULT_PATH", dir)
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error for partial config: %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:9999" {
		t.Errorf("expected partial override URL, got %q", cfg.Ollama.URL)
	}
	// Other defaults should still be present
	if cfg.Display.Mode != "full" {
		t.Errorf("expected default display mode, got %q", cfg.Display.Mode)
	}
}

// --- Environment variable overrides ---

func TestLoadConfig_AllEnvVars(t *testing.T) {
	t.Setenv("VAULT_PATH", "/tmp/test-vault-env")
	t.Setenv("OLLAMA_URL", "http://localhost:9876")
	t.Setenv("SAME_HANDOFF_DIR", "my-sessions")
	t.Setenv("SAME_DECISION_LOG", "my-decisions.md")
	t.Setenv("SAME_SKIP_DIRS", "build,dist,vendor")
	t.Setenv("SAME_NOISE_PATHS", "experiments/,raw/")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Vault.Path != "/tmp/test-vault-env" {
		t.Errorf("expected VAULT_PATH override, got %q", cfg.Vault.Path)
	}
	if cfg.Ollama.URL != "http://localhost:9876" {
		t.Errorf("expected OLLAMA_URL override, got %q", cfg.Ollama.URL)
	}
	if cfg.Vault.HandoffDir != "my-sessions" {
		t.Errorf("expected SAME_HANDOFF_DIR override, got %q", cfg.Vault.HandoffDir)
	}
	if cfg.Vault.DecisionLog != "my-decisions.md" {
		t.Errorf("expected SAME_DECISION_LOG override, got %q", cfg.Vault.DecisionLog)
	}

	// Check that SAME_SKIP_DIRS were added
	foundBuild := false
	for _, d := range cfg.Vault.SkipDirs {
		if d == "build" {
			foundBuild = true
		}
	}
	if !foundBuild {
		t.Error("expected 'build' in skip dirs from env var")
	}

	// Check noise paths
	foundExperiments := false
	for _, p := range cfg.Vault.NoisePaths {
		if p == "experiments/" {
			foundExperiments = true
		}
	}
	if !foundExperiments {
		t.Error("expected 'experiments/' in noise paths from env var")
	}
}

func TestLoadConfig_UnknownKeys(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".same")
	os.MkdirAll(configDir, 0o755)

	// Config with unknown keys — should not error but should warn
	os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte(`[vault]
exclude_paths = ["_Raw", "Scratch"]
path = "/home/user/notes"

[embedding]
provider = "ollama"
`), 0o644)

	t.Setenv("VAULT_PATH", dir)
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unknown keys should not cause error: %v", err)
	}
	// Valid keys should still be parsed (VAULT_PATH env overrides toml path, so check provider)
	if cfg.Embedding.Provider != "ollama" {
		t.Errorf("expected embedding provider to be parsed, got %q", cfg.Embedding.Provider)
	}
}

func TestConfigSuggestions(t *testing.T) {
	// Verify the suggestions map has expected entries
	tests := []struct {
		wrong   string
		correct string
	}{
		{"exclude_paths", "skip_dirs"},
		{"exclude_dirs", "skip_dirs"},
		{"skip_paths", "skip_dirs"},
		{"apikey", "api_key"},
		{"base-url", "base_url"},
	}
	for _, tt := range tests {
		if got, ok := configSuggestions[tt.wrong]; !ok || got != tt.correct {
			t.Errorf("configSuggestions[%q] = %q, want %q", tt.wrong, got, tt.correct)
		}
	}
}

func TestLoadConfig_NoEnvVars(t *testing.T) {
	// Unset all SAME-related env vars
	t.Setenv("VAULT_PATH", "")
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("SAME_HANDOFF_DIR", "")
	t.Setenv("SAME_DECISION_LOG", "")
	t.Setenv("SAME_SKIP_DIRS", "")
	t.Setenv("SAME_NOISE_PATHS", "")

	// Force no env vars (clear them)
	os.Unsetenv("VAULT_PATH")
	os.Unsetenv("OLLAMA_URL")
	os.Unsetenv("SAME_HANDOFF_DIR")
	os.Unsetenv("SAME_DECISION_LOG")
	os.Unsetenv("SAME_SKIP_DIRS")
	os.Unsetenv("SAME_NOISE_PATHS")
	os.Unsetenv("SAME_EMBED_PROVIDER")
	os.Unsetenv("SAME_EMBED_MODEL")
	os.Unsetenv("SAME_EMBED_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error with no env vars: %v", err)
	}

	// All should be defaults
	if cfg.Ollama.URL != "http://localhost:11434" {
		t.Errorf("expected default Ollama URL, got %q", cfg.Ollama.URL)
	}
	if cfg.Vault.HandoffDir != "sessions" {
		t.Errorf("expected default handoff dir, got %q", cfg.Vault.HandoffDir)
	}
	if cfg.Vault.DecisionLog != "decisions.md" {
		t.Errorf("expected default decision log, got %q", cfg.Vault.DecisionLog)
	}
}

// --- Embedding provider config ---

func TestEmbeddingProviderConfig_EnvOverrides(t *testing.T) {
	t.Setenv("SAME_EMBED_PROVIDER", "openai")
	t.Setenv("SAME_EMBED_MODEL", "text-embedding-3-small")
	t.Setenv("SAME_EMBED_API_KEY", "sk-test-key-123")

	ec := EmbeddingProviderConfig()
	if ec.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", ec.Provider)
	}
	if ec.Model != "text-embedding-3-small" {
		t.Errorf("expected model override, got %q", ec.Model)
	}
	if ec.APIKey != "sk-test-key-123" {
		t.Errorf("expected API key override, got %q", ec.APIKey)
	}
}

func TestEmbeddingProviderConfig_OpenAIFallbackKey(t *testing.T) {
	os.Unsetenv("SAME_EMBED_API_KEY")
	t.Setenv("SAME_EMBED_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "sk-fallback-key")

	ec := EmbeddingProviderConfig()
	if ec.APIKey != "sk-fallback-key" {
		t.Errorf("expected OPENAI_API_KEY fallback, got %q", ec.APIKey)
	}
}

// --- EmbeddingDim tests ---

func TestEmbeddingDim_Defaults(t *testing.T) {
	// Ensure env vars don't interfere
	os.Unsetenv("SAME_EMBED_PROVIDER")
	os.Unsetenv("SAME_EMBED_MODEL")

	dim := EmbeddingDim()
	// Default provider is ollama with nomic-embed-text -> 768
	if dim != 768 {
		t.Errorf("expected default dim 768, got %d", dim)
	}
}

func TestEmbeddingDim_OpenAIDefault(t *testing.T) {
	t.Setenv("SAME_EMBED_PROVIDER", "openai")
	os.Unsetenv("SAME_EMBED_MODEL")

	dim := EmbeddingDim()
	if dim != 1536 {
		t.Errorf("expected openai default dim 1536, got %d", dim)
	}
}

// --- SkipDirs ---

func TestDefaultSkipDirs(t *testing.T) {
	// Check that _PRIVATE is always skipped
	if !SkipDirs["_PRIVATE"] {
		t.Error("expected _PRIVATE in default skip dirs")
	}
	if !SkipDirs[".git"] {
		t.Error("expected .git in default skip dirs")
	}
	if !SkipDirs[".same"] {
		t.Error("expected .same in default skip dirs")
	}
}

func TestRebuildSkipDirs_AddsCustom(t *testing.T) {
	RebuildSkipDirs([]string{"custom-dir", "build"})
	defer RebuildSkipDirs(nil) // restore

	if !SkipDirs["custom-dir"] {
		t.Error("expected 'custom-dir' in rebuilt skip dirs")
	}
	if !SkipDirs["build"] {
		t.Error("expected 'build' in rebuilt skip dirs")
	}
	// Default dirs should still be present
	if !SkipDirs["_PRIVATE"] {
		t.Error("expected _PRIVATE still in skip dirs after rebuild")
	}
}

// --- Profile tests ---

func TestBuiltinProfiles_Exist(t *testing.T) {
	expected := []string{"precise", "balanced", "broad", "pi"}
	for _, name := range expected {
		if _, ok := BuiltinProfiles[name]; !ok {
			t.Errorf("expected builtin profile %q to exist", name)
		}
	}
}

func TestSetProfile_InvalidName(t *testing.T) {
	dir := t.TempDir()
	err := SetProfile(dir, "nonexistent-profile")
	if err == nil {
		t.Error("expected error for invalid profile name")
	}
}

// --- GenerateConfig ---

func TestGenerateConfig_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateConfig(dir); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	cfgPath := ConfigFilePath(dir)
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Check permissions (0o600)
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("expected permissions 0600, got %o", perm)
	}
}
