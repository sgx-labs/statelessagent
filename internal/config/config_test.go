package config

import (
	"os"
	"path/filepath"
	"testing"
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
