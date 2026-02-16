package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestHandleSearchNotes_ExcludesPrivateResults(t *testing.T) {
	setupHandlerTest(t)

	recs := []store.NoteRecord{
		{
			Path:         "_PRIVATE/secret.md",
			Title:        "Secret Security",
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

	res, _, err := handleSearchNotes(context.Background(), nil, searchInput{Query: "security", TopK: 10})
	if err != nil {
		t.Fatalf("handleSearchNotes: %v", err)
	}
	text := resultText(t, res)
	if strings.Contains(strings.ToUpper(text), "_PRIVATE/") {
		t.Fatalf("private note leaked in MCP search result: %q", text)
	}
	if !strings.Contains(text, "notes/public.md") {
		t.Fatalf("expected public result to remain in MCP search output: %q", text)
	}
}
