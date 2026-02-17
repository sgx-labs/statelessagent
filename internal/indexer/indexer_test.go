package indexer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

type failingEmbeddingProvider struct{}

func (f failingEmbeddingProvider) GetEmbedding(text string, purpose string) ([]float32, error) {
	return nil, fmt.Errorf("embedding backend unavailable")
}

func (f failingEmbeddingProvider) GetDocumentEmbedding(text string) ([]float32, error) {
	return nil, fmt.Errorf("embedding backend unavailable")
}

func (f failingEmbeddingProvider) GetQueryEmbedding(text string) ([]float32, error) {
	return nil, fmt.Errorf("embedding backend unavailable")
}

func (f failingEmbeddingProvider) Name() string {
	return "failing"
}

func (f failingEmbeddingProvider) Model() string {
	return "failing-model"
}

func (f failingEmbeddingProvider) Dimensions() int {
	return 768
}

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

func TestChunkByHeadingsWithIntro(t *testing.T) {
	body := `Some introductory text before any heading.

## Section One

Content of section one.

## Section Two

Content of section two.
`
	chunks := ChunkByHeadings(body)

	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks (intro + 2 sections), got %d", len(chunks))
	}

	if chunks[0].Heading != "(intro)" {
		t.Errorf("expected first chunk heading '(intro)', got %q", chunks[0].Heading)
	}
	if !strings.Contains(chunks[0].Text, "introductory text") {
		t.Error("intro chunk should contain introductory text")
	}
	if chunks[1].Heading != "Section One" {
		t.Errorf("expected second chunk heading 'Section One', got %q", chunks[1].Heading)
	}
	if chunks[2].Heading != "Section Two" {
		t.Errorf("expected third chunk heading 'Section Two', got %q", chunks[2].Heading)
	}
}

func TestChunkByHeadingsEmptySection(t *testing.T) {
	// H2 with no body text should be skipped
	body := `## Empty Section

## Has Content

Actual content here.
`
	chunks := ChunkByHeadings(body)

	// Only the section with content should appear
	found := false
	for _, c := range chunks {
		if strings.Contains(c.Text, "Actual content") {
			found = true
		}
	}
	if !found {
		t.Error("expected chunk with 'Actual content'")
	}
}

func TestChunkByHeadingsEmptyBody(t *testing.T) {
	chunks := ChunkByHeadings("")

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for empty body, got %d", len(chunks))
	}
	if chunks[0].Heading != "(full)" {
		t.Errorf("expected heading '(full)' for empty body, got %q", chunks[0].Heading)
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

func TestChunkBySizeSingleParagraph(t *testing.T) {
	text := "A short paragraph that fits in one chunk."
	chunks := ChunkBySize(text, 1000)

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != text {
		t.Errorf("expected text preserved, got %q", chunks[0].Text)
	}
	if chunks[0].Heading != "(part 1)" {
		t.Errorf("expected heading '(part 1)', got %q", chunks[0].Heading)
	}
}

func TestChunkBySizeZeroMaxChars(t *testing.T) {
	// maxChars <= 0 should fall back to config.MaxEmbedChars
	text := "Short text."
	chunks := ChunkBySize(text, 0)

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk with zero maxChars, got %d", len(chunks))
	}
	if chunks[0].Text != text {
		t.Errorf("expected text preserved, got %q", chunks[0].Text)
	}
}

func TestChunkBySizePartLabeling(t *testing.T) {
	// Verify "(part N)" labels are sequential
	var paragraphs []string
	for i := 0; i < 10; i++ {
		paragraphs = append(paragraphs, strings.Repeat("x", 100))
	}
	text := strings.Join(paragraphs, "\n\n")
	chunks := ChunkBySize(text, 250)

	for i, c := range chunks {
		expected := "(part " + strings.TrimSpace(strings.Replace(c.Heading, "(part ", "", 1))
		if c.Heading != expected {
			t.Errorf("chunk %d: expected heading matching (part N) pattern, got %q", i, c.Heading)
		}
	}
}

func TestParseNote(t *testing.T) {
	content := `---
title: "Test Note"
tags: [go, testing]
content_type: decision
domain: engineering
workstream: api
agent: codex
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
	if parsed.Meta.Agent != "codex" {
		t.Errorf("expected agent 'codex', got %q", parsed.Meta.Agent)
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

func TestParseNoteReviewByAlt(t *testing.T) {
	content := `---
title: "Review Note"
review-by: "2026-06-01"
---

Body text.
`
	parsed := ParseNote(content)

	if parsed.Meta.ReviewBy != "2026-06-01" {
		t.Errorf("expected ReviewBy '2026-06-01' from alternate key, got %q", parsed.Meta.ReviewBy)
	}
}

func TestParseNoteReviewByPrimary(t *testing.T) {
	content := `---
title: "Review Note"
review_by: "2026-03-15"
review-by: "2026-06-01"
---

Body text.
`
	parsed := ParseNote(content)

	// Primary review_by should take precedence
	if parsed.Meta.ReviewBy != "2026-03-15" {
		t.Errorf("expected ReviewBy '2026-03-15' (primary key), got %q", parsed.Meta.ReviewBy)
	}
}

func TestParseNoteEmptyContent(t *testing.T) {
	parsed := ParseNote("")

	if parsed.Meta.Title != "" {
		t.Errorf("expected empty title for empty content, got %q", parsed.Meta.Title)
	}
}

func TestRelativePath(t *testing.T) {
	got := relativePath("/home/user/vault/notes/test.md", "/home/user/vault")
	want := "notes/test.md"
	if got != want {
		t.Errorf("relativePath() = %q, want %q", got, want)
	}
}

func TestRelativePathSameDir(t *testing.T) {
	got := relativePath("/home/user/vault/test.md", "/home/user/vault")
	want := "test.md"
	if got != want {
		t.Errorf("relativePath() = %q, want %q", got, want)
	}
}

func TestBuildRecords_AllEmbeddingsFailReturnsError(t *testing.T) {
	dir := t.TempDir()
	filePath := writeTestNote(t, dir, "note.md", "# Example\n\nThis note should fail embedding.")

	_, _, _, err := buildRecords(filePath, "note.md", dir, failingEmbeddingProvider{})
	if !errors.Is(err, errNoEmbeddingsForFile) {
		t.Fatalf("expected errNoEmbeddingsForFile, got %v", err)
	}
}

func TestSha256Hash(t *testing.T) {
	// Known SHA-256 of empty string
	got := sha256Hash("")
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("sha256Hash('') = %q, want %q", got, want)
	}

	// Deterministic: same input → same output
	h1 := sha256Hash("hello world")
	h2 := sha256Hash("hello world")
	if h1 != h2 {
		t.Error("sha256Hash should be deterministic")
	}

	// Different input → different output
	h3 := sha256Hash("hello world!")
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
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

func TestWalkVaultSkipDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directories that should be skipped
	skipDirs := []string{"_PRIVATE", ".obsidian", ".same", ".claude", ".trash", "node_modules", ".logseq"}
	for _, d := range skipDirs {
		dir := filepath.Join(tmpDir, d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "secret.md"), []byte("# Private\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a normal .md file
	if err := os.WriteFile(filepath.Join(tmpDir, "note.md"), []byte("# Public\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := walkVault(tmpDir)

	if len(found) != 1 {
		t.Fatalf("expected 1 file (only note.md), got %d: %v", len(found), found)
	}

	for _, f := range found {
		for _, d := range skipDirs {
			if strings.Contains(f, d) {
				t.Errorf("found file inside skip directory %s: %s", d, f)
			}
		}
	}
}

func TestWalkVaultSkipFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// CLAUDE.md should be skipped
	if err := os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte("# Claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Normal file should be found
	if err := os.WriteFile(filepath.Join(tmpDir, "notes.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := walkVault(tmpDir)

	if len(found) != 1 {
		t.Fatalf("expected 1 file (only notes.md), got %d: %v", len(found), found)
	}
	if !strings.HasSuffix(found[0], "notes.md") {
		t.Errorf("expected notes.md, got %s", found[0])
	}
}

func TestWalkVaultEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	found := walkVault(tmpDir)

	if len(found) != 0 {
		t.Fatalf("expected 0 files for empty dir, got %d", len(found))
	}
}

func TestWalkVaultExported(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "test.md"), []byte("# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := WalkVault(tmpDir)
	if len(found) != 1 {
		t.Fatalf("WalkVault: expected 1 file, got %d", len(found))
	}
}

func TestCountMarkdownFiles(t *testing.T) {
	tmpDir := t.TempDir()

	files := []string{"a.md", "b.md", "c.md"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("# Note\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Non-md file
	if err := os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}

	count := CountMarkdownFiles(tmpDir)
	if count != 3 {
		t.Errorf("CountMarkdownFiles: expected 3, got %d", count)
	}
}

func TestCountMarkdownFilesEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	count := CountMarkdownFiles(tmpDir)
	if count != 0 {
		t.Errorf("CountMarkdownFiles: expected 0 for empty dir, got %d", count)
	}
}

// setupTestVault creates a temporary vault directory with test markdown files
// and sets up config.VaultOverride to point at it.
func setupTestVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.VaultOverride = dir
	t.Cleanup(func() { config.VaultOverride = "" })

	// Create .same/data directory for saveStats
	if err := os.MkdirAll(filepath.Join(dir, ".same", "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	return dir
}

func writeTestNote(t *testing.T, dir, relPath, content string) string {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return fullPath
}

func TestBuildRecordsLite(t *testing.T) {
	dir := t.TempDir()
	content := `---
title: "Architecture Decision"
tags: [api, design]
content_type: decision
domain: engineering
workstream: backend
review_by: "2026-12-01"
---

# Architecture Decision

We decided to use REST over GraphQL for the public API.

## Rationale

REST is simpler and better suited for our use case.
`
	filePath := writeTestNote(t, dir, "decisions/api-design.md", content)
	relPath := "decisions/api-design.md"

	records, _, err := buildRecordsLite(filePath, relPath, dir)
	if err != nil {
		t.Fatalf("buildRecordsLite: %v", err)
	}

	if len(records) == 0 {
		t.Fatal("expected at least 1 record")
	}

	rec := records[0]
	if rec.Path != relPath {
		t.Errorf("expected path %q, got %q", relPath, rec.Path)
	}
	if rec.Title != "Architecture Decision" {
		t.Errorf("expected title 'Architecture Decision', got %q", rec.Title)
	}
	if rec.Domain != "engineering" {
		t.Errorf("expected domain 'engineering', got %q", rec.Domain)
	}
	if rec.Workstream != "backend" {
		t.Errorf("expected workstream 'backend', got %q", rec.Workstream)
	}
	if rec.ReviewBy != "2026-12-01" {
		t.Errorf("expected review_by '2026-12-01', got %q", rec.ReviewBy)
	}
	if rec.ContentHash == "" {
		t.Error("expected non-empty content hash")
	}
	if rec.Modified == 0 {
		t.Error("expected non-zero modified timestamp")
	}
	if rec.Confidence <= 0 {
		t.Error("expected positive confidence")
	}

	// Tags should be JSON array
	var tags []string
	if err := json.Unmarshal([]byte(rec.Tags), &tags); err != nil {
		t.Errorf("tags should be valid JSON: %v", err)
	}
	if len(tags) != 2 || tags[0] != "api" || tags[1] != "design" {
		t.Errorf("expected tags [api, design], got %v", tags)
	}
}

func TestBuildRecordsLiteTitleFromFilename(t *testing.T) {
	dir := t.TempDir()
	content := "# No Frontmatter\n\nJust body text.\n"
	filePath := writeTestNote(t, dir, "my-note.md", content)

	records, _, err := buildRecordsLite(filePath, "my-note.md", dir)
	if err != nil {
		t.Fatalf("buildRecordsLite: %v", err)
	}

	if len(records) == 0 {
		t.Fatal("expected at least 1 record")
	}
	if records[0].Title != "my-note" {
		t.Errorf("expected title 'my-note' (from filename), got %q", records[0].Title)
	}
}

func TestBuildRecordsLiteNilTags(t *testing.T) {
	dir := t.TempDir()
	content := `---
title: "No Tags"
---

Body.
`
	filePath := writeTestNote(t, dir, "note.md", content)

	records, _, err := buildRecordsLite(filePath, "note.md", dir)
	if err != nil {
		t.Fatalf("buildRecordsLite: %v", err)
	}

	if len(records) == 0 {
		t.Fatal("expected at least 1 record")
	}
	if records[0].Tags != "[]" {
		t.Errorf("expected tags '[]' for nil tags, got %q", records[0].Tags)
	}
}

func TestBuildRecordsLiteChunking(t *testing.T) {
	dir := t.TempDir()

	// Build a note larger than ChunkTokenThreshold (6000 chars)
	var body strings.Builder
	body.WriteString("---\ntitle: \"Long Note\"\n---\n\n")
	body.WriteString("## Section One\n\n")
	body.WriteString(strings.Repeat("Content for section one. ", 200))
	body.WriteString("\n\n## Section Two\n\n")
	body.WriteString(strings.Repeat("Content for section two. ", 200))

	filePath := writeTestNote(t, dir, "long.md", body.String())

	records, _, err := buildRecordsLite(filePath, "long.md", dir)
	if err != nil {
		t.Fatalf("buildRecordsLite: %v", err)
	}

	if len(records) < 2 {
		t.Errorf("expected multiple chunks for long note, got %d", len(records))
	}

	// Verify chunk IDs are sequential
	for i, rec := range records {
		if rec.ChunkID != i {
			t.Errorf("expected chunk_id %d, got %d", i, rec.ChunkID)
		}
	}

	// All records should share the same path and content hash
	for i, rec := range records {
		if rec.Path != "long.md" {
			t.Errorf("record %d: expected path 'long.md', got %q", i, rec.Path)
		}
		if rec.ContentHash != records[0].ContentHash {
			t.Errorf("record %d: content hash differs from record 0", i)
		}
	}
}

func TestBuildRecordsLiteTextTruncation(t *testing.T) {
	dir := t.TempDir()

	// Create a note with a single chunk that exceeds 10000 chars
	content := "---\ntitle: \"Huge\"\n---\n\n" + strings.Repeat("x", 12000)
	filePath := writeTestNote(t, dir, "huge.md", content)

	records, _, err := buildRecordsLite(filePath, "huge.md", dir)
	if err != nil {
		t.Fatalf("buildRecordsLite: %v", err)
	}

	for _, rec := range records {
		if len(rec.Text) > 10000 {
			t.Errorf("text exceeds 10000 chars: %d", len(rec.Text))
		}
	}
}

func TestBuildRecordsLiteFileNotFound(t *testing.T) {
	_, _, err := buildRecordsLite("/nonexistent/file.md", "file.md", "/nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReindexLiteForce(t *testing.T) {
	vaultDir := setupTestVault(t)

	writeTestNote(t, vaultDir, "note1.md", `---
title: "Note One"
tags: [test]
---

# Note One

First test note content.
`)
	writeTestNote(t, vaultDir, "subdir/note2.md", `---
title: "Note Two"
---

# Note Two

Second test note with more content.
`)

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	stats, err := ReindexLite(db, true, nil)
	if err != nil {
		t.Fatalf("ReindexLite: %v", err)
	}

	if stats.TotalFiles != 2 {
		t.Errorf("expected 2 total files, got %d", stats.TotalFiles)
	}
	if stats.NewlyIndexed != 2 {
		t.Errorf("expected 2 newly indexed, got %d", stats.NewlyIndexed)
	}
	if stats.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", stats.Errors)
	}
	if stats.NotesInIndex == 0 {
		t.Error("expected non-zero notes in index")
	}
	if stats.ChunksInIndex == 0 {
		t.Error("expected non-zero chunks in index")
	}
	if stats.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}

	// Verify notes are in the database
	noteCount, err := db.NoteCount()
	if err != nil {
		t.Fatalf("NoteCount: %v", err)
	}
	if noteCount != 2 {
		t.Errorf("expected 2 notes in DB, got %d", noteCount)
	}
}

func TestReindexLiteIncremental(t *testing.T) {
	vaultDir := setupTestVault(t)

	// Use files without frontmatter so that body == full content (hashes match
	// between the incremental check and the stored content_hash).
	writeTestNote(t, vaultDir, "note1.md", "Content one.\n")
	writeTestNote(t, vaultDir, "note2.md", "Content two.\n")

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// First indexing (force)
	stats1, err := ReindexLite(db, true, nil)
	if err != nil {
		t.Fatalf("first ReindexLite: %v", err)
	}
	if stats1.NewlyIndexed != 2 {
		t.Errorf("first run: expected 2 newly indexed, got %d", stats1.NewlyIndexed)
	}

	// Second indexing (incremental, no changes) — should skip both
	stats2, err := ReindexLite(db, false, nil)
	if err != nil {
		t.Fatalf("second ReindexLite: %v", err)
	}
	if stats2.SkippedUnchanged != 2 {
		t.Errorf("second run: expected 2 skipped, got %d", stats2.SkippedUnchanged)
	}
	if stats2.NewlyIndexed != 0 {
		t.Errorf("second run: expected 0 newly indexed, got %d", stats2.NewlyIndexed)
	}

	// Modify one note and re-index
	writeTestNote(t, vaultDir, "note1.md", "Updated content.\n")

	stats3, err := ReindexLite(db, false, nil)
	if err != nil {
		t.Fatalf("third ReindexLite: %v", err)
	}
	if stats3.SkippedUnchanged != 1 {
		t.Errorf("third run: expected 1 skipped, got %d", stats3.SkippedUnchanged)
	}
	if stats3.NewlyIndexed != 1 {
		t.Errorf("third run: expected 1 newly indexed, got %d", stats3.NewlyIndexed)
	}
}

func TestReindexLiteWithProgress(t *testing.T) {
	vaultDir := setupTestVault(t)

	writeTestNote(t, vaultDir, "note.md", "---\ntitle: \"Progress Test\"\n---\n\nContent.\n")

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	var progressCalls int
	progress := func(current, total int, path string) {
		progressCalls++
		if total <= 0 {
			t.Errorf("expected positive total, got %d", total)
		}
	}

	_, err = ReindexLite(db, true, progress)
	if err != nil {
		t.Fatalf("ReindexLite: %v", err)
	}

	if progressCalls == 0 {
		t.Error("expected progress callback to be called")
	}
}

func TestReindexLiteSkipsPrivateDir(t *testing.T) {
	vaultDir := setupTestVault(t)

	writeTestNote(t, vaultDir, "public.md", "# Public\n\nPublic content.\n")
	writeTestNote(t, vaultDir, "_PRIVATE/secret.md", "# Secret\n\nPrivate content.\n")

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	stats, err := ReindexLite(db, true, nil)
	if err != nil {
		t.Fatalf("ReindexLite: %v", err)
	}

	if stats.TotalFiles != 1 {
		t.Errorf("expected 1 total file (private skipped), got %d", stats.TotalFiles)
	}
}

func TestReindexLiteSetsMetadata(t *testing.T) {
	vaultDir := setupTestVault(t)
	writeTestNote(t, vaultDir, "note.md", "# Test\n\nContent.\n")

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	Version = "test-version"
	defer func() { Version = "" }()

	_, err = ReindexLite(db, true, nil)
	if err != nil {
		t.Fatalf("ReindexLite: %v", err)
	}

	mode, ok := db.GetMeta("index_mode")
	if !ok {
		t.Fatal("GetMeta index_mode: not found")
	}
	if mode != "lite" {
		t.Errorf("expected index_mode 'lite', got %q", mode)
	}

	ver, ok := db.GetMeta("same_version")
	if !ok {
		t.Fatal("GetMeta same_version: not found")
	}
	if ver != "test-version" {
		t.Errorf("expected same_version 'test-version', got %q", ver)
	}

	reindexTime, ok := db.GetMeta("last_reindex_time")
	if !ok {
		t.Fatal("GetMeta last_reindex_time: not found")
	}
	if reindexTime == "" {
		t.Error("expected non-empty last_reindex_time")
	}
}

func TestReindexLiteEmptyVault(t *testing.T) {
	_ = setupTestVault(t)
	// No files created

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	stats, err := ReindexLite(db, true, nil)
	if err != nil {
		t.Fatalf("ReindexLite: %v", err)
	}

	if stats.TotalFiles != 0 {
		t.Errorf("expected 0 total files, got %d", stats.TotalFiles)
	}
	if stats.NewlyIndexed != 0 {
		t.Errorf("expected 0 newly indexed, got %d", stats.NewlyIndexed)
	}
}

func TestIndexSingleFileLite_UpsertsAndSyncsGraph(t *testing.T) {
	vaultDir := setupTestVault(t)
	relPath := "notes/live.md"

	filePath := writeTestNote(t, vaultDir, relPath, `---
agent: buzz
---
We decided: use old path.
See internal/old.go for details.
`)

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if err := IndexSingleFileLite(db, filePath, relPath, vaultDir); err != nil {
		t.Fatalf("first IndexSingleFileLite: %v", err)
	}

	var count int
	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM graph_nodes WHERE type = 'file' AND name = 'internal/old.go'",
	).Scan(&count); err != nil {
		t.Fatalf("count old file node: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected old file node count 1 after first index, got %d", count)
	}

	// Update note content and ensure incremental single-file reindex replaces prior graph links.
	filePath = writeTestNote(t, vaultDir, relPath, `---
agent: buzz
---
We decided: use new path.
See internal/new.go for details.
`)
	if err := IndexSingleFileLite(db, filePath, relPath, vaultDir); err != nil {
		t.Fatalf("second IndexSingleFileLite: %v", err)
	}

	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM vault_notes WHERE path = ? AND chunk_id = 0",
		relPath,
	).Scan(&count); err != nil {
		t.Fatalf("count note roots: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one root note row after reindex, got %d", count)
	}

	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM graph_nodes WHERE type = 'file' AND name = 'internal/old.go'",
	).Scan(&count); err != nil {
		t.Fatalf("count old file node after update: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected old file node to be pruned after update, got %d", count)
	}

	if err := db.Conn().QueryRow(
		"SELECT COUNT(*) FROM graph_nodes WHERE type = 'file' AND name = 'internal/new.go'",
	).Scan(&count); err != nil {
		t.Fatalf("count new file node after update: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected new file node count 1 after update, got %d", count)
	}

	if err := db.Conn().QueryRow(`
		SELECT COUNT(*)
		FROM graph_edges e
		JOIN graph_nodes src ON src.id = e.source_id
		JOIN graph_nodes dst ON dst.id = e.target_id
		WHERE src.type = 'agent' AND src.name = 'buzz'
		  AND dst.type = 'note' AND dst.name = ?
		  AND e.relationship = 'produced'`,
		relPath,
	).Scan(&count); err != nil {
		t.Fatalf("count produced edges: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected single produced edge after reindex, got %d", count)
	}
}

func TestSaveStats(t *testing.T) {
	dir := t.TempDir()
	config.VaultOverride = dir
	defer func() { config.VaultOverride = "" }()

	// Create the data directory
	dataDir := filepath.Join(dir, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	stats := &Stats{
		TotalFiles:       10,
		NewlyIndexed:     8,
		SkippedUnchanged: 2,
		Errors:           0,
		NotesInIndex:     10,
		ChunksInIndex:    25,
		Timestamp:        "2026-01-01T00:00:00Z",
	}

	saveStats(stats)

	// Read back and verify
	data, err := os.ReadFile(filepath.Join(dataDir, "index_stats.json"))
	if err != nil {
		t.Fatalf("failed to read saved stats: %v", err)
	}

	var loaded Stats
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to parse saved stats: %v", err)
	}

	if loaded.TotalFiles != 10 {
		t.Errorf("expected TotalFiles 10, got %d", loaded.TotalFiles)
	}
	if loaded.NewlyIndexed != 8 {
		t.Errorf("expected NewlyIndexed 8, got %d", loaded.NewlyIndexed)
	}
	if loaded.Timestamp != "2026-01-01T00:00:00Z" {
		t.Errorf("expected Timestamp '2026-01-01T00:00:00Z', got %q", loaded.Timestamp)
	}
}
