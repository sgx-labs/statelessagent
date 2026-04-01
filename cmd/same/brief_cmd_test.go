package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestBuildBriefPrompt_IncludesTrustState(t *testing.T) {
	ctx := &briefContext{
		DecisionNotes: []briefNote{
			{Path: "decisions/auth.md", Title: "JWT for API auth", Text: "Use JWT tokens for auth", TrustState: "validated", Confidence: 0.9},
			{Path: "decisions/db.md", Title: "PostgreSQL with RLS", Text: "Use PostgreSQL with row level security", TrustState: "stale", Confidence: 0.85},
		},
		TrustSummary: &store.TrustSummary{
			Validated: 5,
			Stale:     2,
			Unknown:   10,
		},
		Sources: map[string][]briefSource{
			"decisions/auth.md": {
				{NotePath: "decisions/auth.md", SourcePath: "internal/auth.go", SourceType: "file"},
			},
		},
	}

	prompt := buildBriefPrompt(ctx)

	// Should mention trust states
	if !strings.Contains(prompt, "trust: validated") {
		t.Error("prompt should include 'trust: validated' for validated notes")
	}
	if !strings.Contains(prompt, "trust: stale") {
		t.Error("prompt should include 'trust: stale' for stale notes")
	}

	// Should include vault trust summary
	if !strings.Contains(prompt, "5 validated") {
		t.Error("prompt should include trust summary counts")
	}
	if !strings.Contains(prompt, "2 stale") {
		t.Error("prompt should include stale count in trust summary")
	}

	// Should include provenance
	if !strings.Contains(prompt, "internal/auth.go") {
		t.Error("prompt should include source file provenance")
	}
}

func TestBuildBriefPrompt_IncludesProvenance(t *testing.T) {
	ctx := &briefContext{
		DecisionNotes: []briefNote{
			{Path: "decisions/api.md", Title: "API design", Text: "REST API with versioning", TrustState: "validated"},
		},
		Sources: map[string][]briefSource{
			"decisions/api.md": {
				{NotePath: "decisions/api.md", SourcePath: "handler.go", SourceType: "file"},
				{NotePath: "decisions/api.md", SourcePath: "routes.go", SourceType: "file"},
			},
		},
	}

	prompt := buildBriefPrompt(ctx)

	if !strings.Contains(prompt, "handler.go") {
		t.Error("prompt should include source files from provenance")
	}
	if !strings.Contains(prompt, "routes.go") {
		t.Error("prompt should include all source files from provenance")
	}
}

func TestBuildBriefPrompt_StaleSection(t *testing.T) {
	ctx := &briefContext{
		StaleNotes: []briefNote{
			{Path: "notes/auth-audit.md", Title: "Auth audit", Text: "Security audit of auth system", TrustState: "stale"},
		},
		Sources: map[string][]briefSource{
			"notes/auth-audit.md": {
				{NotePath: "notes/auth-audit.md", SourcePath: "auth.go", SourceType: "file"},
			},
		},
	}

	prompt := buildBriefPrompt(ctx)

	if !strings.Contains(prompt, "STALE CONTEXT") {
		t.Error("prompt should include STALE CONTEXT section when stale notes exist")
	}
	if !strings.Contains(prompt, "auth.go") {
		t.Error("stale section should include source file that changed")
	}
}

func TestBuildBriefPrompt_EmptyVault(t *testing.T) {
	ctx := &briefContext{
		Sources: make(map[string][]briefSource),
	}

	prompt := buildBriefPrompt(ctx)

	if !strings.Contains(prompt, "(none)") {
		t.Error("empty sections should show (none)")
	}
	if !strings.Contains(prompt, "Produce the briefing now") {
		t.Error("prompt should end with generation instruction")
	}
}

func TestBuildBriefPrompt_OutputFormat(t *testing.T) {
	ctx := &briefContext{
		RecentNotes: []briefNote{
			{Path: "notes/test.md", Title: "Test note", Text: "Some content", TrustState: "unknown"},
		},
		Sources: make(map[string][]briefSource),
	}

	prompt := buildBriefPrompt(ctx)

	// Should include structured output instructions
	if !strings.Contains(prompt, "Current Focus") {
		t.Error("prompt should request 'Current Focus' section")
	}
	if !strings.Contains(prompt, "Key Decisions") {
		t.Error("prompt should request 'Key Decisions' section")
	}
	if !strings.Contains(prompt, "Stale Context") {
		t.Error("prompt should request 'Stale Context' section")
	}
	if !strings.Contains(prompt, "Suggestions") {
		t.Error("prompt should request 'Suggestions' section")
	}
}

func TestBriefTrustTag(t *testing.T) {
	tests := []struct {
		state    string
		contains string
		empty    bool
	}{
		{"validated", "validated", false},
		{"stale", "stale", false},
		{"contradicted", "contradicted", false},
		{"unknown", "", true},
		{"", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			tag := briefTrustTag(tc.state)
			if tc.empty && tag != "" {
				t.Errorf("expected empty tag for state %q, got %q", tc.state, tag)
			}
			if !tc.empty && !strings.Contains(tag, tc.contains) {
				t.Errorf("expected tag to contain %q for state %q, got %q", tc.contains, tc.state, tag)
			}
		})
	}
}

func TestTruncateSnippet(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short text", 100, "short text"},
		{"line1\nline2\nline3", 100, "line1 line2 line3"},
		{"a very long string that exceeds limit", 10, "a very lon"},
	}

	for _, tc := range tests {
		got := truncateSnippet(tc.input, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncateSnippet(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
		}
	}
}

func TestBriefContext_TotalGathered(t *testing.T) {
	ctx := &briefContext{
		RecentNotes:   make([]briefNote, 3),
		SessionNotes:  make([]briefNote, 2),
		DecisionNotes: make([]briefNote, 1),
		HighConfNotes: make([]briefNote, 4),
	}

	if got := ctx.totalGathered(); got != 10 {
		t.Errorf("totalGathered() = %d, want 10", got)
	}
}

func TestRenderBriefNoLLM_EmptyVault(t *testing.T) {
	ctx := &briefContext{
		TrustSummary: &store.TrustSummary{},
		Sources:      make(map[string][]briefSource),
	}

	// Should not panic on empty data
	out := captureCommandStdout(t, func() {
		_ = renderBriefNoLLM(ctx)
	})

	if !strings.Contains(out, "No recent sessions") {
		t.Error("no-llm mode should show 'No recent sessions' when empty")
	}
	if !strings.Contains(out, "No decisions recorded") {
		t.Error("no-llm mode should show 'No decisions recorded' when empty")
	}
}

func TestRenderBriefNoLLM_WithData(t *testing.T) {
	now := float64(time.Now().Unix())
	ctx := &briefContext{
		SessionNotes: []briefNote{
			{Path: "sessions/today.md", Title: "API refactoring", Modified: now, TrustState: "validated"},
		},
		DecisionNotes: []briefNote{
			{Path: "decisions/auth.md", Title: "JWT for API auth", Modified: now, TrustState: "validated", Confidence: 0.9},
		},
		HighConfNotes: []briefNote{
			{Path: "notes/arch.md", Title: "Architecture overview", Confidence: 0.95, TrustState: "validated"},
		},
		StaleNotes: []briefNote{
			{Path: "notes/old.md", Title: "Stale audit", Modified: now - 86400, TrustState: "stale"},
		},
		RecentNotes: []briefNote{
			{Path: "notes/recent.md", Title: "Recent work", Modified: now, ContentType: "note"},
		},
		TrustSummary: &store.TrustSummary{
			Validated: 3,
			Stale:     1,
		},
		Sources: map[string][]briefSource{
			"decisions/auth.md": {
				{NotePath: "decisions/auth.md", SourcePath: "internal/auth.go", SourceType: "file"},
			},
			"notes/old.md": {
				{NotePath: "notes/old.md", SourcePath: "old_source.go", SourceType: "file"},
			},
		},
	}

	out := captureCommandStdout(t, func() {
		_ = renderBriefNoLLM(ctx)
	})

	// Check sections exist
	if !strings.Contains(out, "Current Focus") {
		t.Error("expected 'Current Focus' section")
	}
	if !strings.Contains(out, "Key Decisions") {
		t.Error("expected 'Key Decisions' section")
	}
	if !strings.Contains(out, "Stale Context") {
		t.Error("expected 'Stale Context' section")
	}
	if !strings.Contains(out, "Recent Activity") {
		t.Error("expected 'Recent Activity' section")
	}

	// Check content
	if !strings.Contains(out, "API refactoring") {
		t.Error("expected session note title in output")
	}
	if !strings.Contains(out, "JWT for API auth") {
		t.Error("expected decision note title in output")
	}
	if !strings.Contains(out, "internal/auth.go") {
		t.Error("expected provenance source file in output")
	}
	if !strings.Contains(out, "validated") {
		t.Error("expected trust state tag in output")
	}
	if !strings.Contains(out, "Stale audit") {
		t.Error("expected stale note in output")
	}
	if !strings.Contains(out, "old_source.go") {
		t.Error("expected stale source file in output")
	}
}

func TestQueryBriefNotes_WithTrustState(t *testing.T) {
	_, db := setupCommandTestVault(t)
	defer db.Close()

	// Insert a note with a specific content type
	rec := store.NoteRecord{
		Path:         "test/note.md",
		Title:        "Test Note",
		Tags:         "[]",
		ChunkID:      0,
		ChunkHeading: "(full)",
		Text:         "Test content",
		Modified:     float64(time.Now().Unix()),
		ContentHash:  "test-hash",
		ContentType:  "decision",
		Confidence:   0.9,
	}
	if _, err := db.BulkInsertNotesLite([]store.NoteRecord{rec}); err != nil {
		t.Fatalf("insert note: %v", err)
	}

	// Set trust state
	if err := db.UpdateTrustState([]string{"test/note.md"}, "validated"); err != nil {
		t.Fatalf("update trust state: %v", err)
	}

	conn := db.Conn()
	notes := queryBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence, COALESCE(trust_state, 'unknown')
		 FROM vault_notes
		 WHERE chunk_id = 0 AND content_type = 'decision'
		 ORDER BY modified DESC
		 LIMIT 5`)

	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].TrustState != "validated" {
		t.Errorf("expected trust_state 'validated', got %q", notes[0].TrustState)
	}
	if notes[0].Title != "Test Note" {
		t.Errorf("expected title 'Test Note', got %q", notes[0].Title)
	}
}

func TestBriefCmd_HasNoLLMFlag(t *testing.T) {
	cmd := briefCmd()
	flag := cmd.Flags().Lookup("no-llm")
	if flag == nil {
		t.Fatal("expected --no-llm flag to exist on brief command")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected --no-llm default to be 'false', got %q", flag.DefValue)
	}
}

func TestBuildBriefPrompt_TokenSize(t *testing.T) {
	// Build a worst-case prompt with max data
	notes := make([]briefNote, 20)
	for i := range notes {
		notes[i] = briefNote{
			Path:       "notes/test.md",
			Title:      "A reasonably long title for testing purposes",
			Text:       strings.Repeat("word ", 60), // 300 chars
			TrustState: "validated",
			Confidence: 0.9,
		}
	}

	ctx := &briefContext{
		RecentNotes:   notes,
		SessionNotes:  notes[:5],
		DecisionNotes: notes[:5],
		HighConfNotes: notes[:10],
		StaleNotes:    notes[:3],
		TrustSummary: &store.TrustSummary{
			Validated: 50,
			Stale:     5,
			Unknown:   100,
		},
		Sources: make(map[string][]briefSource),
	}

	prompt := buildBriefPrompt(ctx)

	// Rough token estimate: ~4 chars per token for English text
	estimatedTokens := len(prompt) / 4
	if estimatedTokens > 8000 {
		t.Errorf("prompt estimated at ~%d tokens, should be under 8000 for reliable LLM generation. Actual chars: %d", estimatedTokens, len(prompt))
	}
}
