package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
)

func TestRepairCmd_WithValidVault(t *testing.T) {
	vault, db := setupCommandTestVault(t)
	_ = db.Close()

	dbPath := filepath.Join(vault, ".same", "data", "vault.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected database to exist before repair: %v", err)
	}

	var runErr error
	_ = captureCommandStdout(t, func() {
		runErr = runRepair()
	})
	if runErr != nil {
		t.Fatalf("runRepair: %v", runErr)
	}

	if _, err := os.Stat(dbPath + ".bak"); err != nil {
		t.Fatalf("expected backup file to exist after repair: %v", err)
	}
}

func TestRepairCmd_NoVault(t *testing.T) {
	oldOverride := config.VaultOverride
	config.VaultOverride = "/definitely/nonexistent/same-vault-path"
	t.Cleanup(func() { config.VaultOverride = oldOverride })

	err := runRepair()
	if err == nil {
		t.Fatal("expected repair to fail without a valid vault")
	}
	if !errors.Is(err, config.ErrNoDatabase) {
		t.Fatalf("expected ErrNoDatabase, got: %v", err)
	}
}
