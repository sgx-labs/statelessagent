package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestStalenessHook_StaleNoteTimingShowsSourceMtime(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Create a temp vault with a source file
	vaultPath := t.TempDir()
	sourceFile := filepath.Join(vaultPath, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(sourceFile, []byte("package main"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Set the source file mtime to 3 days ago
	threeDaysAgo := time.Now().Add(-3 * 24 * time.Hour)
	if err := os.Chtimes(sourceFile, threeDaysAgo, threeDaysAgo); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Record a source with a different hash so it shows as diverged
	if err := db.RecordSource("notes/arch.md", "src/main.go", "file", "oldhash"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	context := buildDivergenceContext(db, vaultPath)

	// Should show "3 days ago" not "just now"
	if strings.Contains(context, "just now") {
		t.Errorf("expected source mtime, got 'just now' in context: %s", context)
	}
	if !strings.Contains(context, "days ago") {
		t.Errorf("expected 'days ago' in context, got: %s", context)
	}
}

func TestStalenessHook_DeletedSource(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	vaultPath := t.TempDir()

	// Record a source pointing to a file that doesn't exist
	if err := db.RecordSource("notes/orphan.md", "src/deleted.go", "file", "hash"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	context := buildDivergenceContext(db, vaultPath)

	if !strings.Contains(context, "source deleted") {
		t.Errorf("expected 'source deleted' for missing file, got: %s", context)
	}
}
