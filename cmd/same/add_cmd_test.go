package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"JWT auth decision", "jwt-auth-decision"},
		{"Switched from React to Vue", "switched-from-react-to-vue"},
		{"A very long title with many words that should be truncated", "a-very-long-title-with"},
		{"special!@#chars$%^test", "specialcharstest"},
		{"", ""},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"first line\nsecond line", "first-line"},
	}

	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildNoteContent_NoFrontmatter(t *testing.T) {
	content := buildNoteContent("Hello world", nil, "", "")
	if strings.Contains(content, "---") {
		t.Error("should not contain frontmatter when no metadata provided")
	}
	if !strings.Contains(content, "Hello world") {
		t.Error("should contain the note text")
	}
}

func TestBuildNoteContent_WithTags(t *testing.T) {
	content := buildNoteContent("Hello", []string{"auth", "security"}, "", "")
	if !strings.Contains(content, "---") {
		t.Error("should contain frontmatter")
	}
	if !strings.Contains(content, "  - auth") {
		t.Error("should contain auth tag")
	}
	if !strings.Contains(content, "  - security") {
		t.Error("should contain security tag")
	}
}

func TestBuildNoteContent_WithAllMetadata(t *testing.T) {
	content := buildNoteContent("Decision text", []string{"api"}, "decision", "engineering")
	if !strings.Contains(content, "content_type: decision") {
		t.Error("should contain content_type")
	}
	if !strings.Contains(content, "domain: engineering") {
		t.Error("should contain domain")
	}
	if !strings.Contains(content, "  - api") {
		t.Error("should contain api tag")
	}
	if !strings.Contains(content, "Decision text") {
		t.Error("should contain note text after frontmatter")
	}
}

func TestGenerateNotePath(t *testing.T) {
	path := generateNotePath("JWT auth decision")
	if !strings.HasPrefix(path, "notes/") {
		t.Errorf("path should start with notes/, got %s", path)
	}
	if !strings.HasSuffix(path, ".md") {
		t.Errorf("path should end with .md, got %s", path)
	}
	if !strings.Contains(path, "jwt-auth-decision") {
		t.Errorf("path should contain slug, got %s", path)
	}
}

func TestPluralS(t *testing.T) {
	if pluralS(1) != "" {
		t.Error("pluralS(1) should be empty")
	}
	if pluralS(0) != "s" {
		t.Error("pluralS(0) should be 's'")
	}
	if pluralS(5) != "s" {
		t.Error("pluralS(5) should be 's'")
	}
}

func setupAddTestVault(t *testing.T) string {
	t.Helper()

	vault, db := setupCommandTestVault(t)
	_ = db.Close()
	return vault
}

func parseTestNoteFrontmatter(t *testing.T, content string) (noteFrontmatter, map[string]any) {
	t.Helper()

	const start = "---\n"
	const end = "\n---\n\n"

	if !strings.HasPrefix(content, start) {
		t.Fatalf("expected frontmatter prefix, got %q", content)
	}
	rest := strings.TrimPrefix(content, start)
	idx := strings.Index(rest, end)
	if idx < 0 {
		t.Fatalf("expected closing frontmatter delimiter in %q", content)
	}

	frontmatter := rest[:idx]

	var typed noteFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &typed); err != nil {
		t.Fatalf("unmarshal typed frontmatter: %v", err)
	}

	raw := map[string]any{}
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		t.Fatalf("unmarshal raw frontmatter: %v", err)
	}

	return typed, raw
}

func TestRunAdd_RejectsPathTraversal(t *testing.T) {
	vault := setupAddTestVault(t)

	err := runAdd("escape attempt", "../escape.md", nil, "", "")
	if err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if !strings.Contains(err.Error(), "outside the vault boundary") {
		t.Fatalf("expected vault boundary error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(vault), "escape.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file outside vault, got stat error %v", statErr)
	}
}

func TestRunAdd_RejectsSymlinkEscape(t *testing.T) {
	vault := setupAddTestVault(t)
	outside := t.TempDir()

	linkPath := filepath.Join(vault, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	err := runAdd("escape attempt", filepath.Join("outside-link", "escape.md"), nil, "", "")
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if !strings.Contains(err.Error(), "outside the vault boundary") {
		t.Fatalf("expected vault boundary error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file outside symlink target, got stat error %v", statErr)
	}
}

func TestRunAdd_BlocksInternalPaths(t *testing.T) {
	vault := setupAddTestVault(t)

	tests := []string{
		".same/secret.md",
		".git/config.md",
		filepath.Join("notes", "..", ".same", "normalized.md"),
		filepath.Join("notes", ".git", "nested.md"),
	}

	for _, notePath := range tests {
		t.Run(notePath, func(t *testing.T) {
			err := runAdd("blocked path", notePath, nil, "", "")
			if err == nil {
				t.Fatal("expected internal path to be rejected")
			}
			if !strings.Contains(err.Error(), "cannot write to internal path") {
				t.Fatalf("expected internal path error, got %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(vault, notePath)); !os.IsNotExist(statErr) {
				t.Fatalf("expected no file created for blocked path, got stat error %v", statErr)
			}
		})
	}
}

func TestRunAdd_FrontmatterInjectionIsEscaped(t *testing.T) {
	vault := setupAddTestVault(t)

	tags := []string{"prod\nowner: root"}
	contentType := "decision\nadmin: true"
	domain := "eng\npriority: high"

	var runErr error
	_ = captureCommandStdout(t, func() {
		runErr = runAdd("release checklist", "notes/frontmatter-safe.md", tags, contentType, domain)
	})
	if runErr != nil {
		t.Fatalf("runAdd: %v", runErr)
	}

	content, err := os.ReadFile(filepath.Join(vault, "notes", "frontmatter-safe.md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}

	typed, raw := parseTestNoteFrontmatter(t, string(content))
	if len(raw) != 3 {
		t.Fatalf("expected exactly 3 top-level frontmatter keys, got %v", raw)
	}
	for _, unexpected := range []string{"owner", "admin", "priority"} {
		if _, ok := raw[unexpected]; ok {
			t.Fatalf("unexpected injected key %q present in frontmatter: %v", unexpected, raw)
		}
	}
	if len(typed.Tags) != 1 || typed.Tags[0] != tags[0] {
		t.Fatalf("tags = %#v, want %#v", typed.Tags, tags)
	}
	if typed.ContentType != contentType {
		t.Fatalf("content_type = %q, want %q", typed.ContentType, contentType)
	}
	if typed.Domain != domain {
		t.Fatalf("domain = %q, want %q", typed.Domain, domain)
	}

	db, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	notes, err := db.GetNoteByPath("notes/frontmatter-safe.md")
	if err != nil {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if len(notes) == 0 {
		t.Fatal("expected new note to be indexed")
	}
}
