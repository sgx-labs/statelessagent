package main

import (
	"strings"
	"testing"
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
