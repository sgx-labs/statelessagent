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

// withStdinYes temporarily replaces os.Stdin with a pipe that sends "y\n"
// so importClaudeMemories' confirmation prompt doesn't block.
func withStdinYes(t *testing.T, fn func()) {
	t.Helper()
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe for stdin: %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	if _, err := w.WriteString("y\n"); err != nil {
		t.Fatalf("write to stdin pipe: %v", err)
	}
	w.Close()

	fn()
}

// createClaudeMemoryFile is a helper to create a Claude-style memory file with frontmatter.
func createClaudeMemoryFile(t *testing.T, dir, filename, name, desc, memType, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	var content string
	if name != "" || desc != "" || memType != "" {
		content = "---\n"
		if name != "" {
			content += "name: " + name + "\n"
		}
		if desc != "" {
			content += "description: " + desc + "\n"
		}
		if memType != "" {
			content += "type: " + memType + "\n"
		}
		content += "---\n\n"
	}
	content += body
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}

func TestImportCmd_ClaudeMemoryGlobal(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	// HOME is set to tmp by setupCommandTestVault
	memDir := filepath.Join(tmp, ".claude", "memory")
	createClaudeMemoryFile(t, memDir, "user_role.md",
		"user-role", "User is a data scientist", "user",
		"The user is a data scientist investigating logging.\n")

	var runErr error
	withStdinYes(t, func() {
		_ = captureCommandStdout(t, func() {
			runErr = runImport(tmp, "", false)
		})
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}

	// Verify file created in imports/claude-memory/
	destPath := filepath.Join(tmp, "imports", "claude-memory", "global-user_role.md")
	imported, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	content := string(imported)

	// Verify SAME frontmatter
	if !strings.Contains(content, "trust_state: unknown") {
		t.Error("missing trust_state: unknown in frontmatter")
	}
	if !strings.Contains(content, "name: user-role") {
		t.Error("missing name in frontmatter")
	}
	if !strings.Contains(content, "tags: [claude-memory, user]") {
		t.Error("missing tags in frontmatter")
	}
	// Verify source path in import header
	if !strings.Contains(content, "# Source:") {
		t.Error("missing source path header")
	}
	// Verify original body preserved
	if !strings.Contains(content, "data scientist investigating logging") {
		t.Error("original body not preserved")
	}
}

func TestImportCmd_ClaudeMemoryProject(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	// Create project-scoped memory
	projMemDir := filepath.Join(tmp, ".claude", "projects", "test-project", "memory")
	createClaudeMemoryFile(t, projMemDir, "project_notes.md",
		"project-notes", "Project roadmap", "project",
		"The project is focused on memory integrity.\n")

	var runErr error
	withStdinYes(t, func() {
		_ = captureCommandStdout(t, func() {
			runErr = runImport(tmp, "", false)
		})
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}

	destPath := filepath.Join(tmp, "imports", "claude-memory", "project-project_notes.md")
	imported, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	content := string(imported)

	if !strings.Contains(content, "content_type: project") {
		t.Error("expected content_type: project for project-scoped memory")
	}
	if !strings.Contains(content, "Imported from Claude Code (project)") {
		t.Error("missing project scope in import header")
	}
}

func TestImportCmd_ClaudeMemorySkipIndex(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	memDir := filepath.Join(tmp, ".claude", "memory")
	// Create MEMORY.md (index file — should be skipped)
	createClaudeMemoryFile(t, memDir, "MEMORY.md", "", "", "",
		"# Memory Index\n- [Role](user_role.md)\n")
	// Create a real note
	createClaudeMemoryFile(t, memDir, "real_note.md",
		"real-note", "A real note", "feedback",
		"This is actual content.\n")

	var runErr error
	withStdinYes(t, func() {
		_ = captureCommandStdout(t, func() {
			runErr = runImport(tmp, "", false)
		})
	})
	if runErr != nil {
		t.Fatalf("runImport: %v", runErr)
	}

	// MEMORY.md should NOT be imported
	indexPath := filepath.Join(tmp, "imports", "claude-memory", "global-memory.md")
	if _, err := os.Stat(indexPath); err == nil {
		t.Error("MEMORY.md should have been skipped but was imported")
	}

	// real_note.md should be imported
	realPath := filepath.Join(tmp, "imports", "claude-memory", "global-real_note.md")
	if _, err := os.Stat(realPath); err != nil {
		t.Error("real_note.md should have been imported but was not")
	}
}

func TestImportCmd_ClaudeMemoryDeduplicate(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	memDir := filepath.Join(tmp, ".claude", "memory")
	createClaudeMemoryFile(t, memDir, "test_mem.md",
		"test", "A test memory", "user",
		"Test content.\n")

	// First import
	withStdinYes(t, func() {
		_ = captureCommandStdout(t, func() {
			_ = runImport(tmp, "", false)
		})
	})

	destPath := filepath.Join(tmp, "imports", "claude-memory", "global-test_mem.md")
	if _, err := os.Stat(destPath); err != nil {
		t.Fatal("first import should have created the file")
	}

	// Second import — should skip (already imported)
	var out string
	withStdinYes(t, func() {
		out = captureCommandStdout(t, func() {
			_ = runImport(tmp, "", false)
		})
	})

	if strings.Contains(out, "Import these memories?") {
		t.Error("second import should not prompt — all files already imported")
	}
	if !strings.Contains(out, "already imported") {
		t.Error("second import should mention files were already imported")
	}
}

func TestImportCmd_ClaudeMemoryFrontmatter(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	memDir := filepath.Join(tmp, ".claude", "memory")
	createClaudeMemoryFile(t, memDir, "feedback_testing.md",
		"testing-feedback", "Integration tests must hit real DB", "feedback",
		"Never mock the database in integration tests.\n")

	withStdinYes(t, func() {
		_ = captureCommandStdout(t, func() {
			_ = runImport(tmp, "", false)
		})
	})

	destPath := filepath.Join(tmp, "imports", "claude-memory", "global-feedback_testing.md")
	imported, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(imported)

	// Verify Claude frontmatter fields preserved in SAME format
	if !strings.Contains(content, "name: testing-feedback") {
		t.Error("name not preserved from Claude frontmatter")
	}
	if !strings.Contains(content, "description: Integration tests must hit real DB") {
		t.Error("description not preserved from Claude frontmatter")
	}
	if !strings.Contains(content, "tags: [claude-memory, feedback]") {
		t.Error("Claude type not mapped to SAME tags")
	}
	if !strings.Contains(content, "content_type: note") {
		t.Error("feedback type should map to content_type: note")
	}
}

func TestImportCmd_ClaudeMemoryNoFrontmatter(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	memDir := filepath.Join(tmp, ".claude", "memory")
	// Create a plain markdown file with no frontmatter
	plainContent := "Just some plain notes about the project.\nNo YAML here.\n"
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "plain_note.md"), []byte(plainContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	withStdinYes(t, func() {
		_ = captureCommandStdout(t, func() {
			_ = runImport(tmp, "", false)
		})
	})

	destPath := filepath.Join(tmp, "imports", "claude-memory", "global-plain_note.md")
	imported, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(imported)

	// Default metadata should be applied
	if !strings.Contains(content, "name: plain_note") {
		t.Error("expected name from filename when no frontmatter")
	}
	if !strings.Contains(content, "description: Imported from Claude Code") {
		t.Error("expected default description when no frontmatter")
	}
	if !strings.Contains(content, "trust_state: unknown") {
		t.Error("expected trust_state: unknown")
	}
	// Tags should only have claude-memory (no type to add)
	if !strings.Contains(content, "tags: [claude-memory]") {
		t.Error("expected only claude-memory tag when no type in frontmatter")
	}
	// Original content should be in the body
	if !strings.Contains(content, "Just some plain notes") {
		t.Error("original content not preserved")
	}
}

func TestImportCmd_ClaudeMemoryProvenance(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	memDir := filepath.Join(tmp, ".claude", "memory")
	body := "The user prefers Go over Python.\n"
	createClaudeMemoryFile(t, memDir, "user_pref.md",
		"user-pref", "Language preference", "user", body)

	withStdinYes(t, func() {
		_ = captureCommandStdout(t, func() {
			_ = runImport(tmp, "", false)
		})
	})

	destPath := filepath.Join(tmp, "imports", "claude-memory", "global-user_pref.md")
	imported, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(imported)

	// Verify provenance_source contains the absolute source path
	sourcePath := filepath.Join(memDir, "user_pref.md")
	if !strings.Contains(content, "provenance_source: "+sourcePath) {
		t.Errorf("expected provenance_source: %s in frontmatter", sourcePath)
	}

	// Verify provenance_hash is present and non-empty
	if !strings.Contains(content, "provenance_hash: ") {
		t.Error("expected provenance_hash in frontmatter")
	}
	// Hash should be 64 hex chars (SHA256)
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "provenance_hash: ") {
			hash := strings.TrimPrefix(line, "provenance_hash: ")
			if len(hash) != 64 {
				t.Errorf("expected 64-char SHA256 hash, got %d chars: %q", len(hash), hash)
			}
		}
	}
}

func TestParseClaudeFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantDesc string
		wantType string
		wantBody string
	}{
		{
			name:     "full frontmatter",
			input:    "---\nname: test\ndescription: a test\ntype: user\n---\n\nBody here.",
			wantName: "test",
			wantDesc: "a test",
			wantType: "user",
			wantBody: "Body here.",
		},
		{
			name:     "no frontmatter",
			input:    "Just plain text.",
			wantName: "",
			wantDesc: "",
			wantType: "",
			wantBody: "Just plain text.",
		},
		{
			name:     "quoted values",
			input:    "---\nname: \"quoted name\"\ndescription: 'single quoted'\ntype: feedback\n---\n\nContent.",
			wantName: "quoted name",
			wantDesc: "single quoted",
			wantType: "feedback",
			wantBody: "Content.",
		},
		{
			name:     "partial frontmatter",
			input:    "---\nname: only-name\n---\n\nSome body.",
			wantName: "only-name",
			wantDesc: "",
			wantType: "",
			wantBody: "Some body.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, desc, typ, body := parseClaudeFrontmatter(tt.input)
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if desc != tt.wantDesc {
				t.Errorf("desc = %q, want %q", desc, tt.wantDesc)
			}
			if typ != tt.wantType {
				t.Errorf("type = %q, want %q", typ, tt.wantType)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}
