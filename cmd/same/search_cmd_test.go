package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func setupCommandTestVault(t *testing.T) (string, *store.DB) {
	t.Helper()

	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}

	oldOverride := config.VaultOverride
	config.VaultOverride = tmp
	t.Cleanup(func() { config.VaultOverride = oldOverride })

	t.Setenv("VAULT_PATH", tmp)
	t.Setenv("SAME_DATA_DIR", dataDir)
	t.Setenv("SAME_EMBED_PROVIDER", "none")
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	db, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return tmp, db
}

func insertCommandTestNote(t *testing.T, db *store.DB, path, title, text string) {
	t.Helper()
	rec := store.NoteRecord{
		Path:         path,
		Title:        title,
		Tags:         "[]",
		ChunkID:      0,
		ChunkHeading: "(full)",
		Text:         text,
		Modified:     float64(time.Now().Unix()),
		ContentHash:  path + "-hash",
		ContentType:  "note",
		Confidence:   0.8,
	}
	if _, err := db.BulkInsertNotesLite([]store.NoteRecord{rec}); err != nil {
		t.Fatalf("BulkInsertNotesLite: %v", err)
	}
}

func captureCommandStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
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

func TestRunSearch_EmptyQuery(t *testing.T) {
	if err := runSearch("", 5, "", false, false); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestRunSearch_WhitespaceQuery(t *testing.T) {
	if err := runSearch("   ", 5, "", false, false); err == nil {
		t.Fatal("expected error for whitespace query")
	}
}

func TestRunSearch_KeywordMode(t *testing.T) {
	_, db := setupCommandTestVault(t)
	insertCommandTestNote(t, db, "auth.md", "Authentication Design", "We decided to use jwt-tokens for authentication.")
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runSearch("jwt-tokens", 5, "", false, false)
	})
	if runErr != nil {
		t.Fatalf("runSearch: %v", runErr)
	}
	if !strings.Contains(out, "Authentication Design") {
		t.Fatalf("expected output to include note title, got: %s", out)
	}
}

func TestRunSearch_NoResults_FewNotes(t *testing.T) {
	_, db := setupCommandTestVault(t)
	insertCommandTestNote(t, db, "a.md", "A", "alpha")
	insertCommandTestNote(t, db, "b.md", "B", "bravo")
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runSearch("not-present-term", 5, "", false, false)
	})
	if runErr != nil {
		t.Fatalf("runSearch: %v", runErr)
	}
	if !strings.Contains(out, "Your vault has only") {
		t.Fatalf("expected few-notes hint, got: %s", out)
	}
}

func TestRunSearch_NoResults_ManyNotes(t *testing.T) {
	_, db := setupCommandTestVault(t)
	for i := 0; i < 10; i++ {
		insertCommandTestNote(t, db, "note-"+string(rune('a'+i))+".md", "Note", "common text")
	}
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runSearch("missing-query-value", 5, "", false, false)
	})
	if runErr != nil {
		t.Fatalf("runSearch: %v", runErr)
	}
	if !strings.Contains(out, "Try different terms") {
		t.Fatalf("expected many-notes hint, got: %s", out)
	}
}

func TestRunSearch_JSONOutput_EmptyResults(t *testing.T) {
	_, db := setupCommandTestVault(t)
	insertCommandTestNote(t, db, "notes/a.md", "A", "alpha")
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runSearch("term-not-found", 5, "", true, false)
	})
	if runErr != nil {
		t.Fatalf("runSearch: %v", runErr)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("expected [] JSON output, got: %q", out)
	}
}

func TestRunSearch_JSONOutput_WithResults(t *testing.T) {
	_, db := setupCommandTestVault(t)
	insertCommandTestNote(t, db, "notes/auth.md", "Auth", "token strategy includes unique-term-123")
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runSearch("unique-term-123", 5, "", true, false)
	})
	if runErr != nil {
		t.Fatalf("runSearch: %v", runErr)
	}

	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("expected valid JSON array, got: %v (%q)", err, out)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one result, got %v", results)
	}
}

func TestRunFederatedSearch_EmptyQuery(t *testing.T) {
	if err := runFederatedSearch("", 5, "", false, false, true, ""); err == nil {
		t.Fatal("expected error for empty federated query")
	}
}

func TestRunRelated_MissingPath(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	t.Setenv("SAME_EMBED_PROVIDER", "openai-compatible")
	t.Setenv("SAME_EMBED_MODEL", "test-embed")
	t.Setenv("SAME_EMBED_BASE_URL", "http://127.0.0.1:11434")

	err := runRelated("notes/missing.md", 5, false, false)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "note not found in index") {
		t.Fatalf("unexpected error: %v", err)
	}
}
