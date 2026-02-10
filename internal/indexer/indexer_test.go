package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkByHeadings(t *testing.T) {
	body := `## Overview

This is the overview section with some content.

## Design

This section covers the design decisions.

## Implementation

The implementation details go here.
`

	chunks := ChunkByHeadings(body)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// Verify each chunk has a Heading field set
	for i, c := range chunks {
		if c.Heading == "" {
			t.Errorf("chunk %d has empty Heading", i)
		}
	}

	// Verify text is split at heading boundaries: each chunk should contain its heading text
	foundOverview := false
	foundDesign := false
	foundImpl := false
	for _, c := range chunks {
		if strings.Contains(c.Text, "overview section") {
			foundOverview = true
		}
		if strings.Contains(c.Text, "design decisions") {
			foundDesign = true
		}
		if strings.Contains(c.Text, "implementation details") {
			foundImpl = true
		}
	}
	if !foundOverview {
		t.Error("expected a chunk containing 'overview section'")
	}
	if !foundDesign {
		t.Error("expected a chunk containing 'design decisions'")
	}
	if !foundImpl {
		t.Error("expected a chunk containing 'implementation details'")
	}

	// Verify no single chunk contains content from two different sections
	for _, c := range chunks {
		if strings.Contains(c.Text, "overview section") && strings.Contains(c.Text, "design decisions") {
			t.Error("a single chunk should not contain text from two different H2 sections")
		}
	}
}

func TestChunkByHeadingsNoHeadings(t *testing.T) {
	body := `This is plain text without any headings.
It has multiple lines but no markdown heading markers.
Just regular paragraph text that should stay together.`

	chunks := ChunkByHeadings(body)

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for text without headings, got %d", len(chunks))
	}

	// When there are no H2 headings but the text is non-empty, ChunkByHeadings
	// returns the text as an "(intro)" chunk since it precedes any H2 heading.
	if chunks[0].Heading != "(intro)" {
		t.Errorf("expected heading '(intro)', got %q", chunks[0].Heading)
	}

	if chunks[0].Text != body {
		t.Errorf("expected full text preserved, got %q", chunks[0].Text)
	}
}

func TestChunkBySize(t *testing.T) {
	// Build a long string of paragraphs, each ~100 chars, totaling well over 1000 chars
	var paragraphs []string
	for i := 0; i < 20; i++ {
		paragraphs = append(paragraphs, strings.Repeat("word ", 20))
	}
	longText := strings.Join(paragraphs, "\n\n")

	maxSize := 300
	chunks := ChunkBySize(longText, maxSize)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long text, got %d", len(chunks))
	}

	// Verify no chunk exceeds max size
	for i, c := range chunks {
		if len(c.Text) > maxSize {
			t.Errorf("chunk %d exceeds max size: %d > %d", i, len(c.Text), maxSize)
		}
	}

	// Verify all text is preserved: concatenated chunks should equal original (modulo whitespace)
	var reassembled []string
	for _, c := range chunks {
		reassembled = append(reassembled, c.Text)
	}
	joined := strings.Join(reassembled, "\n\n")

	// Normalize whitespace for comparison
	originalNorm := strings.Join(strings.Fields(longText), " ")
	joinedNorm := strings.Join(strings.Fields(joined), " ")

	if originalNorm != joinedNorm {
		t.Errorf("reassembled text does not match original.\nOriginal length: %d\nReassembled length: %d",
			len(originalNorm), len(joinedNorm))
	}
}

func TestParseNote(t *testing.T) {
	content := `---
title: "Test Note"
tags: [go, testing]
content_type: decision
domain: engineering
workstream: api
---

# Test Note

Body content here.
`

	parsed := ParseNote(content)

	if parsed.Meta.Title != "Test Note" {
		t.Errorf("expected title 'Test Note', got %q", parsed.Meta.Title)
	}

	if len(parsed.Meta.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(parsed.Meta.Tags))
	}
	if parsed.Meta.Tags[0] != "go" || parsed.Meta.Tags[1] != "testing" {
		t.Errorf("expected tags [go, testing], got %v", parsed.Meta.Tags)
	}

	if parsed.Meta.ContentType != "decision" {
		t.Errorf("expected content_type 'decision', got %q", parsed.Meta.ContentType)
	}

	if parsed.Meta.Domain != "engineering" {
		t.Errorf("expected domain 'engineering', got %q", parsed.Meta.Domain)
	}

	if parsed.Meta.Workstream != "api" {
		t.Errorf("expected workstream 'api', got %q", parsed.Meta.Workstream)
	}

	if !strings.Contains(parsed.Body, "Body content here.") {
		t.Errorf("expected body to contain 'Body content here.', got %q", parsed.Body)
	}

	if !strings.Contains(parsed.Body, "# Test Note") {
		t.Errorf("expected body to contain '# Test Note', got %q", parsed.Body)
	}
}

func TestParseNoteNoFrontmatter(t *testing.T) {
	content := `# Just a Heading

Some body text without frontmatter delimiters.
`

	parsed := ParseNote(content)

	if parsed.Meta.Title != "" {
		t.Errorf("expected empty title, got %q", parsed.Meta.Title)
	}

	if len(parsed.Meta.Tags) != 0 {
		t.Errorf("expected no tags, got %v", parsed.Meta.Tags)
	}

	if parsed.Meta.ContentType != "" {
		t.Errorf("expected empty content_type, got %q", parsed.Meta.ContentType)
	}

	if parsed.Meta.Domain != "" {
		t.Errorf("expected empty domain, got %q", parsed.Meta.Domain)
	}

	if parsed.Meta.Workstream != "" {
		t.Errorf("expected empty workstream, got %q", parsed.Meta.Workstream)
	}

	if !strings.Contains(parsed.Body, "Just a Heading") {
		t.Errorf("expected body to contain full text, got %q", parsed.Body)
	}

	if !strings.Contains(parsed.Body, "Some body text") {
		t.Errorf("expected body to contain 'Some body text', got %q", parsed.Body)
	}
}

func TestRelativePath(t *testing.T) {
	got := relativePath("/home/user/vault/notes/test.md", "/home/user/vault")
	want := "notes/test.md"
	if got != want {
		t.Errorf("relativePath() = %q, want %q", got, want)
	}
}

func TestWalkVault(t *testing.T) {
	// Create a temp directory structure
	tmpDir, err := os.MkdirTemp("", "walkvault-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some .md files
	mdFiles := []string{
		filepath.Join(tmpDir, "note1.md"),
		filepath.Join(tmpDir, "subdir", "note2.md"),
		filepath.Join(tmpDir, "subdir", "note3.md"),
	}

	// Create subdirectory
	if err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a .git directory that should be skipped
	if err := os.MkdirAll(filepath.Join(tmpDir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write .md files
	for _, f := range mdFiles {
		if err := os.WriteFile(f, []byte("# Test\n\nContent.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Write a .md file inside .git (should be skipped)
	gitMD := filepath.Join(tmpDir, ".git", "readme.md")
	if err := os.WriteFile(gitMD, []byte("# Git internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a non-.md file (should be skipped)
	txtFile := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtFile, []byte("not markdown"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Call walkVault
	found := walkVault(tmpDir)

	// Should find exactly 3 .md files
	if len(found) != 3 {
		t.Fatalf("expected 3 markdown files, got %d: %v", len(found), found)
	}

	// Verify .git directory was skipped
	for _, f := range found {
		if strings.Contains(f, ".git") {
			t.Errorf("found file inside .git directory: %s", f)
		}
	}

	// Verify non-.md files are excluded
	for _, f := range found {
		if !strings.HasSuffix(f, ".md") {
			t.Errorf("found non-.md file: %s", f)
		}
	}

	// Verify the expected files are present
	foundMap := make(map[string]bool)
	for _, f := range found {
		foundMap[f] = true
	}
	for _, expected := range mdFiles {
		if !foundMap[expected] {
			t.Errorf("expected file not found: %s", expected)
		}
	}
}
