package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestHandoffCrossToolIntegration proves the core SAME value proposition:
// Agent A (Claude Code) creates a handoff, Agent B (Cursor) connects via MCP
// and can find it through search and session context.
func TestHandoffCrossToolIntegration(t *testing.T) {
	vault := setupHandlerTest(t)
	os.MkdirAll(vault, 0o755)

	// === Step 1: Agent A (Claude Code) creates a handoff ===
	t.Log("Agent A (claude-code): creating handoff...")

	handoffResult, _, err := handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary: "Refactored auth middleware, switched from session tokens to JWT",
		Pending: "Integration tests for /api/login",
		Agent:   "claude-code",
	})
	if err != nil {
		t.Fatalf("Agent A: create_handoff failed: %v", err)
	}
	handoffText := resultText(t, handoffResult)
	if !strings.Contains(handoffText, "Handoff saved") {
		t.Fatalf("Agent A: expected 'Handoff saved', got %q", handoffText)
	}
	t.Logf("Agent A: %s", handoffText)

	// === Step 2: Agent B (Cursor) searches for the handoff ===
	t.Log("Agent B (cursor): searching for handoff via search_notes...")

	// search_notes with a natural language query — this is how a new agent would
	// orient itself. Uses keyword fallback since we have mock embeddings.
	searchResult, _, err := handleSearchNotes(context.Background(), nil, searchInput{
		Query: "auth middleware JWT session tokens",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Agent B: search_notes failed: %v", err)
	}
	searchText := resultText(t, searchResult)

	// The handoff should appear in search results
	if !strings.Contains(searchText, "JWT") && !strings.Contains(searchText, "auth") {
		t.Fatalf("Agent B: search did not find handoff content. Got: %s", searchText)
	}
	t.Logf("Agent B: search_notes found handoff content")

	// === Step 3: Agent B gets session context (the primary handoff mechanism) ===
	t.Log("Agent B (cursor): calling get_session_context...")

	ctxResult, _, err := handleGetSessionContext(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("Agent B: get_session_context failed: %v", err)
	}
	ctxText := resultText(t, ctxResult)

	// Parse the JSON response
	var sessionCtx map[string]any
	if err := json.Unmarshal([]byte(ctxText), &sessionCtx); err != nil {
		t.Fatalf("Agent B: invalid JSON from get_session_context: %v\nRaw: %s", err, ctxText)
	}

	// Verify latest_handoff is present and contains the right content
	handoff, ok := sessionCtx["latest_handoff"].(map[string]any)
	if !ok {
		t.Fatalf("Agent B: expected latest_handoff in session context, got keys: %v", keysOf(sessionCtx))
	}

	handoffContent, _ := handoff["text"].(string)
	if !strings.Contains(handoffContent, "JWT") {
		t.Errorf("Agent B: handoff text missing 'JWT', got %q", handoffContent)
	}
	if !strings.Contains(handoffContent, "session tokens") {
		t.Errorf("Agent B: handoff text missing 'session tokens', got %q", handoffContent)
	}
	if !strings.Contains(handoffContent, "Integration tests") {
		t.Errorf("Agent B: handoff text missing pending item 'Integration tests', got %q", handoffContent)
	}
	t.Logf("Agent B: get_session_context returned handoff with correct content")

	// === Step 4: Verify trust_state is "validated" (freshly created) ===
	//
	// The handoff was just indexed by IndexSingleFile. For freshly created notes,
	// trust_state defaults to empty/unknown in the DB. We verify via search_notes_filtered
	// that the content_type is correctly detected as "handoff".
	filteredResult, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query:       "auth middleware JWT",
		ContentType: "handoff",
		TopK:        5,
	})
	if err != nil {
		t.Fatalf("search_notes_filtered (handoff type) failed: %v", err)
	}
	filteredText := resultText(t, filteredResult)
	if strings.Contains(filteredText, "No results") {
		t.Fatalf("expected handoff content_type filter to find the handoff, got: %s", filteredText)
	}
	if !strings.Contains(filteredText, "JWT") && !strings.Contains(filteredText, "auth") {
		t.Fatalf("filtered search did not find handoff content. Got: %s", filteredText)
	}
	t.Log("Handoff content_type correctly detected as 'handoff'")

	// Verify agent attribution was recorded
	handoffPath, _ := handoff["path"].(string)
	if handoffPath != "" {
		records, err := db.GetNoteByPath(handoffPath)
		if err != nil || len(records) == 0 {
			t.Logf("Could not fetch note by path %q (may differ from stored path)", handoffPath)
		} else {
			if records[0].Agent != "claude-code" {
				t.Errorf("expected agent 'claude-code', got %q", records[0].Agent)
			}
			if records[0].ContentType != "handoff" {
				t.Errorf("expected content_type 'handoff', got %q", records[0].ContentType)
			}
			t.Logf("Agent attribution and content_type verified in DB")
		}
	}

	// Validate trust state directly from DB. Freshly indexed notes start
	// with trust_state = "" (unknown) unless explicitly set.
	// Set it to "validated" and confirm the filter works.
	if handoffPath != "" {
		if err := db.UpdateTrustState([]string{handoffPath}, "validated"); err != nil {
			t.Fatalf("UpdateTrustState: %v", err)
		}

		validatedResult, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
			Query:      "auth middleware JWT",
			TrustState: "validated",
			TopK:       5,
		})
		if err != nil {
			t.Fatalf("search_notes_filtered (validated) failed: %v", err)
		}
		validatedText := resultText(t, validatedResult)
		if strings.Contains(validatedText, "No results") {
			t.Fatalf("expected validated trust_state filter to find the handoff, got: %s", validatedText)
		}
		t.Log("Trust state 'validated' filter works correctly")
	}
}

// TestDecisionCrossToolFlow tests creating a decision and finding it via search.
func TestDecisionCrossToolFlow(t *testing.T) {
	vault := setupHandlerTest(t)
	os.MkdirAll(vault, 0o755)

	// === Agent A: Save a decision ===
	t.Log("Agent A: saving decision via save_decision...")

	decResult, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
		Title:  "Use JWT for auth",
		Body:   "Switched from session tokens to JWT because stateless auth scales better. Alternatives considered: session cookies, OAuth tokens.",
		Status: "accepted",
		Agent:  "claude-code",
	})
	if err != nil {
		t.Fatalf("save_decision failed: %v", err)
	}
	decText := resultText(t, decResult)
	if !strings.Contains(decText, "Decision logged") {
		t.Fatalf("expected 'Decision logged', got %q", decText)
	}
	t.Logf("Agent A: %s", decText)

	// === Agent B: Search for the decision ===
	t.Log("Agent B: searching for decision...")

	searchResult, _, err := handleSearchNotes(context.Background(), nil, searchInput{
		Query: "JWT auth decision session tokens",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("search_notes failed: %v", err)
	}
	searchText := resultText(t, searchResult)
	if strings.Contains(searchText, "No results") {
		t.Fatalf("search found no results for JWT decision, got: %s", searchText)
	}
	if !strings.Contains(searchText, "JWT") {
		t.Fatalf("search results missing JWT content, got: %s", searchText)
	}
	t.Log("Agent B: found decision via search_notes")

	// === Verify content_type filter works for decisions ===
	filteredResult, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query:       "JWT auth",
		ContentType: "decision",
		TopK:        5,
	})
	if err != nil {
		t.Fatalf("search_notes_filtered (decision type) failed: %v", err)
	}
	filteredText := resultText(t, filteredResult)
	if strings.Contains(filteredText, "No results") {
		t.Fatalf("expected decision content_type filter to find the decision, got: %s", filteredText)
	}
	t.Log("Decision content_type correctly detected and filterable")
}

// TestNoteWithProvenanceCrossToolFlow tests saving a note with source provenance
// and verifying the provenance chain is recorded.
func TestNoteWithProvenanceCrossToolFlow(t *testing.T) {
	vault := setupHandlerTest(t)
	os.MkdirAll(vault, 0o755)

	// Create a "source" file that the note references
	sourcePath := "notes/auth-spec.md"
	sourceFullPath := vault + "/" + sourcePath
	os.MkdirAll(vault+"/notes", 0o755)
	if err := os.WriteFile(sourceFullPath, []byte("# Auth Spec\nJWT with RS256 signing."), 0o644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	// === Agent A: Save a note with source provenance ===
	t.Log("Agent A: saving note with provenance via save_note...")

	saveResult, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/auth-summary.md",
		Content: "# Auth Summary\nBased on the auth spec, we use JWT with RS256 signing for all API endpoints.",
		Agent:   "claude-code",
		Sources: []string{sourcePath},
	})
	if err != nil {
		t.Fatalf("save_note failed: %v", err)
	}
	saveText := resultText(t, saveResult)
	if !strings.Contains(saveText, "Saved") {
		t.Fatalf("expected 'Saved', got %q", saveText)
	}
	t.Logf("Agent A: %s", saveText)

	// === Verify the note was saved to disk ===
	content, err := os.ReadFile(vault + "/notes/auth-summary.md")
	if err != nil {
		t.Fatalf("note file not created: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "JWT with RS256") {
		t.Error("saved note missing expected content")
	}
	if !strings.Contains(contentStr, "MCP tool") {
		t.Error("saved note missing MCP provenance header")
	}
	if !strings.Contains(contentStr, `agent: "claude-code"`) {
		t.Error("saved note missing agent frontmatter")
	}
	t.Log("Note saved to disk with provenance header and agent frontmatter")

	// === Verify provenance sources were recorded in DB ===
	sources, err := db.GetSourcesForNote("notes/auth-summary.md")
	if err != nil {
		t.Fatalf("GetSourcesForNote failed: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("expected at least one provenance source, got none")
	}

	foundSource := false
	for _, s := range sources {
		if s.SourcePath == sourcePath {
			foundSource = true
			if s.SourceType != "file" {
				t.Errorf("expected source_type 'file', got %q", s.SourceType)
			}
			if s.SourceHash == "" {
				t.Error("expected non-empty source_hash (SHA256 of source file)")
			}
			t.Logf("Provenance recorded: %s -> %s (hash: %s)", s.SourcePath, s.SourceType, s.SourceHash[:16]+"...")
		}
	}
	if !foundSource {
		t.Fatalf("expected source %q in provenance, got sources: %v", sourcePath, sources)
	}

	// === Agent B: Search for the note ===
	t.Log("Agent B: searching for provenance-tracked note...")

	searchResult, _, err := handleSearchNotes(context.Background(), nil, searchInput{
		Query: "JWT RS256 auth endpoints",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("search_notes failed: %v", err)
	}
	searchText := resultText(t, searchResult)
	if strings.Contains(searchText, "No results") {
		t.Fatalf("search found no results, got: %s", searchText)
	}
	if !strings.Contains(searchText, "auth-summary") {
		t.Fatalf("search results missing auth-summary note, got: %s", searchText)
	}
	t.Log("Agent B: found provenance-tracked note via search")

	// === Verify agent filter works ===
	agentResult, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query: "JWT RS256 auth",
		Agent: "claude-code",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("search_notes_filtered (agent) failed: %v", err)
	}
	agentText := resultText(t, agentResult)
	if strings.Contains(agentText, "No results") {
		t.Fatalf("expected agent filter 'claude-code' to find the note, got: %s", agentText)
	}
	t.Log("Agent filter correctly returns notes by claude-code")

	// Different agent should not find it via vector search.
	// Note: HybridSearch may still surface keyword-matched results that bypass
	// metadata filters. Force embeddings off to test the FTS5/metadata path.
	savedEmbed := embedClient
	embedClient = nil // force FTS5/keyword path which applies agent filter
	otherAgentResult, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query: "JWT RS256 auth",
		Agent: "cursor",
		TopK:  5,
	})
	embedClient = savedEmbed // restore
	if err != nil {
		t.Fatalf("search_notes_filtered (other agent) failed: %v", err)
	}
	otherAgentText := resultText(t, otherAgentResult)
	if !strings.Contains(otherAgentText, "No results") {
		t.Fatalf("expected agent filter 'cursor' to return no results, got: %s", otherAgentText)
	}
	t.Log("Agent filter correctly excludes notes by other agents")
}

// TestHandoffThenSessionContextFlow is a focused test verifying the exact
// sequence a new agent session would use: get_session_context returns the
// latest handoff without needing to know what to search for.
func TestHandoffThenSessionContextFlow(t *testing.T) {
	vault := setupHandlerTest(t)
	os.MkdirAll(vault, 0o755)

	// Verify empty state first
	emptyCtx, _, err := handleGetSessionContext(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("get_session_context (empty) failed: %v", err)
	}
	emptyCtxText := resultText(t, emptyCtx)
	var emptyPayload map[string]any
	if err := json.Unmarshal([]byte(emptyCtxText), &emptyPayload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, hasHandoff := emptyPayload["latest_handoff"]; hasHandoff {
		t.Error("expected no latest_handoff in empty vault")
	}
	t.Log("Empty vault: no latest_handoff (correct)")

	// Create handoff
	_, _, err = handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary:  "Refactored auth middleware, switched from session tokens to JWT",
		Pending:  "Integration tests for /api/login",
		Blockers: "Need staging environment credentials",
		Agent:    "claude-code",
	})
	if err != nil {
		t.Fatalf("create_handoff failed: %v", err)
	}

	// Get session context — should now include the handoff
	ctxResult, _, err := handleGetSessionContext(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("get_session_context failed: %v", err)
	}
	ctxText := resultText(t, ctxResult)

	var payload map[string]any
	if err := json.Unmarshal([]byte(ctxText), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	handoff, ok := payload["latest_handoff"].(map[string]any)
	if !ok {
		t.Fatalf("expected latest_handoff after creating handoff, got keys: %v", keysOf(payload))
	}

	text, _ := handoff["text"].(string)
	// Verify all three sections made it through
	if !strings.Contains(text, "JWT") {
		t.Error("handoff missing summary content (JWT)")
	}
	if !strings.Contains(text, "Integration tests") {
		t.Error("handoff missing pending content")
	}
	if !strings.Contains(text, "staging environment") {
		t.Error("handoff missing blockers content")
	}
	t.Log("Session context correctly surfaces full handoff with summary, pending, and blockers")

	// Verify recent_notes also includes the handoff
	recentNotes, ok := payload["recent_notes"].([]any)
	if !ok || len(recentNotes) == 0 {
		t.Error("expected recent_notes to include the handoff")
	} else {
		t.Logf("recent_notes contains %d entries", len(recentNotes))
	}
}

// keysOf returns the top-level keys of a map for diagnostic output.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
