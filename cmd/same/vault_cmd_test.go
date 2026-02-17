package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeAlias(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"dev", "dev"},
		{"my-project", "my-project"},
		{"../../../etc", "etc"},
		{"../../passwd", "passwd"},
		{"/absolute/path", "absolute_path"},
		{".", "unnamed"},
		{"..", "unnamed"},
		{".hidden", "hidden"},
		{"ok.name", "ok_name"},
		{"a\\b", "a_b"},
		{"normal_alias", "normal_alias"},
		{"", "unnamed"},
		{"\x00evil", "evil"},
		{"___leading", "leading"},
		{"...", "unnamed"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeAlias(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeAlias(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// Every result must be safe: no path separators, no leading dots
			for _, c := range got {
				if c == '/' || c == '\\' || c == '\x00' {
					t.Errorf("sanitizeAlias(%q) = %q contains unsafe character %q", tt.input, got, c)
				}
			}
			if len(got) > 0 && got[0] == '.' {
				t.Errorf("sanitizeAlias(%q) = %q starts with dot", tt.input, got)
			}
		})
	}
}

func TestValidateAlias(t *testing.T) {
	tests := []struct {
		input string
		ok    bool
	}{
		{"dev", true},
		{"my-project", true},
		{"work_notes", true},
		{"vault2", true},
		{"A", true},
		{"a-b-c", true},

		// Invalid
		{"", false},
		{"../etc", false},
		{"/absolute", false},
		{".hidden", false},
		{"has space", false},
		{"has.dot", false},
		{"a/b", false},
		{"a\\b", false},
		{"-starts-with-dash", false},
		{"_starts-with-underscore", false},
		{"has:colon", false},
		{"has@at", false},
		{"\x00null", false},
		{strings.Repeat("a", 65), false},  // too long
		{strings.Repeat("a", 64), true},   // exactly at limit
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := validateAlias(tt.input)
			if tt.ok && err != nil {
				t.Errorf("validateAlias(%q) = %v, expected valid", tt.input, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("validateAlias(%q) = nil, expected error", tt.input)
			}
		})
	}
}

func TestSafeFeedPath(t *testing.T) {
	tests := []struct {
		input string
		safe  bool
	}{
		// Safe paths
		{"notes/auth.md", true},
		{"decisions/2024-01-01.md", true},
		{"deep/nested/path/note.md", true},

		// Traversal attacks
		{"../../../etc/passwd", false},
		{"notes/../../../etc/passwd", false},
		{"notes/../../secret", false},

		// Absolute paths
		{"/etc/passwd", false},
		{"/home/user/notes/test.md", false},

		// Private paths
		{"_PRIVATE/secret.md", false},
		{"_private/secret.md", false},
		{"_Private/test.md", false},

		// Dot-prefixed
		{".same/config.toml", false},
		{".git/config", false},
		{".hidden/note.md", false},
		{"notes/.hidden/test.md", false},

		// Null bytes
		{"notes/test\x00evil.md", false},

		// Empty
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := safeFeedPath(tt.input)
			if tt.safe && got == "" {
				t.Errorf("safeFeedPath(%q) = empty, expected safe path", tt.input)
			}
			if !tt.safe && got != "" {
				t.Errorf("safeFeedPath(%q) = %q, expected rejection", tt.input, got)
			}
		})
	}
}

func TestPathWithinBase(t *testing.T) {
	base := filepath.Join("tmp", "vault")

	tests := []struct {
		name      string
		candidate string
		want      bool
	}{
		{name: "same path", candidate: base, want: true},
		{name: "child path", candidate: filepath.Join(base, "notes", "a.md"), want: true},
		{name: "prefix confusion sibling", candidate: base + "-other", want: false},
		{name: "outside sibling", candidate: filepath.Join("tmp", "other", "a.md"), want: false},
		{name: "outside via parent", candidate: filepath.Join(base, "..", "escape.md"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathWithinBase(base, tt.candidate)
			if got != tt.want {
				t.Fatalf("pathWithinBase(%q, %q) = %v, want %v", base, tt.candidate, got, tt.want)
			}
		})
	}
}
