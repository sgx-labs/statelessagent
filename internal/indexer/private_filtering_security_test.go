package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestReindexLite_SkipsPrivateDirectoryContent(t *testing.T) {
	vault := t.TempDir()
	config.VaultOverride = vault
	t.Cleanup(func() { config.VaultOverride = "" })

	if err := os.MkdirAll(filepath.Join(vault, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(vault, "_PRIVATE"), 0o755); err != nil {
		t.Fatalf("mkdir _PRIVATE: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, "notes", "public.md"), []byte("# Public\nsecret shared safely"), 0o644); err != nil {
		t.Fatalf("write public note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, "_PRIVATE", "secret.md"), []byte("# Secret\ndo not index"), 0o644); err != nil {
		t.Fatalf("write private note: %v", err)
	}

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	if _, err := ReindexLite(context.Background(), db, true, nil); err != nil {
		t.Fatalf("reindex lite: %v", err)
	}

	var privateRows int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM vault_notes WHERE UPPER(path) LIKE '_PRIVATE/%'`).Scan(&privateRows); err != nil {
		t.Fatalf("count private rows: %v", err)
	}
	if privateRows != 0 {
		t.Fatalf("expected no _PRIVATE rows in index, found %d", privateRows)
	}

	results, err := db.KeywordSearch([]string{"secret"}, 10)
	if err != nil {
		t.Fatalf("keyword search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one searchable result")
	}
	for _, r := range results {
		if strings.HasPrefix(strings.ToUpper(r.Path), "_PRIVATE/") {
			t.Fatalf("private note leaked into search results: %q", r.Path)
		}
	}
}
