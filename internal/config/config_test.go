package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOllamaURL_Default(t *testing.T) {
	// Unset env to test default
	os.Unsetenv("OLLAMA_URL")
	url, err := OllamaURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://localhost:11434" {
		t.Errorf("expected default URL, got %q", url)
	}
}

func TestOllamaURL_Localhost(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"localhost", "http://localhost:11434"},
		{"127.0.0.1", "http://127.0.0.1:11434"},
		{"ipv6", "http://[::1]:11434"},
		{"custom port", "http://localhost:9999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OLLAMA_URL", tt.url)
			got, err := OllamaURL()
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.url, err)
			}
			if got != tt.url {
				t.Errorf("expected %q, got %q", tt.url, got)
			}
		})
	}
}

func TestOllamaURL_RejectsRemote(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"remote host", "http://example.com:11434"},
		{"remote IP", "http://192.168.1.100:11434"},
		{"https remote", "https://ollama.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OLLAMA_URL", tt.url)
			_, err := OllamaURL()
			if err == nil {
				t.Errorf("expected error for remote URL %q, got nil", tt.url)
			}
		})
	}
}

func TestOllamaURL_InvalidURL(t *testing.T) {
	t.Setenv("OLLAMA_URL", "://not-a-url")
	_, err := OllamaURL()
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestOllamaURL_RejectsBadScheme(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"file scheme", "file://localhost/etc/passwd"},
		{"ftp scheme", "ftp://localhost:11434"},
		{"no scheme", "localhost:11434"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OLLAMA_URL", tt.url)
			_, err := OllamaURL()
			if err == nil {
				t.Errorf("expected error for %s URL %q", tt.name, tt.url)
			}
		})
	}
}

func TestLoadConfig_Default(t *testing.T) {
	// With no config file, should get defaults
	os.Unsetenv("VAULT_PATH")
	os.Unsetenv("OLLAMA_URL")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:11434" {
		t.Errorf("expected default Ollama URL, got %q", cfg.Ollama.URL)
	}
	if cfg.Display.Mode != "full" {
		t.Errorf("expected default display mode 'full', got %q", cfg.Display.Mode)
	}
	if cfg.Memory.MaxResults != 4 {
		t.Errorf("expected default max_results 4, got %d", cfg.Memory.MaxResults)
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	t.Setenv("OLLAMA_URL", "http://localhost:9999")
	t.Setenv("VAULT_PATH", "/tmp/test-vault")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:9999" {
		t.Errorf("expected env override for Ollama URL, got %q", cfg.Ollama.URL)
	}
	if cfg.Vault.Path != "/tmp/test-vault" {
		t.Errorf("expected env override for vault path, got %q", cfg.Vault.Path)
	}
}

func TestVaultPath_VaultOverrideBeatsEnv(t *testing.T) {
	envVault := t.TempDir()
	overrideVault := t.TempDir()

	t.Setenv("VAULT_PATH", envVault)
	VaultOverride = overrideVault
	defer func() { VaultOverride = "" }()

	got := VaultPath()
	if got != overrideVault {
		t.Fatalf("expected VaultOverride %q to win, got %q", overrideVault, got)
	}
}

func TestLoadConfig_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".same")
	os.MkdirAll(configDir, 0o755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("invalid [[ toml"), 0o644)

	t.Setenv("VAULT_PATH", dir)
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	_, err := LoadConfig()
	if err == nil {
		t.Error("expected error for invalid TOML")
	}
}

func TestAcquireFileLock_StaleLockRemoveFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}

	dir := t.TempDir()
	lockPath := filepath.Join(dir, "vaults.lock")
	if err := os.WriteFile(lockPath, []byte("123\n"), 0o600); err != nil {
		t.Fatalf("write stale lockfile: %v", err)
	}

	stale := time.Now().Add(-11 * time.Second)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatalf("set stale lock mtime: %v", err)
	}

	// Remove write permission so stale lock cleanup fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := acquireFileLock(lockPath)
	if err == nil {
		t.Fatal("expected stale lock cleanup failure")
	}
	if !strings.Contains(err.Error(), "remove stale lockfile") {
		t.Fatalf("expected stale-lock removal error, got: %v", err)
	}
}

func TestDisplayMode_Default(t *testing.T) {
	os.Unsetenv("VAULT_PATH")
	mode := DisplayMode()
	if mode != "full" {
		t.Errorf("expected 'full', got %q", mode)
	}
}

func TestCurrentProfile_Default(t *testing.T) {
	os.Unsetenv("VAULT_PATH")
	profile := CurrentProfile()
	if profile != "balanced" {
		t.Errorf("expected 'balanced', got %q", profile)
	}
}

func TestConfigDefaultsConsistent(t *testing.T) {
	// Verify that accessor fallback values match DefaultConfig() values.
	// This catches the bug where accessors returned different defaults
	// than DefaultConfig(), causing inconsistent behavior when no config
	// file is present.
	defaults := DefaultConfig()

	// MemoryCompositeThreshold fallback should match DefaultConfig
	got := MemoryCompositeThreshold()
	if got != defaults.Memory.CompositeThreshold {
		t.Errorf("MemoryCompositeThreshold() = %v, want %v (from DefaultConfig)", got, defaults.Memory.CompositeThreshold)
	}

	// MemoryMaxResults fallback should match DefaultConfig
	gotInt := MemoryMaxResults()
	if gotInt != defaults.Memory.MaxResults {
		t.Errorf("MemoryMaxResults() = %d, want %d (from DefaultConfig)", gotInt, defaults.Memory.MaxResults)
	}

	// MemoryMaxTokenBudget fallback should match DefaultConfig
	gotInt = MemoryMaxTokenBudget()
	if gotInt != defaults.Memory.MaxTokenBudget {
		t.Errorf("MemoryMaxTokenBudget() = %d, want %d (from DefaultConfig)", gotInt, defaults.Memory.MaxTokenBudget)
	}

	// MemoryDistanceThreshold fallback should match DefaultConfig
	gotFloat := MemoryDistanceThreshold()
	if gotFloat != defaults.Memory.DistanceThreshold {
		t.Errorf("MemoryDistanceThreshold() = %v, want %v (from DefaultConfig)", gotFloat, defaults.Memory.DistanceThreshold)
	}
}

func TestErrConstants(t *testing.T) {
	if ErrNoVault == nil {
		t.Error("ErrNoVault should not be nil")
	}
	if ErrNoDatabase == nil {
		t.Error("ErrNoDatabase should not be nil")
	}
	if ErrOllamaNotLocal == nil {
		t.Error("ErrOllamaNotLocal should not be nil")
	}
}

func TestLoadConfig_NegativeMaxResults(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("SAME_EMBED_PROVIDER", "")
	t.Setenv("SAME_EMBED_MODEL", "")
	t.Setenv("SAME_EMBED_BASE_URL", "")
	t.Setenv("SAME_EMBED_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("VAULT_PATH", vault)

	configPath := filepath.Join(vault, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := []byte("[memory]\nmax_results = -1\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Memory.MaxResults != 1 {
		t.Fatalf("expected max_results to clamp to 1, got %d", cfg.Memory.MaxResults)
	}
	if got := MemoryMaxResults(); got != 1 {
		t.Fatalf("expected accessor to return clamped value 1, got %d", got)
	}
}

func TestLoadConfig_MaxResultsClampedToUpperBound(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)

	configPath := filepath.Join(vault, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := []byte("[memory]\nmax_results = 999\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Memory.MaxResults != 100 {
		t.Fatalf("expected max_results to clamp to 100, got %d", cfg.Memory.MaxResults)
	}
}

func TestLoadConfig_InvalidCompositeThreshold(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)

	configPath := filepath.Join(vault, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := []byte("[memory]\ncomposite_threshold = 1.5\ndistance_threshold = -2\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Memory.CompositeThreshold != 1.0 {
		t.Fatalf("expected composite_threshold to clamp to 1.0, got %v", cfg.Memory.CompositeThreshold)
	}
	if got := MemoryDistanceThreshold(); got != 16.2 {
		t.Fatalf("expected distance fallback 16.2 for invalid value, got %v", got)
	}
}

func TestLoadConfig_CompositeThresholdClampedLowerBound(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)

	configPath := filepath.Join(vault, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := []byte("[memory]\ncomposite_threshold = -0.4\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Memory.CompositeThreshold != 0.0 {
		t.Fatalf("expected composite_threshold to clamp to 0.0, got %v", cfg.Memory.CompositeThreshold)
	}
}

func TestLoadConfig_MissingBaseURL_OpenAICompatible(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("SAME_EMBED_PROVIDER", "")
	t.Setenv("SAME_EMBED_MODEL", "")
	t.Setenv("SAME_EMBED_BASE_URL", "")
	t.Setenv("SAME_EMBED_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("VAULT_PATH", vault)

	configPath := filepath.Join(vault, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := []byte("[embedding]\nprovider = \"openai-compatible\"\nmodel = \"test-embed\"\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Embedding.Provider != "openai-compatible" {
		t.Fatalf("provider = %q, want openai-compatible", cfg.Embedding.Provider)
	}
	ec := EmbeddingProviderConfig()
	if ec.BaseURL != "" {
		t.Fatalf("expected empty base URL, got %q", ec.BaseURL)
	}
}

// --- SetConfigValue tests ---

func TestConfigSet_OllamaURL(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	if err := SetConfigValue("ollama.url", "http://host.docker.internal:11434", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Ollama.URL != "http://host.docker.internal:11434" {
		t.Errorf("expected ollama.url = %q, got %q", "http://host.docker.internal:11434", cfg.Ollama.URL)
	}
}

func TestConfigSet_IntegerValue(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	if err := SetConfigValue("memory.max_results", "8", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Memory.MaxResults != 8 {
		t.Errorf("expected memory.max_results = 8, got %d", cfg.Memory.MaxResults)
	}
}

func TestConfigSet_FloatValue(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	if err := SetConfigValue("memory.composite_threshold", "0.5", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Memory.CompositeThreshold != 0.5 {
		t.Errorf("expected memory.composite_threshold = 0.5, got %v", cfg.Memory.CompositeThreshold)
	}
}

func TestConfigSet_BoolValue(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	// First set to true to ensure the file exists with a known state
	if err := SetConfigValue("hooks.context_surfacing", "true", false); err != nil {
		t.Fatalf("SetConfigValue (true): %v", err)
	}
	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if !cfg.Hooks.ContextSurfacing {
		t.Errorf("expected hooks.context_surfacing = true, got false")
	}

	// Now set to false
	if err := SetConfigValue("hooks.context_surfacing", "false", false); err != nil {
		t.Fatalf("SetConfigValue (false): %v", err)
	}
	cfg, err = LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Hooks.ContextSurfacing {
		t.Errorf("expected hooks.context_surfacing = false, got true")
	}
}

func TestConfigSet_EnumValidation(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	err := SetConfigValue("graph.llm_mode", "invalid", false)
	if err == nil {
		t.Fatal("expected error for invalid graph.llm_mode value")
	}
	if !strings.Contains(err.Error(), "invalid value for graph.llm_mode") {
		t.Errorf("expected enum validation error, got: %v", err)
	}

	// Valid values should work
	for _, mode := range []string{"off", "local-only", "on"} {
		if err := SetConfigValue("graph.llm_mode", mode, false); err != nil {
			t.Errorf("SetConfigValue graph.llm_mode=%q: %v", mode, err)
		}
	}
}

func TestConfigSet_UnknownKey(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	err := SetConfigValue("nonexistent.key", "value", false)
	if err == nil {
		t.Fatal("expected error for unknown config key")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("expected 'unknown config key' error, got: %v", err)
	}
}

func TestConfigSet_GlobalFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SetConfigValue("ollama.url", "http://global-host:11434", true); err != nil {
		t.Fatalf("SetConfigValue (global): %v", err)
	}

	// Verify written to global config path, not vault config
	globalPath := filepath.Join(home, ".config", "same", "config.toml")
	cfg, err := LoadConfigFrom(globalPath)
	if err != nil {
		t.Fatalf("LoadConfigFrom global: %v", err)
	}
	if cfg.Ollama.URL != "http://global-host:11434" {
		t.Errorf("expected global ollama.url = %q, got %q", "http://global-host:11434", cfg.Ollama.URL)
	}
}

func TestConfigSet_CreatesFileIfMissing(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	configPath := ConfigFilePath(vault)
	// Ensure no config file exists
	if _, err := os.Stat(configPath); err == nil {
		t.Fatal("config file should not exist before test")
	}

	if err := SetConfigValue("ollama.url", "http://new-host:11434", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	// Verify file was created
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("config file should exist after SetConfigValue: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected file permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestConfigSet_PreservesExistingValues(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	// Set first value
	if err := SetConfigValue("ollama.url", "http://first:11434", false); err != nil {
		t.Fatalf("SetConfigValue (first): %v", err)
	}

	// Set a different value
	if err := SetConfigValue("memory.max_results", "10", false); err != nil {
		t.Fatalf("SetConfigValue (second): %v", err)
	}

	// Verify first value is still present
	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Ollama.URL != "http://first:11434" {
		t.Errorf("expected ollama.url preserved as %q, got %q", "http://first:11434", cfg.Ollama.URL)
	}
	if cfg.Memory.MaxResults != 10 {
		t.Errorf("expected memory.max_results = 10, got %d", cfg.Memory.MaxResults)
	}
}

func TestConfigSet_InvalidKeyFormat(t *testing.T) {
	err := SetConfigValue("noperiod", "value", false)
	if err == nil {
		t.Fatal("expected error for key without period")
	}
	if !strings.Contains(err.Error(), "invalid key format") {
		t.Errorf("expected 'invalid key format' error, got: %v", err)
	}
}

func TestConfigSet_DisplayModeValidation(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	err := SetConfigValue("display.mode", "invalid", false)
	if err == nil {
		t.Fatal("expected error for invalid display.mode value")
	}
	if !strings.Contains(err.Error(), "invalid value for display.mode") {
		t.Errorf("expected display mode validation error, got: %v", err)
	}
}

func TestConfigSet_InvalidIntegerValue(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	err := SetConfigValue("memory.max_results", "notanumber", false)
	if err == nil {
		t.Fatal("expected error for non-integer value")
	}
	if !strings.Contains(err.Error(), "invalid integer") {
		t.Errorf("expected 'invalid integer' error, got: %v", err)
	}
}

func TestConfigSet_InvalidFloatValue(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	err := SetConfigValue("memory.distance_threshold", "notafloat", false)
	if err == nil {
		t.Fatal("expected error for non-float value")
	}
	if !strings.Contains(err.Error(), "invalid float") {
		t.Errorf("expected 'invalid float' error, got: %v", err)
	}
}

func TestConfigSet_TypoSuggestion(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)

	// "budget" is in configSuggestions and maps to "max_token_budget"
	err := SetConfigValue("memory.budget", "value", false)
	if err == nil {
		t.Fatal("expected error for typo key")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected typo suggestion, got: %v", err)
	}
}

func TestLoadConfig_ZeroDimensionsFallsBackToModelDefault(t *testing.T) {
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("SAME_EMBED_PROVIDER", "")
	t.Setenv("SAME_EMBED_MODEL", "")
	t.Setenv("SAME_EMBED_BASE_URL", "")
	t.Setenv("SAME_EMBED_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("VAULT_PATH", vault)

	configPath := filepath.Join(vault, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := []byte("[embedding]\nprovider = \"openai\"\nmodel = \"text-embedding-3-small\"\ndimensions = 0\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Embedding.Dimensions != 0 {
		t.Fatalf("expected raw dimensions 0, got %d", cfg.Embedding.Dimensions)
	}
	if got := EmbeddingDim(); got != 1536 {
		t.Fatalf("expected OpenAI default dimensions 1536, got %d", got)
	}
}
