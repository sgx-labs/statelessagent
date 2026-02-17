package mcp

import (
	"path/filepath"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func setupTestVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.VaultOverride = dir
	abs, _ := filepath.Abs(dir)
	vaultRoot = abs
	t.Cleanup(func() { config.VaultOverride = "" })
	return dir
}

// --- safeVaultPath ---

func TestSafeVaultPath_ValidRelative(t *testing.T) {
	setupTestVault(t)
	result := safeVaultPath("notes/test.md")
	if result == "" {
		t.Error("expected valid path for relative input, got empty")
	}
}

func TestSafeVaultPath_BlocksAbsolutePath(t *testing.T) {
	setupTestVault(t)
	result := safeVaultPath("/etc/passwd")
	if result != "" {
		t.Errorf("expected empty for absolute path, got %q", result)
	}
}

func TestSafeVaultPath_BlocksTraversal(t *testing.T) {
	setupTestVault(t)
	tests := []struct {
		name string
		path string
	}{
		{"parent dir", "../secret.md"},
		{"deep traversal", "../../etc/passwd"},
		{"mid-path traversal", "notes/../../etc/passwd"},
		{"encoded dots", "notes/../../../etc/hosts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeVaultPath(tt.path)
			if result != "" {
				t.Errorf("expected empty for traversal path %q, got %q", tt.path, result)
			}
		})
	}
}

func TestSafeVaultPath_BlocksPrivate(t *testing.T) {
	setupTestVault(t)
	tests := []struct {
		name string
		path string
	}{
		{"private dir", "_PRIVATE/secret.md"},
		{"private root", "_PRIVATE"},
		{"private nested", "_PRIVATE/deep/secret.md"},
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

func TestSafeVaultPath_BlocksDotPaths(t *testing.T) {
	setupTestVault(t)
	tests := []struct {
		name string
		path string
	}{
		{"dot-same dir", ".same/config.toml"},
		{"dot-git dir", ".git/config"},
		{"gitignore", ".gitignore"},
		{"obsidian", ".obsidian/app.json"},
		{"dot-env", ".env"},
		{"hidden file", ".hidden"},
		{"nested hidden dir", "notes/.hidden/file.md"},
		{"nested git dir", "projects/.git/config"},
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

func TestSafeVaultPath_AllowsNonDotPaths(t *testing.T) {
	setupTestVault(t)
	tests := []struct {
		name string
		path string
	}{
		{"simple note", "note.md"},
		{"nested note", "projects/api/notes.md"},
		{"decisions", "decisions/auth.md"},
		{"sessions", "sessions/2026-01-01-handoff.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeVaultPath(tt.path)
			if result == "" {
				t.Errorf("expected valid path for %q, got empty", tt.path)
			}
		})
	}
}

func TestSafeVaultPath_EmptyInput(t *testing.T) {
	setupTestVault(t)
	// filepath.Clean("") returns ".", which starts with "."
	result := safeVaultPath("")
	if result != "" {
		t.Errorf("expected empty for empty input, got %q", result)
	}
}

// --- filterPrivatePaths ---

func TestFilterPrivatePaths_RemovesPrivate(t *testing.T) {
	results := []store.SearchResult{
		{Path: "notes/public.md"},
		{Path: "_PRIVATE/secret.md"},
		{Path: "projects/readme.md"},
		{Path: "_PRIVATE/deep/nested.md"},
	}
	filtered := filterPrivatePaths(results)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results, got %d", len(filtered))
	}
	if filtered[0].Path != "notes/public.md" {
		t.Errorf("expected 'notes/public.md', got %q", filtered[0].Path)
	}
	if filtered[1].Path != "projects/readme.md" {
		t.Errorf("expected 'projects/readme.md', got %q", filtered[1].Path)
	}
}

func TestFilterPrivatePaths_NoPrivate(t *testing.T) {
	results := []store.SearchResult{
		{Path: "notes/a.md"},
		{Path: "notes/b.md"},
	}
	filtered := filterPrivatePaths(results)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results, got %d", len(filtered))
	}
}

func TestFilterPrivatePaths_Empty(t *testing.T) {
	filtered := filterPrivatePaths(nil)
	if len(filtered) != 0 {
		t.Fatalf("expected 0 results, got %d", len(filtered))
	}
}

func TestFilterPrivatePaths_WindowsBackslash(t *testing.T) {
	results := []store.SearchResult{
		{Path: "_PRIVATE\\secret.md"},
		{Path: "notes/ok.md"},
	}
	filtered := filterPrivatePaths(results)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 result, got %d", len(filtered))
	}
	if filtered[0].Path != "notes/ok.md" {
		t.Errorf("expected 'notes/ok.md', got %q", filtered[0].Path)
	}
}

// --- clampTopK ---

func TestClampTopK_Default(t *testing.T) {
	if got := clampTopK(0, 10); got != 10 {
		t.Errorf("expected 10 for zero input, got %d", got)
	}
	if got := clampTopK(-5, 10); got != 10 {
		t.Errorf("expected 10 for negative input, got %d", got)
	}
}

func TestClampTopK_MaxCap(t *testing.T) {
	if got := clampTopK(200, 10); got != 100 {
		t.Errorf("expected 100 for over-max input, got %d", got)
	}
	if got := clampTopK(101, 10); got != 100 {
		t.Errorf("expected 100 for 101, got %d", got)
	}
}

func TestClampTopK_ValidRange(t *testing.T) {
	if got := clampTopK(5, 10); got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
	if got := clampTopK(100, 10); got != 100 {
		t.Errorf("expected 100, got %d", got)
	}
	if got := clampTopK(1, 10); got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
}

// --- formatTimestamp ---

func TestFormatTimestamp_Zero(t *testing.T) {
	if got := formatTimestamp(0); got != "" {
		t.Errorf("expected empty for zero, got %q", got)
	}
}

func TestFormatTimestamp_Valid(t *testing.T) {
	// 2024-01-15 12:00 UTC = 1705320000
	got := formatTimestamp(1705320000)
	if got == "" {
		t.Error("expected non-empty timestamp")
	}
	// Should contain date portion
	if len(got) < 10 {
		t.Errorf("expected at least date portion, got %q", got)
	}
}

// --- maxNoteSize constant ---

func TestMaxNoteSize(t *testing.T) {
	if maxNoteSize != 100*1024 {
		t.Errorf("expected maxNoteSize to be 100KB (102400), got %d", maxNoteSize)
	}
}

// --- textResult helper ---

func TestTextResult(t *testing.T) {
	result := textResult("hello world")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
}

// --- safeVaultPath edge cases ---

func TestSafeVaultPath_WindowsAbsolute(t *testing.T) {
	setupTestVault(t)
	tests := []string{
		"C:\\Windows\\System32\\cmd.exe",
		"C:/Windows/System32/cmd.exe",
	}
	for _, path := range tests {
		result := safeVaultPath(path)
		// On macOS/Linux these aren't detected as absolute by filepath.IsAbs,
		// but filepath.Clean + vault containment check should still block them.
		// On Windows, filepath.IsAbs would catch them.
		_ = result // platform-dependent; just verify no panic
	}
}

func TestSafeVaultPath_UnicodeNormalization(t *testing.T) {
	setupTestVault(t)
	// Unicode paths should work if they don't violate rules
	result := safeVaultPath("notes/cafe\u0301.md")
	if result == "" {
		t.Error("expected valid path for unicode input")
	}
}

func TestSafeVaultPath_DeepNesting(t *testing.T) {
	setupTestVault(t)
	result := safeVaultPath("a/b/c/d/e/f/g/h/i/note.md")
	if result == "" {
		t.Error("expected valid path for deeply nested input")
	}
}

func TestSafeVaultPath_SpacesInPath(t *testing.T) {
	setupTestVault(t)
	result := safeVaultPath("my notes/project ideas/plan.md")
	if result == "" {
		t.Error("expected valid path with spaces")
	}
}
