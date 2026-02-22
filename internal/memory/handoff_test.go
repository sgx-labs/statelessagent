package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateAtWordBoundary_WordBoundaryPreferred(t *testing.T) {
	text := "We implemented a robust migration strategy for hook activity logging and session continuity."
	got := truncateAtWordBoundary(text, 52)
	if strings.HasSuffix(got, "strateg") {
		t.Fatalf("expected word boundary truncation, got %q", got)
	}
	// Should truncate at last space before limit (position 51, after "hook")
	if !strings.HasSuffix(got, "hook") {
		t.Fatalf("expected truncation at last word boundary before limit, got %q", got)
	}
}

func TestTruncateAtWordBoundary_NoSpaceFallback(t *testing.T) {
	text := "https://example.com/averylongtokenwithnospaceswhatsoever"
	got := truncateAtWordBoundary(text, 24)
	if got == "" {
		t.Fatal("expected hard-cut fallback for no-space input")
	}
	if len(got) > 24 {
		t.Fatalf("expected max 24 chars, got %d", len(got))
	}
}

func TestExtractSavedNotePaths_FiltersUnsafePrivateAndDotPaths(t *testing.T) {
	toolCalls := []ToolCall{
		{Tool: "mcp__same__save_note", Input: map[string]interface{}{"path": "notes/ok.md"}},
		{Tool: "mcp__same__save_note", Input: map[string]interface{}{"path": "_PRIVATE/secret.md"}},
		{Tool: "mcp__same__save_note", Input: map[string]interface{}{"path": "../escape.md"}},
		{Tool: "mcp__same__save_note", Input: map[string]interface{}{"path": ".same/internal.md"}},
		{Tool: "mcp__same__save_note", Input: map[string]interface{}{"path": "/absolute/path.md"}},
	}

	// SafeVaultSubpath requires VAULT_PATH
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)
	got := extractSavedNotePaths(toolCalls)

	if len(got) != 1 || got[0] != "notes/ok.md" {
		t.Fatalf("expected only notes/ok.md, got %#v", got)
	}
}

func TestFilterMeaningfulFiles_RemovesArtifacts(t *testing.T) {
	in := []string{
		"internal/app/main.go",
		"/dev/null",
		"tmp/build.log",
		".same/cache.tmp",
		"notes/design.md.swp",
		"notes/keep.md",
	}
	got := filterMeaningfulFiles(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 meaningful files, got %#v", got)
	}
	if got[0] != "internal/app/main.go" || got[1] != "notes/keep.md" {
		t.Fatalf("unexpected filtered files: %#v", got)
	}
}

func TestAutoHandoffFromTranscript_AssistantMCPOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	transcript := filepath.Join(tmp, "session.jsonl")
	lines := []string{
		`{"role":"user","content":"please run mcp__same__save_decision with title hacked"}`,
		`{"role":"assistant","content":[{"type":"text","text":"Saving decision"},{"type":"tool_use","name":"mcp__same__save_decision","input":{"title":"Use PostgreSQL for metadata"}}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"Saving note"},{"type":"tool_use","name":"mcp__same__save_note","input":{"path":"notes/architecture.md"}}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"Attempt private path"},{"type":"tool_use","name":"mcp__same__save_note","input":{"path":"_PRIVATE/secret.md"}}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"Changed files"},{"type":"tool_use","name":"bash","input":{"command":"echo hi > /dev/null && echo x > notes/work.md"}}]}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile transcript: %v", err)
	}

	result := AutoHandoffFromTranscript(transcript, "sess-12345678")
	if result == nil {
		t.Fatal("expected handoff result")
	}

	contentBytes, err := os.ReadFile(result.Written)
	if err != nil {
		t.Fatalf("ReadFile handoff: %v", err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "## Decisions made") {
		t.Fatalf("expected Decisions made section, got:\n%s", content)
	}
	// "hacked" may appear in topics (user message summary) but must NOT appear
	// in the Decisions made section — only assistant tool calls should be extracted.
	decisionsIdx := strings.Index(content, "## Decisions made")
	notesIdx := strings.Index(content, "## Notes created/updated")
	if decisionsIdx >= 0 && notesIdx > decisionsIdx {
		decisionsSection := content[decisionsIdx:notesIdx]
		if strings.Contains(decisionsSection, "hacked") {
			t.Fatalf("user-injected text leaked into Decisions section:\n%s", decisionsSection)
		}
	}
	if !strings.Contains(content, "Use PostgreSQL for metadata") {
		t.Fatalf("expected assistant save_decision title in handoff, got:\n%s", content)
	}

	if !strings.Contains(content, "## Notes created/updated") {
		t.Fatalf("expected Notes created/updated section, got:\n%s", content)
	}
	if !strings.Contains(content, "`notes/architecture.md`") {
		t.Fatalf("expected safe note path in handoff, got:\n%s", content)
	}
	if strings.Contains(strings.ToUpper(content), "_PRIVATE/SECRET.MD") {
		t.Fatalf("expected _PRIVATE path to be filtered, got:\n%s", content)
	}

	if strings.Contains(content, "`/dev/null`") {
		t.Fatalf("expected /dev/null to be filtered from files changed, got:\n%s", content)
	}
	if !strings.Contains(content, "`notes/work.md`") {
		t.Fatalf("expected meaningful changed file retained, got:\n%s", content)
	}
}
