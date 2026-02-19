package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
)

func TestRunReindex_NoVault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}

	vault := t.TempDir()
	oldOverride := config.VaultOverride
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)
	t.Setenv("SAME_DATA_DIR", "")
	t.Setenv("SAME_EMBED_PROVIDER", "none")

	if err := os.Chmod(vault, 0o500); err != nil {
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(vault, 0o700) })

	err := runReindex(false, false)
	if !errors.Is(err, config.ErrNoDatabase) {
		t.Fatalf("expected ErrNoDatabase, got: %v", err)
	}
}

func TestRunStats_NoVault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}

	vault := t.TempDir()
	oldOverride := config.VaultOverride
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", vault)
	t.Setenv("SAME_DATA_DIR", "")

	if err := os.Chmod(vault, 0o500); err != nil {
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(vault, 0o700) })

	err := runStats()
	if !errors.Is(err, config.ErrNoDatabase) {
		t.Fatalf("expected ErrNoDatabase, got: %v", err)
	}
}

func TestRunStats_WithVault(t *testing.T) {
	_, db := setupCommandTestVault(t)
	insertCommandTestNote(t, db, filepath.ToSlash("notes/stats.md"), "Stats Note", "stats test text")
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runStats()
	})
	if runErr != nil {
		t.Fatalf("runStats: %v", runErr)
	}
	if !strings.Contains(out, "Index Statistics") {
		t.Fatalf("expected stats header in output, got: %q", out)
	}
}
