package hooks

import (
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestShouldSkipPath_BlocksPrivatePrefix(t *testing.T) {
	paths := []string{"_PRIVATE/secret.md", "_private/secret.md", "_Private/nested/secret.md"}
	for _, p := range paths {
		if !shouldSkipPath(p) {
			t.Fatalf("expected private path %q to be skipped", p)
		}
	}
}

func TestKeywordFallbackSearch_ExcludesPrivateNotes(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	recs := []store.NoteRecord{
		{
			Path:         "_PRIVATE/secret.md",
			Title:        "Secret",
			Tags:         "[]",
			ChunkID:      0,
			ChunkHeading: "(full)",
			Text:         "security token secret",
			Modified:     1700000000,
			ContentHash:  "secret-hash",
			ContentType:  "note",
			Confidence:   0.5,
		},
		{
			Path:         "notes/public.md",
			Title:        "Public Security",
			Tags:         "[]",
			ChunkID:      0,
			ChunkHeading: "(full)",
			Text:         "security hardening checklist",
			Modified:     1700000001,
			ContentHash:  "public-hash",
			ContentType:  "note",
			Confidence:   0.5,
		},
	}
	if _, err := db.BulkInsertNotesLite(recs); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}

	prev := keyTermsPrompt
	keyTermsPrompt = "security"
	t.Cleanup(func() { keyTermsPrompt = prev })

	results := keywordFallbackSearch(db)
	if len(results) == 0 {
		t.Fatalf("expected at least one keyword fallback result")
	}
	for _, r := range results {
		if strings.HasPrefix(strings.ToUpper(r.path), "_PRIVATE/") {
			t.Fatalf("private note leaked via keyword fallback: %q", r.path)
		}
	}
}
