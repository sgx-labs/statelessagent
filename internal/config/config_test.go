package config

import (
	"encoding/json"
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
		{"host.docker.internal", "http://host.docker.internal:11434"},
		{"host.docker.internal custom port", "http://host.docker.internal:9999"},
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
		{"evil host", "http://evil.example.com:11434"},
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

func TestChatModel_EnvVar(t *testing.T) {
	t.Setenv("SAME_CHAT_MODEL", "test-model")
	got := ChatModel()
	if got != "test-model" {
		t.Fatalf("expected 'test-model', got %q", got)
	}
}

func TestChatModel_Config(t *testing.T) {
	t.Setenv("SAME_CHAT_MODEL", "")
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[chat]\nmodel = \"qwen2.5:7b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAULT_PATH", dir)
	got := ChatModel()
	if got != "qwen2.5:7b" {
		t.Fatalf("expected 'qwen2.5:7b', got %q", got)
	}
}

func TestChatModel_EnvOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".same", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[chat]\nmodel = \"qwen2.5:7b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAULT_PATH", dir)
	t.Setenv("SAME_CHAT_MODEL", "override-model")
	got := ChatModel()
	if got != "override-model" {
		t.Fatalf("expected 'override-model', got %q", got)
	}
}

func TestChatModel_Empty(t *testing.T) {
	t.Setenv("SAME_CHAT_MODEL", "")
	t.Setenv("VAULT_PATH", t.TempDir())
	got := ChatModel()
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// --- Vault UX tests ---

func TestDefaultVaultPath_SingleChildVault(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "myproject")
	if err := os.MkdirAll(filepath.Join(child, ".same"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Clear overrides so defaultVaultPath() auto-detects
	oldOverride := VaultOverride
	VaultOverride = ""
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", "")

	// Change to the parent directory
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	got := defaultVaultPath()
	if got != child {
		t.Errorf("expected single child vault %q, got %q", child, got)
	}
}

func TestDefaultVaultPath_MultipleChildVaults(t *testing.T) {
	parent := t.TempDir()
	child1 := filepath.Join(parent, "project1")
	child2 := filepath.Join(parent, "project2")
	if err := os.MkdirAll(filepath.Join(child1, ".same"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(child2, ".same"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldOverride := VaultOverride
	VaultOverride = ""
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", "")

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	// Capture stderr for the warning message
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })

	got := defaultVaultPath()

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	// Should NOT pick one of the children (should fall through to registry default)
	if got == child1 || got == child2 {
		t.Errorf("should not pick a child vault when multiple found, got %q", got)
	}

	// Should print a warning
	if !strings.Contains(output, "Multiple vaults found") {
		t.Errorf("expected multi-vault warning, got stderr: %q", output)
	}
	if !strings.Contains(output, "project1") || !strings.Contains(output, "project2") {
		t.Errorf("expected both vault names in warning, got stderr: %q", output)
	}
}

func TestDefaultVaultPath_NoChildVaults(t *testing.T) {
	parent := t.TempDir()
	// Create child dirs with no vault markers
	os.MkdirAll(filepath.Join(parent, "notvault"), 0o755)

	oldOverride := VaultOverride
	VaultOverride = ""
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", "")

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	// Capture stderr — should have no warning
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })

	_ = defaultVaultPath()

	w.Close()
	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if strings.Contains(output, "Multiple vaults found") {
		t.Errorf("should not print warning when no child vaults, got stderr: %q", output)
	}
}

func TestGlobalConfig_Loaded(t *testing.T) {
	// Create a temp home dir and a temp vault
	tmpHome := t.TempDir()
	vault := t.TempDir()

	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("HOME", tmpHome)

	// Write global config
	globalDir := filepath.Join(tmpHome, ".config", "same")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalConfig := `[ollama]
url = "http://global-host:11434"
`
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.Ollama.URL != "http://global-host:11434" {
		t.Errorf("expected global Ollama URL, got %q", cfg.Ollama.URL)
	}
}

func TestGlobalConfig_VaultOverrides(t *testing.T) {
	tmpHome := t.TempDir()
	vault := t.TempDir()

	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("HOME", tmpHome)

	// Write global config
	globalDir := filepath.Join(tmpHome, ".config", "same")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(`[ollama]
url = "http://global:11434"
`), 0o644)

	// Write vault config that overrides
	vaultDir := filepath.Join(vault, ".same")
	os.MkdirAll(vaultDir, 0o755)
	os.WriteFile(filepath.Join(vaultDir, "config.toml"), []byte(`[ollama]
url = "http://vault:11434"
`), 0o644)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.Ollama.URL != "http://vault:11434" {
		t.Errorf("expected vault Ollama URL to override global, got %q", cfg.Ollama.URL)
	}
}

func TestGlobalConfig_VaultInheritsGlobal(t *testing.T) {
	tmpHome := t.TempDir()
	vault := t.TempDir()

	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("HOME", tmpHome)

	// Write global config with Ollama URL
	globalDir := filepath.Join(tmpHome, ".config", "same")
	os.MkdirAll(globalDir, 0o755)
	os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(`[ollama]
url = "http://global:11434"
`), 0o644)

	// Write vault config with only vault path (no ollama section)
	vaultDir := filepath.Join(vault, ".same")
	os.MkdirAll(vaultDir, 0o755)
	os.WriteFile(filepath.Join(vaultDir, "config.toml"), []byte(`[vault]
path = "`+vault+`"
`), 0o644)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	// Ollama URL should come from global since vault didn't set it
	if cfg.Ollama.URL != "http://global:11434" {
		t.Errorf("expected global Ollama URL to be inherited, got %q", cfg.Ollama.URL)
	}
	// Vault path should come from vault config
	if cfg.Vault.Path != vault {
		t.Errorf("expected vault path from vault config, got %q", cfg.Vault.Path)
	}
}

func TestRegistryCleanup_RemovesStale(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	regDir := filepath.Join(tmpHome, ".config", "same")
	os.MkdirAll(regDir, 0o755)

	// Create a registry with a stale entry pointing to non-existent path
	reg := &VaultRegistry{
		Vaults: map[string]string{
			"stale_vault": "/tmp/nonexistent-test-path-" + t.Name(),
		},
	}
	data, _ := json.MarshalIndent(reg, "", "  ")
	os.WriteFile(filepath.Join(regDir, "vaults.json"), data, 0o600)

	// LoadRegistry should NOT prune (read-only)
	loaded := LoadRegistry()
	if _, ok := loaded.Vaults["stale_vault"]; !ok {
		t.Error("expected stale vault entry to be preserved by read-only LoadRegistry")
	}

	// PruneRegistry should remove the stale entry
	PruneRegistry()
	pruned := LoadRegistry()
	if _, ok := pruned.Vaults["stale_vault"]; ok {
		t.Error("expected stale vault entry to be removed by PruneRegistry")
	}
}

func TestRegistryCleanup_ClearsStaleDefault(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	regDir := filepath.Join(tmpHome, ".config", "same")
	os.MkdirAll(regDir, 0o755)

	existingVault := t.TempDir()

	// Create a registry with a valid vault and a default pointing to a non-existent alias
	reg := &VaultRegistry{
		Vaults: map[string]string{
			"valid_vault": existingVault,
		},
		Default: "nonexistent_alias",
	}
	data, _ := json.MarshalIndent(reg, "", "  ")
	os.WriteFile(filepath.Join(regDir, "vaults.json"), data, 0o600)

	// LoadRegistry should NOT clear stale default (read-only)
	loaded := LoadRegistry()
	if loaded.Default == "" {
		t.Error("expected stale default to be preserved by read-only LoadRegistry")
	}

	// PruneRegistry should clear it
	PruneRegistry()
	pruned := LoadRegistry()
	if pruned.Default != "" {
		t.Errorf("expected stale default to be cleared by PruneRegistry, got %q", pruned.Default)
	}
	// Valid vault should still be present
	if _, ok := loaded.Vaults["valid_vault"]; !ok {
		t.Error("expected valid vault to remain in registry")
	}
}

func TestNameForPath_ReverseLookup(t *testing.T) {
	reg := &VaultRegistry{
		Vaults: map[string]string{
			"myproject": "/some/path",
			"other":     "/other/path",
		},
	}

	if got := reg.NameForPath("/some/path"); got != "myproject" {
		t.Errorf("expected 'myproject', got %q", got)
	}
	if got := reg.NameForPath("/other/path"); got != "other" {
		t.Errorf("expected 'other', got %q", got)
	}
	if got := reg.NameForPath("/unknown/path"); got != "" {
		t.Errorf("expected empty string for unknown path, got %q", got)
	}
}

func TestGlobalConfigPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}
	got := GlobalConfigPath()
	expected := filepath.Join(home, ".config", "same", "config.toml")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// --- SetConfigValue tests ---

// setupTestVault creates an isolated vault directory with proper env/override setup.
// Returns the vault path.
func setupTestVault(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	oldOverride := VaultOverride
	VaultOverride = vault
	t.Cleanup(func() { VaultOverride = oldOverride })
	t.Setenv("VAULT_PATH", vault)
	// Clear embedding env vars to avoid interference
	t.Setenv("SAME_EMBED_PROVIDER", "")
	t.Setenv("SAME_EMBED_MODEL", "")
	t.Setenv("SAME_EMBED_BASE_URL", "")
	t.Setenv("SAME_EMBED_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("SAME_GRAPH_LLM", "")
	return vault
}

func TestConfigSet_OllamaURL(t *testing.T) {
	vault := setupTestVault(t)

	if err := SetConfigValue("ollama.url", "http://localhost:9999", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:9999" {
		t.Errorf("ollama.url = %q, want %q", cfg.Ollama.URL, "http://localhost:9999")
	}
}

func TestConfigSet_IntegerValue(t *testing.T) {
	vault := setupTestVault(t)

	if err := SetConfigValue("memory.max_results", "8", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Memory.MaxResults != 8 {
		t.Errorf("memory.max_results = %d, want 8", cfg.Memory.MaxResults)
	}
}

func TestConfigSet_FloatValue(t *testing.T) {
	vault := setupTestVault(t)

	if err := SetConfigValue("memory.composite_threshold", "0.5", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Memory.CompositeThreshold != 0.5 {
		t.Errorf("memory.composite_threshold = %v, want 0.5", cfg.Memory.CompositeThreshold)
	}
}

func TestConfigSet_BoolValue(t *testing.T) {
	vault := setupTestVault(t)

	if err := SetConfigValue("hooks.context_surfacing", "false", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Hooks.ContextSurfacing != false {
		t.Errorf("hooks.context_surfacing = %v, want false", cfg.Hooks.ContextSurfacing)
	}
}

func TestConfigSet_EnumValidation(t *testing.T) {
	_ = setupTestVault(t)

	err := SetConfigValue("graph.llm_mode", "invalid", false)
	if err == nil {
		t.Fatal("expected error for invalid enum value, got nil")
	}
	if !strings.Contains(err.Error(), "invalid value") {
		t.Errorf("expected 'invalid value' in error, got: %v", err)
	}
}

func TestConfigSet_UnknownKey(t *testing.T) {
	_ = setupTestVault(t)

	err := SetConfigValue("nonexistent.key", "value", false)
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("expected 'unknown config key' in error, got: %v", err)
	}
}

func TestConfigSet_GlobalFlag(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	if err := SetConfigValue("ollama.url", "http://localhost:7777", true); err != nil {
		t.Fatalf("SetConfigValue(global): %v", err)
	}

	cfg, err := LoadConfigFrom(GlobalConfigPath())
	if err != nil {
		t.Fatalf("LoadConfigFrom(global): %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:7777" {
		t.Errorf("global ollama.url = %q, want %q", cfg.Ollama.URL, "http://localhost:7777")
	}
}

func TestConfigSet_CreatesFileIfMissing(t *testing.T) {
	vault := setupTestVault(t)
	configPath := ConfigFilePath(vault)

	// Verify config file does not exist yet
	if _, err := os.Stat(configPath); err == nil {
		t.Fatal("config file should not exist before SetConfigValue")
	}

	if err := SetConfigValue("ollama.url", "http://localhost:5555", false); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	// Verify file was created
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Check permissions (0600)
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("config file permissions = %o, want 0600", perm)
	}
}

func TestConfigSet_PreservesExistingValues(t *testing.T) {
	vault := setupTestVault(t)

	// Set first value
	if err := SetConfigValue("ollama.url", "http://localhost:1111", false); err != nil {
		t.Fatalf("SetConfigValue(1): %v", err)
	}

	// Set a different value
	if err := SetConfigValue("memory.max_results", "10", false); err != nil {
		t.Fatalf("SetConfigValue(2): %v", err)
	}

	// Both values should persist
	cfg, err := LoadConfigFrom(ConfigFilePath(vault))
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:1111" {
		t.Errorf("ollama.url = %q, want %q", cfg.Ollama.URL, "http://localhost:1111")
	}
	if cfg.Memory.MaxResults != 10 {
		t.Errorf("memory.max_results = %d, want 10", cfg.Memory.MaxResults)
	}
}
