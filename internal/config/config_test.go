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
	if cfg.Memory.MaxResults != -1 {
		t.Fatalf("expected raw config value -1, got %d", cfg.Memory.MaxResults)
	}
	if got := MemoryMaxResults(); got != 4 {
		t.Fatalf("expected accessor fallback 4 for invalid value, got %d", got)
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
	if cfg.Memory.CompositeThreshold != 1.5 {
		t.Fatalf("expected raw composite_threshold 1.5, got %v", cfg.Memory.CompositeThreshold)
	}
	if got := MemoryDistanceThreshold(); got != 16.2 {
		t.Fatalf("expected distance fallback 16.2 for invalid value, got %v", got)
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
