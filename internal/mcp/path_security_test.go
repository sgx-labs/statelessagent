package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeVaultPath_RejectsTraversalNullAbsoluteAndPrivate(t *testing.T) {
	setupTestVault(t)

	tests := []string{
		"../secret.md",
		"notes/../../secret.md",
		"notes/..\\..\\secret.md",
		"notes/evil\x00.md",
		"/etc/passwd",
		"C:/Windows/System32/drivers/etc/hosts",
		"_PRIVATE/secret.md",
		"_private/secret.md",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if got := safeVaultPath(input); got != "" {
				t.Fatalf("expected unsafe path %q to be rejected, got %q", input, got)
			}
		})
	}
}

func TestSafeVaultPath_SymlinkEscapeBlocked(t *testing.T) {
	vault := setupTestVault(t)
	notesDir := filepath.Join(vault, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	if err := os.Symlink(outside, filepath.Join(notesDir, "escape")); err != nil {
		t.Skipf("symlink not available on this platform: %v", err)
	}

	if got := safeVaultPath("notes/escape/secret.md"); got != "" {
		t.Fatalf("expected symlink escape to be blocked, got %q", got)
	}
}

func TestSafeVaultPath_URLEncodedTraversal(t *testing.T) {
	setupTestVault(t)

	tests := []string{
		"notes/%2e%2e%2fsecret.md",       // URL-encoded ../
		"%2e%2e/secret.md",               // URL-encoded ..
		"notes/%2e%2e/secret.md",         // URL-encoded .. in path
		"notes/..%2fsecret.md",           // mixed encoded traversal
		"notes%2f..%2f..%2fsecret.md",    // fully encoded traversal
		"%2Fetc%2Fpasswd",                // URL-encoded absolute path
		"notes/%5c..%5c..%5csecret.md",   // URL-encoded backslash traversal
		"notes/%00evil.md",               // URL-encoded null byte
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if got := safeVaultPath(input); got != "" {
				t.Fatalf("expected URL-encoded path %q to be rejected, got %q", input, got)
			}
		})
	}
}

func TestSafeVaultPath_UnicodeFullwidthTraversal(t *testing.T) {
	setupTestVault(t)

	tests := []string{
		"notes/\uff0e\uff0e/secret.md",     // fullwidth period: .. in fullwidth
		"notes/\uff0e\uff0e\uff0fsecret.md", // fullwidth ../
		"\uff0e\uff0e/secret.md",            // fullwidth .. at start
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if got := safeVaultPath(input); got != "" {
				t.Fatalf("expected Unicode fullwidth path %q to be rejected, got %q", input, got)
			}
		})
	}
}

func TestSafeVaultPath_DanglingSymlinkOutsideVault(t *testing.T) {
	vault := setupTestVault(t)
	notesDir := filepath.Join(vault, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}

	// Create a symlink pointing outside the vault (existing directory)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(notesDir, "escape-link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	// The symlink resolves outside the vault, so it should be blocked
	if got := safeVaultPath("notes/escape-link/secret.md"); got != "" {
		t.Fatalf("expected symlink-escape path to be rejected, got %q", got)
	}
}

func TestSafeVaultPath_SymlinkWithinVaultAllowed(t *testing.T) {
	vault := setupTestVault(t)
	notesDir := filepath.Join(vault, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "ok.md"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := os.Symlink(notesDir, filepath.Join(vault, "alias")); err != nil {
		t.Skipf("symlink not available on this platform: %v", err)
	}

	if got := safeVaultPath("alias/ok.md"); got == "" {
		t.Fatal("expected symlink path within vault to be allowed")
	}
}
