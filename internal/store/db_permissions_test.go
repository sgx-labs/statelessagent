//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPathCreatesOwnerOnlyDataPaths(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".same", "data")
	dbPath := filepath.Join(dataDir, "vault.db")

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	assertPathMode(t, dataDir, 0o700)
	assertPathMode(t, dbPath, 0o600)
}

func TestOpenPathHardensExistingPermissiveDataPaths(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".same", "data")
	dbPath := filepath.Join(dataDir, "vault.db")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("create permissive data dir: %v", err)
	}
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatalf("set permissive data dir mode: %v", err)
	}
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatalf("create permissive database: %v", err)
	}
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("set permissive database mode: %v", err)
	}

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	assertPathMode(t, dataDir, 0o700)
	assertPathMode(t, dbPath, 0o600)
}

func assertPathMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode for %s = %#o, want %#o", path, got, want)
	}
}
