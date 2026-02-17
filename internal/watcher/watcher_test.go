package watcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestWalkDirs_SkipsDefaultPrivateAndMetaDirs(t *testing.T) {
	root := t.TempDir()

	mkdirAll(t, filepath.Join(root, "notes", "nested"))
	mkdirAll(t, filepath.Join(root, "_PRIVATE"))
	mkdirAll(t, filepath.Join(root, ".git"))
	mkdirAll(t, filepath.Join(root, ".same"))

	got := walkDirs(root)
	relSet := make(map[string]bool, len(got))
	for _, p := range got {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("rel path: %v", err)
		}
		relSet[filepath.ToSlash(rel)] = true
	}

	if !relSet["."] {
		t.Fatalf("expected vault root in watched dirs")
	}
	if !relSet["notes"] || !relSet["notes/nested"] {
		t.Fatalf("expected notes dirs to be watched, got: %#v", relSet)
	}
	if relSet["_PRIVATE"] {
		t.Fatalf("expected _PRIVATE to be skipped, got: %#v", relSet)
	}
	if relSet[".git"] {
		t.Fatalf("expected .git to be skipped, got: %#v", relSet)
	}
	if relSet[".same"] {
		t.Fatalf("expected .same to be skipped, got: %#v", relSet)
	}
}

func TestRelativePath_NormalizesToSlash(t *testing.T) {
	vault := filepath.Join("tmp", "vault")
	full := filepath.Join(vault, "notes", "alpha.md")
	got := relativePath(full, vault)
	if got != "notes/alpha.md" {
		t.Fatalf("relativePath = %q, want %q", got, "notes/alpha.md")
	}
}

func TestRemoveFromIndex_DeletesIndexedPath(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	const relPath = "notes/renamed.md"
	insertLiteNote(t, db, relPath)

	vault := t.TempDir()
	removeFromIndex(db, filepath.Join(vault, relPath), vault)

	count, err := db.NoteCount()
	if err != nil {
		t.Fatalf("note count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected note to be removed, count=%d", count)
	}
}

func TestReindexFiles_MissingPathDeletesIndexedEntry(t *testing.T) {
	t.Setenv("SAME_EMBED_PROVIDER", "none")

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	const relPath = "notes/missing.md"
	insertLiteNote(t, db, relPath)

	vault := t.TempDir()
	missingAbs := filepath.Join(vault, relPath)
	reindexFiles(db, []string{missingAbs}, vault)

	count, err := db.NoteCount()
	if err != nil {
		t.Fatalf("note count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected stale note to be removed, count=%d", count)
	}
}

func insertLiteNote(t *testing.T, db *store.DB, relPath string) {
	t.Helper()

	records := []store.NoteRecord{
		{
			Path:         relPath,
			Title:        "Test Note",
			Tags:         "[]",
			ChunkID:      0,
			ChunkHeading: "(full)",
			Text:         "body",
			Modified:     1,
			ContentHash:  "hash",
			ContentType:  "note",
			Confidence:   0.5,
			AccessCount:  0,
		},
	}
	if _, err := db.BulkInsertNotesLite(records); err != nil {
		t.Fatalf("insert lite note: %v", err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
