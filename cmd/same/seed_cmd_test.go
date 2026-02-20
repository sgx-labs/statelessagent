package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/seed"
)

type testManifestCache struct {
	FetchedAt time.Time     `json:"fetched_at"`
	Manifest  seed.Manifest `json:"manifest"`
}

func writeSeedManifestCache(t *testing.T, home string, fetchedAt time.Time, seeds []seed.Seed) {
	t.Helper()

	cachePath := filepath.Join(home, ".config", "same", "seed-manifest.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}

	cache := testManifestCache{
		FetchedAt: fetchedAt,
		Manifest: seed.Manifest{
			SchemaVersion: 1,
			Seeds:         seeds,
		},
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatalf("marshal cache: %v", err)
	}
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

func testSeedEntry() seed.Seed {
	return seed.Seed{
		Name:        "solo-dev-kit",
		DisplayName: "Solo Dev Kit",
		Description: "Starter templates for solo builders.",
		Audience:    "solo builders",
		NoteCount:   12,
		SizeKB:      120,
		Tags:        []string{"productivity", "solo"},
		Path:        "vaults/solo-dev-kit",
		Featured:    true,
	}
}

func TestSeedCmd_ListNoNetwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Force remote fetch failure quickly while allowing stale cache fallback.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")

	writeSeedManifestCache(t, home, time.Now().Add(-2*time.Hour), []seed.Seed{testSeedEntry()})

	cmd := seedListCmd()
	cmd.SetArgs([]string{"--json"})

	var execErr error
	out := captureCommandStdout(t, func() {
		execErr = cmd.Execute()
	})
	if execErr != nil {
		t.Fatalf("seed list: %v", execErr)
	}
	if !strings.Contains(out, "solo-dev-kit") {
		t.Fatalf("expected cached seed in output, got: %q", out)
	}
}

func TestSeedCmd_InfoInvalidName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeSeedManifestCache(t, home, time.Now(), []seed.Seed{testSeedEntry()})

	cmd := seedInfoCmd()
	cmd.SetArgs([]string{"missing-seed"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing seed")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found message, got: %v", err)
	}
}

func TestSeedCmd_InstallInvalidName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeSeedManifestCache(t, home, time.Now(), []seed.Seed{testSeedEntry()})

	cmd := seedInstallCmd()
	cmd.SetArgs([]string{"missing-seed", "--no-index"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing seed")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "same seed list") {
		t.Fatalf("expected recovery hint in error, got: %v", err)
	}
}
