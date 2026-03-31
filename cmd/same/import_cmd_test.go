package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
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

func TestImportCmd_FilePermissions(t *testing.T) {
	tmp, _ := setupCommandTestVault(t)

	// Create a source file to import
	srcFile := filepath.Join(tmp, "test-rules.md")
	if err := os.WriteFile(srcFile, []byte("# Test Rules\nSome content"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	_ = captureCommandStdout(t, func() {
		if err := runImport(tmp, srcFile, false); err != nil {
			t.Fatalf("runImport: %v", err)
		}
	})

	// Check imports/ directory permissions
	importsDir := filepath.Join(tmp, "imports")
	info, err := os.Stat(importsDir)
	if err != nil {
		t.Fatalf("stat imports dir: %v", err)
	}
	dirPerm := info.Mode().Perm()
	if dirPerm != 0o700 {
		t.Errorf("imports/ directory permissions = %o, want 0700", dirPerm)
	}

	// Check imported file permissions
	importedFile := filepath.Join(importsDir, "test-rules.md")
	finfo, err := os.Stat(importedFile)
	if err != nil {
		t.Fatalf("stat imported file: %v", err)
	}
	filePerm := finfo.Mode().Perm()
	if filePerm != 0o600 {
		t.Errorf("imported file permissions = %o, want 0600", filePerm)
	}
}

func TestImportCmd_AutoIndexAfterImport(t *testing.T) {
	tmp, database := setupCommandTestVault(t)

	// Verify no notes in DB before import
	countBefore, err := database.NoteCount()
	if err != nil {
		t.Fatalf("NoteCount before: %v", err)
	}
	if countBefore != 0 {
		t.Fatalf("expected 0 notes before import, got %d", countBefore)
	}

	// Create a source file to import
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte("# Claude Instructions\nBuild with go test"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	// Run import — should auto-index
	_ = captureCommandStdout(t, func() {
		if err := runImport(tmp, "", false); err != nil {
			t.Fatalf("runImport: %v", err)
		}
	})

	// Re-open DB to check — the import function opens its own DB connection,
	// so we need to check via a fresh query on our test DB connection.
	// Actually, let's just open a new DB to read results since import opens its own.
	database2, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open for verification: %v", err)
	}
	defer database2.Close()

	// Verify the note is in the database (was auto-indexed)
	countAfter, err := database2.NoteCount()
	if err != nil {
		t.Fatalf("NoteCount after: %v", err)
	}
	if countAfter == 0 {
		t.Error("imported file was NOT auto-indexed — note count is still 0")
	}

	// Also verify via keyword search
	results, err := database2.KeywordSearch([]string{"Claude", "Instructions"}, 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Path == filepath.Join("imports", "claude-md.md") {
			found = true
			break
		}
	}
	if !found {
		t.Error("imported file was NOT auto-indexed — not found in keyword search results")
	}
}

// TestImportProvenanceHealthPipeline is an end-to-end integration test that verifies:
//
//	provenance_source frontmatter -> indexer -> note_sources -> divergence detection
//
// This is the core value proposition of the import feature: SAME can detect when
// the original source of an imported note has changed or been deleted.
func TestImportProvenanceHealthPipeline(t *testing.T) {
	// 1. Create a temp vault directory
	vaultDir, db := setupCommandTestVault(t)

	// 2. Create a fake source file (simulates the original file being imported)
	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "memory.md")
	sourceContent := []byte("# Original Memory\n\nThe user prefers Go over Python.\n")
	if err := os.WriteFile(sourceFile, sourceContent, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(sourceContent))

	// 3. Create a note in imports/ with provenance_source frontmatter
	importsDir := filepath.Join(vaultDir, "imports")
	if err := os.MkdirAll(importsDir, 0o755); err != nil {
		t.Fatalf("mkdir imports: %v", err)
	}
	noteContent := fmt.Sprintf(`---
title: "Imported Memory"
provenance_source: %s
provenance_hash: %s
trust_state: unknown
---

# Imported from Claude Code (global)
# Source: %s

The user prefers Go over Python.
`, sourceFile, sourceHash, sourceFile)
	notePath := filepath.Join(importsDir, "test-memory.md")
	if err := os.WriteFile(notePath, []byte(noteContent), 0o644); err != nil {
		t.Fatalf("write imported note: %v", err)
	}

	// 4. Run the indexer on the vault
	relPath := "imports/test-memory.md"
	if err := indexer.IndexSingleFileLite(db, notePath, relPath, vaultDir); err != nil {
		t.Fatalf("IndexSingleFileLite: %v", err)
	}

	// 5. Query note_sources table — verify the source is recorded
	sources, err := db.GetSourcesForNote(relPath)
	if err != nil {
		t.Fatalf("GetSourcesForNote: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source recorded in note_sources, got %d", len(sources))
	}
	if sources[0].SourcePath != sourceFile {
		t.Errorf("expected source_path %q, got %q", sourceFile, sources[0].SourcePath)
	}
	if sources[0].SourceHash != sourceHash {
		t.Errorf("expected source_hash %q, got %q", sourceHash, sources[0].SourceHash)
	}
	if sources[0].SourceType != "file" {
		t.Errorf("expected source_type 'file', got %q", sources[0].SourceType)
	}

	// 6. Run CheckSourceDivergence — should report NO divergence (hash matches)
	diverged, err := db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence (initial): %v", err)
	}
	if len(diverged) != 0 {
		t.Fatalf("expected 0 divergences when source unchanged, got %d", len(diverged))
	}

	// 7. Modify the source file
	modifiedContent := []byte("# Updated Memory\n\nThe user now prefers Rust over Go.\n")
	if err := os.WriteFile(sourceFile, modifiedContent, 0o644); err != nil {
		t.Fatalf("write modified source: %v", err)
	}

	// 8. Run CheckSourceDivergence — should detect stale (hash diverged)
	diverged, err = db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence (after modify): %v", err)
	}
	if len(diverged) != 1 {
		t.Fatalf("expected 1 divergence after source modification, got %d", len(diverged))
	}
	if diverged[0].NotePath != relPath {
		t.Errorf("diverged note_path: expected %q, got %q", relPath, diverged[0].NotePath)
	}
	if diverged[0].StoredHash != sourceHash {
		t.Errorf("diverged stored_hash: expected %q, got %q", sourceHash, diverged[0].StoredHash)
	}
	expectedModifiedHash := fmt.Sprintf("%x", sha256.Sum256(modifiedContent))
	if diverged[0].CurrentHash != expectedModifiedHash {
		t.Errorf("diverged current_hash: expected %q, got %q", expectedModifiedHash, diverged[0].CurrentHash)
	}

	// 9. Delete the source file
	if err := os.Remove(sourceFile); err != nil {
		t.Fatalf("remove source file: %v", err)
	}

	// 10. Run CheckSourceDivergence — should report diverged (source deleted)
	diverged, err = db.CheckSourceDivergence(vaultDir)
	if err != nil {
		t.Fatalf("CheckSourceDivergence (after delete): %v", err)
	}
	if len(diverged) != 1 {
		t.Fatalf("expected 1 divergence after source deletion, got %d", len(diverged))
	}
	if diverged[0].NotePath != relPath {
		t.Errorf("diverged note_path after delete: expected %q, got %q", relPath, diverged[0].NotePath)
	}
	if diverged[0].CurrentHash != "" {
		t.Errorf("expected empty current_hash for deleted source, got %q", diverged[0].CurrentHash)
	}
}

// TestImportProvenance_TrustBoundary verifies that provenance_source frontmatter
// is ONLY recorded for notes inside the imports/ directory. Notes outside imports/
// must NOT have their provenance recorded, because provenance_source values could
// be attacker-controlled (e.g., via MCP save_note) and point at sensitive files.
func TestImportProvenance_TrustBoundary(t *testing.T) {
	vaultDir, db := setupCommandTestVault(t)

	// Create a source file
	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "secret.md")
	sourceContent := []byte("# Secret File\n\nThis is sensitive content.\n")
	if err := os.WriteFile(sourceFile, sourceContent, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(sourceContent))

	noteTemplate := `---
title: "Note With Provenance"
provenance_source: %s
provenance_hash: %s
---

This note claims provenance from an external file.
`

	// Case 1: Note INSIDE imports/ — provenance SHOULD be recorded
	importsDir := filepath.Join(vaultDir, "imports")
	if err := os.MkdirAll(importsDir, 0o755); err != nil {
		t.Fatalf("mkdir imports: %v", err)
	}
	insidePath := filepath.Join(importsDir, "trusted-note.md")
	if err := os.WriteFile(insidePath, []byte(fmt.Sprintf(noteTemplate, sourceFile, sourceHash)), 0o644); err != nil {
		t.Fatalf("write inside note: %v", err)
	}
	insideRelPath := "imports/trusted-note.md"
	if err := indexer.IndexSingleFileLite(db, insidePath, insideRelPath, vaultDir); err != nil {
		t.Fatalf("IndexSingleFileLite (inside imports): %v", err)
	}

	insideSources, err := db.GetSourcesForNote(insideRelPath)
	if err != nil {
		t.Fatalf("GetSourcesForNote (inside): %v", err)
	}
	if len(insideSources) != 1 {
		t.Errorf("imports/ note: expected 1 provenance source, got %d", len(insideSources))
	}

	// Case 2: Note OUTSIDE imports/ — provenance MUST NOT be recorded
	notesDir := filepath.Join(vaultDir, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	outsidePath := filepath.Join(notesDir, "untrusted-note.md")
	if err := os.WriteFile(outsidePath, []byte(fmt.Sprintf(noteTemplate, sourceFile, sourceHash)), 0o644); err != nil {
		t.Fatalf("write outside note: %v", err)
	}
	outsideRelPath := "notes/untrusted-note.md"
	if err := indexer.IndexSingleFileLite(db, outsidePath, outsideRelPath, vaultDir); err != nil {
		t.Fatalf("IndexSingleFileLite (outside imports): %v", err)
	}

	outsideSources, err := db.GetSourcesForNote(outsideRelPath)
	if err != nil {
		t.Fatalf("GetSourcesForNote (outside): %v", err)
	}
	if len(outsideSources) != 0 {
		t.Errorf("non-imports/ note: expected 0 provenance sources (trust boundary), got %d", len(outsideSources))
	}

	// Case 3: Note at vault root — also MUST NOT be recorded
	rootPath := filepath.Join(vaultDir, "root-note.md")
	if err := os.WriteFile(rootPath, []byte(fmt.Sprintf(noteTemplate, sourceFile, sourceHash)), 0o644); err != nil {
		t.Fatalf("write root note: %v", err)
	}
	rootRelPath := "root-note.md"
	if err := indexer.IndexSingleFileLite(db, rootPath, rootRelPath, vaultDir); err != nil {
		t.Fatalf("IndexSingleFileLite (root): %v", err)
	}

	rootSources, err := db.GetSourcesForNote(rootRelPath)
	if err != nil {
		t.Fatalf("GetSourcesForNote (root): %v", err)
	}
	if len(rootSources) != 0 {
		t.Errorf("root note: expected 0 provenance sources (trust boundary), got %d", len(rootSources))
	}
}
