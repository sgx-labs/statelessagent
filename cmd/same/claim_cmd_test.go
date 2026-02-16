package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestRunClaimUpsertAndRelease(t *testing.T) {
	vault := t.TempDir()
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = "" })

	if err := runClaimUpsert("cmd/same/main.go", "codex", store.ClaimTypeWrite, 5*time.Minute); err != nil {
		t.Fatalf("runClaimUpsert: %v", err)
	}

	db, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	claims, err := db.ListActiveClaims()
	if err != nil {
		t.Fatalf("ListActiveClaims: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(claims))
	}
	if claims[0].Path != "cmd/same/main.go" || claims[0].Agent != "codex" || claims[0].Type != store.ClaimTypeWrite {
		t.Fatalf("unexpected claim row: %+v", claims[0])
	}

	if err := runClaimRelease("cmd/same/main.go", "codex"); err != nil {
		t.Fatalf("runClaimRelease: %v", err)
	}

	claims, err = db.ListActiveClaims()
	if err != nil {
		t.Fatalf("ListActiveClaims after release: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("expected no claims after release, got %d", len(claims))
	}
}

func TestRunClaimUpsert_ValidationErrors(t *testing.T) {
	vault := t.TempDir()
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = "" })

	if err := runClaimUpsert("notes/a.md", "", store.ClaimTypeWrite, 5*time.Minute); err == nil {
		t.Fatal("expected error when --agent is missing")
	}

	if err := runClaimUpsert("../outside.md", "codex", store.ClaimTypeWrite, 5*time.Minute); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestRunClaimUpsert_PathOutsideVaultRejected(t *testing.T) {
	vault := t.TempDir()
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = "" })

	other := filepath.Join(os.TempDir(), "same-other-"+time.Now().Format("150405"))
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}

	absOutside := filepath.Join(other, "file.go")
	if err := runClaimUpsert(absOutside, "codex", store.ClaimTypeWrite, 5*time.Minute); err == nil {
		t.Fatal("expected absolute outside path to be rejected")
	}
}

func TestRunClaimList_Empty(t *testing.T) {
	vault := t.TempDir()
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = "" })

	if err := runClaimList(); err != nil {
		t.Fatalf("runClaimList: %v", err)
	}
}
