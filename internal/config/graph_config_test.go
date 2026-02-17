package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGraphLLMMode_DefaultOff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAULT_PATH", dir)
	t.Setenv("SAME_GRAPH_LLM", "")
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	if got := GraphLLMMode(); got != "off" {
		t.Fatalf("GraphLLMMode() = %q, want off", got)
	}
}

func TestGraphLLMMode_EnvAliases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "on", want: "on"},
		{input: "enabled", want: "on"},
		{input: "local", want: "local-only"},
		{input: "local-only", want: "local-only"},
		{input: "false", want: "off"},
		{input: "unknown-value", want: "off"},
	}

	dir := t.TempDir()
	t.Setenv("VAULT_PATH", dir)
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Setenv("SAME_GRAPH_LLM", tt.input)
			if got := GraphLLMMode(); got != tt.want {
				t.Fatalf("GraphLLMMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGraphLLMMode_FromConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".same")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("[graph]\nllm_mode = \"on\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("VAULT_PATH", dir)
	t.Setenv("SAME_GRAPH_LLM", "")
	VaultOverride = dir
	defer func() { VaultOverride = "" }()

	if got := GraphLLMMode(); got != "on" {
		t.Fatalf("GraphLLMMode() = %q, want on", got)
	}
}
