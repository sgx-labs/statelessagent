package hooks

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
)

func capturePluginTestStderr(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	return buf.String()
}

func setupPluginTrustTest(t *testing.T) (string, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	vault := t.TempDir()
	origOverride := config.VaultOverride
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = origOverride })

	pluginDir := filepath.Join(vault, ".same")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir .same: %v", err)
	}
	pluginPath := filepath.Join(pluginDir, "plugins.json")
	data := []byte(`{"plugins":[{"name":"demo","event":"Stop","command":"echo","enabled":true}]}`)
	if err := os.WriteFile(pluginPath, data, 0o600); err != nil {
		t.Fatalf("write plugins.json: %v", err)
	}

	return vault, pluginPath
}

func TestIsPluginsTrusted_TracksTrustedHash(t *testing.T) {
	vault, pluginPath := setupPluginTrustTest(t)

	if isPluginsTrusted(vault, pluginPath) {
		t.Fatal("expected untrusted manifest before trust is granted")
	}

	if err := TrustVaultPlugins(); err != nil {
		t.Fatalf("TrustVaultPlugins: %v", err)
	}
	if !isPluginsTrusted(vault, pluginPath) {
		t.Fatal("expected manifest to be trusted after explicit trust")
	}

	if err := os.WriteFile(pluginPath, []byte(`{"plugins":[{"name":"demo","event":"Stop","command":"printf","enabled":true}]}`), 0o600); err != nil {
		t.Fatalf("mutate plugins.json: %v", err)
	}
	if isPluginsTrusted(vault, pluginPath) {
		t.Fatal("expected trust to be revoked after plugins.json changes")
	}
}

func TestLoadPlugins_TrustGateBlocksUntrustedManifest(t *testing.T) {
	_, pluginPath := setupPluginTrustTest(t)

	var plugins []PluginConfig
	warn := capturePluginTestStderr(t, func() {
		plugins = LoadPlugins()
	})
	if len(plugins) != 0 {
		t.Fatalf("expected no plugins while untrusted, got %#v", plugins)
	}
	if !strings.Contains(warn, "same plugin trust") {
		t.Fatalf("expected trust warning, got %q", warn)
	}

	if err := TrustVaultPlugins(); err != nil {
		t.Fatalf("TrustVaultPlugins: %v", err)
	}

	plugins = LoadPlugins()
	if len(plugins) != 1 {
		t.Fatalf("expected trusted plugin to load, got %#v", plugins)
	}
	if plugins[0].Name != "demo" {
		t.Fatalf("plugin name = %q, want demo", plugins[0].Name)
	}

	if err := os.WriteFile(pluginPath, []byte(`{"plugins":[{"name":"demo","event":"Stop","command":"echo","enabled":false}]}`), 0o600); err != nil {
		t.Fatalf("mutate plugins.json: %v", err)
	}

	warn = capturePluginTestStderr(t, func() {
		plugins = LoadPlugins()
	})
	if len(plugins) != 0 {
		t.Fatalf("expected modified untrusted manifest to be blocked, got %#v", plugins)
	}
	if !strings.Contains(warn, "Skipping plugin loading for safety") {
		t.Fatalf("expected safety warning after manifest change, got %q", warn)
	}
}
