package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportCmd_NoFilesFound(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runImport(tmp, "", false)
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}
	if !strings.Contains(out, "No AI config files found") {
		t.Fatalf("expected no-files message, got: %q", out)
	}
}

func TestImportCmd_AutoDetectCLAUDEMD(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	// Create a CLAUDE.md in the scan directory
	claudeContent := "# Project Rules\n\nUse Go. Write tests."
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runImport(tmp, "", false)
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}
	if !strings.Contains(out, "CLAUDE.md") {
		t.Fatalf("expected CLAUDE.md in output, got: %q", out)
	}
	if !strings.Contains(out, "Imported 1 file") {
		t.Fatalf("expected import count, got: %q", out)
	}

	// Verify the file was created in imports/
	imported, err := os.ReadFile(filepath.Join(tmp, "imports", "claude-md.md"))
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	if !strings.Contains(string(imported), "# Imported from CLAUDE.md") {
		t.Fatalf("expected import header, got: %q", string(imported))
	}
	if !strings.Contains(string(imported), claudeContent) {
		t.Fatalf("expected original content preserved, got: %q", string(imported))
	}
}

func TestImportCmd_AutoDetectMultiple(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	// Create multiple config files
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte("claude rules"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".cursorrules"), []byte("cursor rules"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("agents config"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runImport(tmp, "", false)
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}
	if !strings.Contains(out, "Imported 3 file") {
		t.Fatalf("expected 3 files imported, got: %q", out)
	}
}

func TestImportCmd_ExplicitFile(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	// Create a custom file
	customContent := "# My Custom Rules\nDo things."
	customPath := filepath.Join(tmp, "my-rules.md")
	if err := os.WriteFile(customPath, []byte(customContent), 0o644); err != nil {
		t.Fatalf("write custom file: %v", err)
	}

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runImport(tmp, customPath, false)
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}
	if !strings.Contains(out, "Imported 1 file") {
		t.Fatalf("expected 1 file imported, got: %q", out)
	}

	// Verify the file was created
	imported, err := os.ReadFile(filepath.Join(tmp, "imports", "my-rules.md"))
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	if !strings.Contains(string(imported), customContent) {
		t.Fatalf("expected original content, got: %q", string(imported))
	}
}

func TestImportCmd_ExplicitFileMissing(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	err := runImport(tmp, filepath.Join(tmp, "nonexistent.md"), false)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "File not found") {
		t.Fatalf("expected file-not-found error, got: %v", err)
	}
}

func TestImportCmd_RecursiveScan(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	// Create .github/copilot-instructions.md in a subdirectory
	ghDir := filepath.Join(tmp, ".github")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "copilot-instructions.md"), []byte("copilot rules"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Non-recursive should still find .github/copilot-instructions.md since it's a known path
	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runImport(tmp, "", false)
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}
	if !strings.Contains(out, "copilot-instructions.md") {
		t.Fatalf("expected copilot file detected, got: %q", out)
	}
}

func TestImportCmd_CopilotInstructions(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	ghDir := filepath.Join(tmp, ".github")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	copilotContent := "Use TypeScript. Follow ESLint."
	if err := os.WriteFile(filepath.Join(ghDir, "copilot-instructions.md"), []byte(copilotContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runImport(tmp, "", false)
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}
	if !strings.Contains(out, "Imported 1 file") {
		t.Fatalf("expected import, got: %q", out)
	}

	imported, err := os.ReadFile(filepath.Join(tmp, "imports", "copilot-instructions.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(imported), copilotContent) {
		t.Fatalf("expected copilot content, got: %q", string(imported))
	}
}

func TestImportCmd_SanitizeSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"CLAUDE.md", "claude.md"},
		{".cursorrules", ".cursorrules.md"},
		{"My Rules.md", "my-rules.md"},
		{"AGENTS.md", "agents.md"},
	}
	for _, tt := range tests {
		got := sanitizeSlug(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestImportCmd_ImportHeaderFormat(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = captureCommandStdout(t, func() {
		_ = runImport(tmp, "", false)
	})

	imported, err := os.ReadFile(filepath.Join(tmp, "imports", "claude-md.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	lines := strings.Split(string(imported), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "# Imported from CLAUDE.md") {
		t.Fatalf("expected import source header, got: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "# Imported: ") {
		t.Fatalf("expected import date header, got: %q", lines[1])
	}
	if lines[2] != "" {
		t.Fatalf("expected blank line after headers, got: %q", lines[2])
	}
}
