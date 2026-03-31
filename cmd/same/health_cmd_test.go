package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthCmd_StaleNoteTimingShowsSourceMtime(t *testing.T) {
	tmp, db := setupCommandTestVault(t)

	// Create a note file in the vault
	notePath := filepath.Join(tmp, "notes", "arch.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("# Architecture\nSome content."), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	// Create a source file and set its mtime to 5 days ago
	sourceFile := filepath.Join(tmp, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(sourceFile, []byte("package main // changed"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	fiveDaysAgo := time.Now().Add(-5 * 24 * time.Hour)
	if err := os.Chtimes(sourceFile, fiveDaysAgo, fiveDaysAgo); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Index the note so it exists in the DB
	if _, err := db.Conn().Exec(
		`INSERT INTO vault_notes (path, title, chunk_id, chunk_heading, text, modified, content_hash)
		 VALUES (?, '', 0, '', ?, unixepoch(), 'testhash')`,
		"notes/arch.md", "Architecture content",
	); err != nil {
		t.Fatalf("insert note: %v", err)
	}

	// Record source with a different hash so CheckSourceDivergence reports it
	if err := db.RecordSource("notes/arch.md", "src/main.go", "file", "oldhash"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	// Run health and capture output
	out := captureCommandStdout(t, func() {
		_ = runHealth()
	})

	// The display should show "5 days ago" based on the file mtime, not "just now"
	if strings.Contains(out, "just now") {
		t.Errorf("stale note should show source mtime, not 'just now'. Output: %s", out)
	}
	// Should show "days ago" since we set it to 5 days ago
	if strings.Contains(out, "main.go") && !strings.Contains(out, "days ago") && !strings.Contains(out, "source deleted") {
		t.Errorf("expected 'days ago' for stale source display. Output: %s", out)
	}
}

func TestHealthCmd_DeletedSource(t *testing.T) {
	tmp, db := setupCommandTestVault(t)

	// Create a note file
	notePath := filepath.Join(tmp, "notes", "orphan.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("# Orphan\nContent."), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	// Index it
	if _, err := db.Conn().Exec(
		`INSERT INTO vault_notes (path, title, chunk_id, chunk_heading, text, modified, content_hash)
		 VALUES (?, '', 0, '', ?, unixepoch(), 'testhash')`,
		"notes/orphan.md", "Orphan content",
	); err != nil {
		t.Fatalf("insert note: %v", err)
	}

	// Record source pointing to a file that doesn't exist (never created)
	if err := db.RecordSource("notes/orphan.md", "src/deleted.go", "file", "hash123"); err != nil {
		t.Fatalf("RecordSource: %v", err)
	}

	out := captureCommandStdout(t, func() {
		_ = runHealth()
	})

	// Should show "source deleted" for the missing file
	if strings.Contains(out, "deleted.go") && !strings.Contains(out, "source deleted") {
		t.Errorf("expected 'source deleted' for missing source file. Output: %s", out)
	}
}
