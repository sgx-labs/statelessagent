package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// --- safeVaultPath: null bytes ---

func TestSafeVaultPath_NullByte(t *testing.T) {
	setupTestVault(t)
	tests := []struct {
		name string
		path string
	}{
		{"null in middle", "notes/te\x00st.md"},
		{"null at start", "\x00notes/test.md"},
		{"null at end", "notes/test.md\x00"},
		{"multiple nulls", "no\x00tes/\x00test.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeVaultPath(tt.path)
			if result != "" {
				t.Errorf("expected empty for null byte path %q, got %q", tt.path, result)
			}
		})
	}
}

// --- safeVaultPath: symlink escape ---

func TestSafeVaultPath_SymlinkEscape(t *testing.T) {
	vault := setupTestVault(t)

	// Create a directory inside the vault
	notesDir := filepath.Join(vault, "notes")
	os.MkdirAll(notesDir, 0o755)

	// Create a symlink inside the vault pointing outside
	outsideDir := t.TempDir() // outside the vault
	outsideFile := filepath.Join(outsideDir, "secret.md")
	os.WriteFile(outsideFile, []byte("secret"), 0o644)

	symlinkPath := filepath.Join(notesDir, "escape")
	err := os.Symlink(outsideDir, symlinkPath)
	if err != nil {
		t.Skip("Cannot create symlinks on this platform")
	}

	// The symlink resolves outside the vault, so should be blocked
	result := safeVaultPath("notes/escape/secret.md")
	if result != "" {
		t.Errorf("expected symlink escape to be blocked, got %q", result)
	}
}

func TestSafeVaultPath_SymlinkWithinVault(t *testing.T) {
	vault := setupTestVault(t)

	// Create real directories and files inside vault
	notesDir := filepath.Join(vault, "notes")
	aliasDir := filepath.Join(vault, "alias")
	os.MkdirAll(notesDir, 0o755)
	os.WriteFile(filepath.Join(notesDir, "test.md"), []byte("test"), 0o644)

	// Create a symlink within the vault to another dir in the vault
	err := os.Symlink(notesDir, aliasDir)
	if err != nil {
		t.Skip("Cannot create symlinks on this platform")
	}

	// Symlink within vault should be allowed
	result := safeVaultPath("alias/test.md")
	if result == "" {
		t.Error("expected symlink within vault to be allowed")
	}
}

// --- safeVaultPath: case-insensitive _PRIVATE ---

func TestSafeVaultPath_CaseInsensitivePrivate(t *testing.T) {
	setupTestVault(t)
	tests := []struct {
		name string
		path string
	}{
		{"uppercase", "_PRIVATE/secret.md"},
		{"lowercase", "_private/secret.md"},
		{"mixed case", "_Private/secret.md"},
		{"mixed case 2", "_pRiVaTe/secret.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeVaultPath(tt.path)
			if result != "" {
				t.Errorf("expected empty for _PRIVATE path %q, got %q", tt.path, result)
			}
		})
	}
}

// --- safeVaultPath: new file in non-existent directory ---

func TestSafeVaultPath_NewFileInNewDir(t *testing.T) {
	setupTestVault(t)
	// Path to a file in a directory that doesn't exist yet
	// (e.g., save_note creating new dirs) should be allowed
	result := safeVaultPath("new-project/design/notes.md")
	if result == "" {
		t.Error("expected valid path for new file in new directory")
	}
}

// --- safeVaultPath: various dot-path patterns ---

func TestSafeVaultPath_DotPathEdgeCases(t *testing.T) {
	setupTestVault(t)
	tests := []struct {
		name string
		path string
	}{
		{"dot only", "."},
		{"double dot", ".."},
		{"dot-hidden nested", ".hidden/file.md"},
		{"nested hidden segment", "notes/.hidden/file.md"},
		{"nested dot-git segment", "notes/.git/config"},
		{"dot-env file", ".env"},
		{"dot-claude", ".claude/settings.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeVaultPath(tt.path)
			if result != "" {
				t.Errorf("expected empty for dot-path %q, got %q", tt.path, result)
			}
		})
	}
}

// --- safeVaultPath: deeply nested allowed paths ---

func TestSafeVaultPath_DeepNestingAllowed(t *testing.T) {
	setupTestVault(t)
	result := safeVaultPath("level1/level2/level3/level4/level5/note.md")
	if result == "" {
		t.Error("expected valid path for deeply nested allowed path")
	}
}

// --- filterPrivatePaths: case-insensitive filtering ---

func TestFilterPrivatePaths_CaseInsensitive(t *testing.T) {
	results := []store.SearchResult{
		{Path: "_PRIVATE/secret.md"},
		{Path: "_private/secret.md"},
		{Path: "_Private/deep/file.md"},
		{Path: "notes/public.md"},
	}
	filtered := filterPrivatePaths(results)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 result after filtering, got %d", len(filtered))
	}
	if filtered[0].Path != "notes/public.md" {
		t.Errorf("expected 'notes/public.md', got %q", filtered[0].Path)
	}
}

// --- Config SSRF prevention tests ---

func TestOllamaURL_SSRFPrevention(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"remote attacker host", "http://evil.example.com:11434", true},
		{"internal IP", "http://10.0.0.1:11434", true},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"DNS rebinding attempt", "http://attacker-controlled.com:11434", true},
		{"file scheme", "file:///etc/passwd", true},
		{"localhost allowed", "http://localhost:11434", false},
		{"loopback allowed", "http://127.0.0.1:11434", false},
		{"ipv6 loopback allowed", "http://[::1]:11434", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OLLAMA_URL", tt.url)
			_, err := config.OllamaURL()
			if tt.wantErr && err == nil {
				t.Errorf("expected error for URL %q, got nil", tt.url)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error for URL %q, got %v", tt.url, err)
			}
		})
	}
}

// --- recordProvenanceSources: auto-injected path validation ---

// seedContextUsage inserts a context_usage row with the given injected_paths.
func seedContextUsage(t *testing.T, paths []string) {
	t.Helper()
	pathsJSON, err := json.Marshal(paths)
	if err != nil {
		t.Fatalf("marshal paths: %v", err)
	}
	ts := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err = db.Conn().Exec(
		`INSERT INTO context_usage (session_id, timestamp, hook_name, injected_paths)
		 VALUES (?, ?, 'test', ?)`,
		fmt.Sprintf("test-%d", time.Now().UnixNano()), ts, string(pathsJSON),
	)
	if err != nil {
		t.Fatalf("seed context_usage: %v", err)
	}
}

func TestRecordProvenance_AutoInjectedTraversalBlocked(t *testing.T) {
	vault := setupHandlerTest(t)

	// Create the note being saved
	os.MkdirAll(filepath.Join(vault, "notes"), 0o755)
	os.WriteFile(filepath.Join(vault, "notes", "saved.md"), []byte("# Saved"), 0o644)

	// Seed context_usage with a traversal path
	seedContextUsage(t, []string{"../../../etc/passwd"})

	// Call recordProvenanceSources with no explicit sources
	recordProvenanceSources("notes/saved.md", nil)

	// Verify no source was recorded for the traversal path
	sources, err := db.GetSourcesForNote("notes/saved.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote: %v", err)
	}
	for _, s := range sources {
		if s.SourcePath == "../../../etc/passwd" {
			t.Error("traversal path was NOT blocked — recorded as provenance source")
		}
	}
}

func TestRecordProvenance_AutoInjectedAbsoluteBlocked(t *testing.T) {
	vault := setupHandlerTest(t)

	// Create the note being saved
	os.MkdirAll(filepath.Join(vault, "notes"), 0o755)
	os.WriteFile(filepath.Join(vault, "notes", "saved.md"), []byte("# Saved"), 0o644)

	// Seed context_usage with an absolute path
	seedContextUsage(t, []string{"/etc/passwd"})

	// Call recordProvenanceSources with no explicit sources
	recordProvenanceSources("notes/saved.md", nil)

	// Verify no source was recorded for the absolute path
	sources, err := db.GetSourcesForNote("notes/saved.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote: %v", err)
	}
	for _, s := range sources {
		if s.SourcePath == "/etc/passwd" {
			t.Error("absolute path was NOT blocked — recorded as provenance source")
		}
	}
}

func TestRecordProvenance_ValidAutoInjectedPath(t *testing.T) {
	vault := setupHandlerTest(t)

	// Create the note being saved and a source note
	os.MkdirAll(filepath.Join(vault, "notes"), 0o755)
	os.WriteFile(filepath.Join(vault, "notes", "saved.md"), []byte("# Saved"), 0o644)
	os.WriteFile(filepath.Join(vault, "notes", "source.md"), []byte("# Source"), 0o644)

	// Seed context_usage with a valid vault-relative path
	seedContextUsage(t, []string{"notes/source.md"})

	// Call recordProvenanceSources with no explicit sources
	recordProvenanceSources("notes/saved.md", nil)

	// Verify the valid source was recorded
	sources, err := db.GetSourcesForNote("notes/saved.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote: %v", err)
	}
	found := false
	for _, s := range sources {
		if s.SourcePath == "notes/source.md" {
			found = true
			if s.SourceType != "note" {
				t.Errorf("expected source_type 'note', got %q", s.SourceType)
			}
			if s.SourceHash == "" {
				t.Error("expected non-empty hash for valid source file")
			}
		}
	}
	if !found {
		t.Error("valid auto-injected path was NOT recorded as provenance source")
	}
}

// --- Vault path validation ---

func TestSafeVaultSubpath_TraversalPrevention(t *testing.T) {
	dir := t.TempDir()
	config.VaultOverride = dir
	defer func() { config.VaultOverride = "" }()

	tests := []struct {
		name   string
		path   string
		wantOK bool
	}{
		{"simple relative", "notes/test.md", true},
		{"parent escape", "../../etc/passwd", false},
		{"deep escape", "a/b/c/../../../../etc/shadow", false},
		{"simple name", "test.md", true},
		{"nested", "deep/nested/path/file.md", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := config.SafeVaultSubpath(tt.path)
			if ok != tt.wantOK {
				t.Errorf("SafeVaultSubpath(%q) ok=%v, want %v", tt.path, ok, tt.wantOK)
			}
		})
	}
}
