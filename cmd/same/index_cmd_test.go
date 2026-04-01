package main

import (
	"errors"
	"fmt"
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

	err := runReindex(false, false, false)
	if err == nil {
		t.Fatal("expected reindex to fail without a valid vault")
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

func TestAcquireReindexLock_Basic(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SAME_DATA_DIR", dataDir)

	cleanup, err := acquireReindexLock()
	if err != nil {
		t.Fatalf("acquireReindexLock: %v", err)
	}
	defer cleanup()

	lockPath := filepath.Join(dataDir, "reindex.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lockfile to exist: %v", err)
	}

	// Verify PID was written
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	if !strings.Contains(string(data), fmt.Sprintf("%d", os.Getpid())) {
		t.Fatalf("lockfile does not contain our PID, got: %q", string(data))
	}

	// Second acquire should fail
	_, err = acquireReindexLock()
	if err == nil {
		t.Fatal("expected second acquireReindexLock to fail")
	}
	if !strings.Contains(err.Error(), "another reindex is in progress") {
		t.Fatalf("expected 'another reindex is in progress', got: %v", err)
	}

	// After cleanup, lock should be removed
	cleanup()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("expected lockfile removed after cleanup, got: %v", err)
	}
}

func TestAcquireReindexLock_StalePID(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SAME_DATA_DIR", dataDir)

	lockPath := filepath.Join(dataDir, "reindex.lock")
	// Write a lockfile with a dead PID
	if err := os.WriteFile(lockPath, []byte("999999999\n"), 0o600); err != nil {
		t.Fatalf("write stale lockfile: %v", err)
	}

	// Override process liveness check to always report dead
	origCheck := reindexLockProcessExists
	reindexLockProcessExists = func(pid int) bool { return false }
	t.Cleanup(func() { reindexLockProcessExists = origCheck })

	cleanup, err := acquireReindexLock()
	if err != nil {
		t.Fatalf("expected stale lock to be reclaimed: %v", err)
	}
	defer cleanup()

	// Verify new lockfile has our PID
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	if !strings.Contains(string(data), fmt.Sprintf("%d", os.Getpid())) {
		t.Fatalf("lockfile should contain current PID, got: %q", string(data))
	}
}

func TestAcquireReindexLock_LivePID(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SAME_DATA_DIR", dataDir)

	lockPath := filepath.Join(dataDir, "reindex.lock")
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("write live lockfile: %v", err)
	}

	_, err := acquireReindexLock()
	if err == nil {
		t.Fatal("expected live PID lockfile to block acquisition")
	}
	if !strings.Contains(err.Error(), "another reindex is in progress") {
		t.Fatalf("expected 'another reindex is in progress', got: %v", err)
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
