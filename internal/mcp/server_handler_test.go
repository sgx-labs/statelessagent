package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// mockEmbedProvider implements embedding.Provider for tests.
type mockEmbedProvider struct {
	failNext bool
}

func (m *mockEmbedProvider) GetEmbedding(text, purpose string) ([]float32, error) {
	if m.failNext {
		return nil, errMockEmbed
	}
	return make([]float32, 768), nil
}

func (m *mockEmbedProvider) GetDocumentEmbedding(text string) ([]float32, error) {
	return m.GetEmbedding(text, "document")
}

func (m *mockEmbedProvider) GetQueryEmbedding(text string) ([]float32, error) {
	return m.GetEmbedding(text, "query")
}

func (m *mockEmbedProvider) Name() string    { return "mock" }
func (m *mockEmbedProvider) Model() string   { return "mock-embed" }
func (m *mockEmbedProvider) Dimensions() int { return 768 }

var errMockEmbed = fmt.Errorf("mock: embedding provider not ready")

// setupHandlerTest sets up a temp vault, in-memory DB, and mock embed provider.
// Returns the vault dir path and a cleanup function.
func setupHandlerTest(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	config.VaultOverride = dir
	abs, _ := filepath.Abs(dir)
	vaultRoot = abs

	writeMu.Lock()
	writeTimes = nil
	writeMu.Unlock()
	reindexMu.Lock()
	lastReindexTime = time.Time{}
	reindexMu.Unlock()

	testDB, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	db = testDB
	embedClient = &mockEmbedProvider{}

	t.Cleanup(func() {
		config.VaultOverride = ""
		db.Close()
		db = nil
		embedClient = nil
	})
	return dir
}

// resultText extracts the text from a CallToolResult.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return tc.Text
}

// --- handleGetNote ---

func TestHandleGetNote_ValidFile(t *testing.T) {
	vault := setupHandlerTest(t)

	// Create a test file
	notesDir := filepath.Join(vault, "notes")
	os.MkdirAll(notesDir, 0o755)
	os.WriteFile(filepath.Join(notesDir, "test.md"), []byte("# Test Note\nHello world"), 0o644)

	result, _, err := handleGetNote(context.Background(), nil, getInput{Path: "notes/test.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Hello world") {
		t.Errorf("expected file content, got %q", text)
	}
}

func TestHandleGetNote_FileNotFound(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleGetNote(context.Background(), nil, getInput{Path: "nonexistent.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "File not found") {
		t.Errorf("expected 'File not found', got %q", text)
	}
}

func TestHandleGetNote_InvalidPath(t *testing.T) {
	setupHandlerTest(t)

	tests := []struct {
		name string
		path string
	}{
		{"traversal", "../../../etc/passwd"},
		{"private dir", "_PRIVATE/secret.md"},
		{"dot path", ".git/config"},
		{"empty path", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := handleGetNote(context.Background(), nil, getInput{Path: tt.path})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			text := resultText(t, result)
			if !strings.Contains(text, "Error") {
				t.Errorf("expected error message for path %q, got %q", tt.path, text)
			}
		})
	}
}

// --- handleSaveNote ---

func TestHandleSaveNote_EmptyPath(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "",
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "path is required") {
		t.Errorf("expected 'path is required', got %q", text)
	}
}

func TestHandleSaveNote_EmptyContent(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "test.md",
		Content: "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "content is required") {
		t.Errorf("expected 'content is required', got %q", text)
	}
}

func TestHandleSaveNote_WhitespaceOnlyContent(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "test.md",
		Content: "   \n\t  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "content is required") {
		t.Errorf("expected 'content is required', got %q", text)
	}
}

func TestHandleSaveNote_ContentTooLarge(t *testing.T) {
	setupHandlerTest(t)

	bigContent := strings.Repeat("x", maxNoteSize+1)
	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "test.md",
		Content: bigContent,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "100KB") {
		t.Errorf("expected size limit error, got %q", text)
	}
}

func TestHandleSaveNote_NonMarkdownFile(t *testing.T) {
	setupHandlerTest(t)

	tests := []struct {
		name string
		path string
	}{
		{"txt file", "notes/file.txt"},
		{"json file", "config.json"},
		{"no extension", "README"},
		{"yaml file", "settings.yaml"},
		{"js file", "script.js"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
				Path:    tt.path,
				Content: "test content",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			text := resultText(t, result)
			if !strings.Contains(text, "only .md") {
				t.Errorf("expected '.md only' error for %q, got %q", tt.path, text)
			}
		})
	}
}

func TestHandleSaveNote_PrivatePath(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "_PRIVATE/secret.md",
		Content: "secret content",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Cannot write to _PRIVATE") {
		t.Errorf("expected private path error, got %q", text)
	}
}

func TestHandleSaveNote_InvalidPathsBlocked(t *testing.T) {
	setupHandlerTest(t)

	tests := []struct {
		name string
		path string
	}{
		{name: "traversal", path: "../../etc/passwd.md"},
		{name: "absolute", path: "/tmp/escape.md"},
		{name: "null-byte", path: "notes/bad\x00path.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
				Path:    tt.path,
				Content: "blocked",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			text := resultText(t, result)
			if !strings.Contains(text, "path must be a relative path within the vault") {
				t.Fatalf("expected vault-path error for %q, got %q", tt.path, text)
			}
		})
	}
}

func TestHandleSaveNote_SymlinkEscapeBlocked(t *testing.T) {
	vault := setupHandlerTest(t)

	notesDir := filepath.Join(vault, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(notes): %v", err)
	}
	outsideDir := t.TempDir()
	escapeLink := filepath.Join(notesDir, "escape")
	if err := os.Symlink(outsideDir, escapeLink); err != nil {
		t.Skip("symlink not supported on this platform")
	}

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/escape/pwned.md",
		Content: "should not write outside vault",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "path must be a relative path within the vault") {
		t.Fatalf("expected symlink escape error, got %q", text)
	}

	if _, err := os.Stat(filepath.Join(outsideDir, "pwned.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no file outside vault, stat err=%v", err)
	}
}

func TestHandleSaveNote_CreateNewFile(t *testing.T) {
	vault := setupHandlerTest(t)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/new-note.md",
		Content: "# New Note\nThis is new.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Saved") {
		t.Errorf("expected 'Saved', got %q", text)
	}

	// Verify file was written with provenance header
	content, err := os.ReadFile(filepath.Join(vault, "notes", "new-note.md"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if !strings.Contains(string(content), "MCP tool") {
		t.Error("expected MCP provenance header in file")
	}
	if !strings.Contains(string(content), "New Note") {
		t.Error("expected note content in file")
	}
}

func TestHandleSaveNote_AppendMode(t *testing.T) {
	vault := setupHandlerTest(t)

	// Create initial file
	os.MkdirAll(filepath.Join(vault, "notes"), 0o755)
	os.WriteFile(filepath.Join(vault, "notes", "existing.md"), []byte("# Existing\n"), 0o644)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/existing.md",
		Content: "Appended content.\n",
		Append:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Saved") {
		t.Errorf("expected 'Saved', got %q", text)
	}

	// Verify appended (no provenance header for append mode)
	content, err := os.ReadFile(filepath.Join(vault, "notes", "existing.md"))
	if err != nil {
		t.Fatalf("file read error: %v", err)
	}
	if !strings.Contains(string(content), "# Existing") {
		t.Error("expected original content preserved")
	}
	if !strings.Contains(string(content), "Appended content") {
		t.Error("expected appended content")
	}
}

func TestHandleSaveNote_CaseSensitiveMD(t *testing.T) {
	setupHandlerTest(t)

	// .MD (uppercase) should also be accepted
	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/test.MD",
		Content: "content",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Saved") {
		t.Errorf("expected .MD to be accepted, got %q", text)
	}
}

func TestHandleSaveNote_InvalidAgent(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/agent.md",
		Content: "content",
		Agent:   "codex\ninjected",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := resultText(t, result); !strings.Contains(text, "invalid agent value") {
		t.Fatalf("expected invalid agent error, got %q", text)
	}
}

func TestHandleSaveNote_WithAgentFrontmatter(t *testing.T) {
	vault := setupHandlerTest(t)

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/agent-note.md",
		Content: "# Agent Note\nOwned by codex",
		Agent:   "codex",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := resultText(t, result); !strings.Contains(text, "Saved") {
		t.Fatalf("expected save confirmation, got %q", text)
	}

	raw, err := os.ReadFile(filepath.Join(vault, "notes", "agent-note.md"))
	if err != nil {
		t.Fatalf("read saved note: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, `agent: "codex"`) {
		t.Fatalf("expected agent frontmatter, got:\n%s", content)
	}
	if !strings.Contains(content, "MCP tool") {
		t.Fatalf("expected provenance header, got:\n%s", content)
	}

	records, err := db.GetNoteByPath("notes/agent-note.md")
	if err != nil || len(records) == 0 {
		t.Fatalf("expected indexed note, err=%v len=%d", err, len(records))
	}
	if records[0].Agent != "codex" {
		t.Fatalf("expected indexed agent to be codex, got %q", records[0].Agent)
	}
}

func TestHandleSaveNote_ReadClaimWarning(t *testing.T) {
	setupHandlerTest(t)

	if err := db.UpsertClaim("notes/shared.md", "claude", store.ClaimTypeRead, 30*time.Minute); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}

	result, _, err := handleSaveNote(context.Background(), nil, saveNoteInput{
		Path:    "notes/shared.md",
		Content: "# Shared\nupdated",
		Agent:   "codex",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "read-claims by claude") {
		t.Fatalf("expected read-claim warning, got %q", text)
	}
}

// --- handleSaveDecision ---

func TestHandleSaveDecision_EmptyTitle(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
		Title: "",
		Body:  "details",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "title is required") {
		t.Errorf("expected 'title is required', got %q", text)
	}
}

func TestHandleSaveDecision_EmptyBody(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
		Title: "My Decision",
		Body:  "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "body is required") {
		t.Errorf("expected 'body is required', got %q", text)
	}
}

func TestHandleSaveDecision_ContentTooLarge(t *testing.T) {
	setupHandlerTest(t)

	bigBody := strings.Repeat("x", maxNoteSize)
	result, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
		Title: "Decision",
		Body:  bigBody,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "too large") {
		t.Errorf("expected size limit error, got %q", text)
	}
}

func TestHandleSaveDecision_InvalidStatus(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
		Title:  "Test Decision",
		Body:   "Body text",
		Status: "invalid",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "must be") {
		t.Errorf("expected status validation error, got %q", text)
	}
}

func TestHandleSaveDecision_InvalidAgent(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
		Title: "Decision",
		Body:  "Body",
		Agent: "bad\ragent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := resultText(t, result); !strings.Contains(text, "invalid agent value") {
		t.Fatalf("expected invalid agent error, got %q", text)
	}
}

func TestHandleSaveDecision_ValidStatuses(t *testing.T) {
	tests := []string{"accepted", "proposed", "superseded"}
	for _, status := range tests {
		t.Run(status, func(t *testing.T) {
			vault := setupHandlerTest(t)

			// Create the decisions file directory
			os.MkdirAll(vault, 0o755)

			result, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
				Title:  "Test Decision",
				Body:   "Body text for test",
				Status: status,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			text := resultText(t, result)
			if !strings.Contains(text, "Decision logged") {
				t.Errorf("expected 'Decision logged' for status %q, got %q", status, text)
			}
			if !strings.Contains(text, status) {
				t.Errorf("expected status %q in response, got %q", status, text)
			}
		})
	}
}

func TestHandleSaveDecision_DefaultStatus(t *testing.T) {
	vault := setupHandlerTest(t)
	os.MkdirAll(vault, 0o755)

	result, _, err := handleSaveDecision(context.Background(), nil, saveDecisionInput{
		Title: "Test Decision",
		Body:  "Body text",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "accepted") {
		t.Errorf("expected default status 'accepted', got %q", text)
	}
}

// --- handleCreateHandoff ---

func TestHandleCreateHandoff_EmptySummary(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary: "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "summary is required") {
		t.Errorf("expected 'summary is required', got %q", text)
	}
}

func TestHandleCreateHandoff_WhitespaceOnlySummary(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary: "   \n\t  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "summary is required") {
		t.Errorf("expected 'summary is required', got %q", text)
	}
}

func TestHandleCreateHandoff_ContentTooLarge(t *testing.T) {
	setupHandlerTest(t)

	bigSummary := strings.Repeat("x", maxNoteSize+1)
	result, _, err := handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary: bigSummary,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "too large") {
		t.Errorf("expected size limit error, got %q", text)
	}
}

func TestHandleCreateHandoff_InvalidAgent(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary: "valid summary",
		Agent:   "bad\000agent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := resultText(t, result); !strings.Contains(text, "invalid agent value") {
		t.Fatalf("expected invalid agent error, got %q", text)
	}
}

func TestHandleCreateHandoff_Success(t *testing.T) {
	vault := setupHandlerTest(t)
	os.MkdirAll(vault, 0o755)

	result, _, err := handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary:  "Implemented feature X",
		Pending:  "Need to add tests",
		Blockers: "Waiting on API spec",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Handoff saved") {
		t.Errorf("expected 'Handoff saved', got %q", text)
	}

	// Verify the handoff file was created
	today := time.Now().Format("2006-01-02") + "-" + time.Now().Format("150405")
	handoffPath := filepath.Join(vault, "sessions", today+"-handoff.md")
	content, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("handoff file not created: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "Implemented feature X") {
		t.Error("expected summary in handoff")
	}
	if !strings.Contains(contentStr, "Need to add tests") {
		t.Error("expected pending in handoff")
	}
	if !strings.Contains(contentStr, "Waiting on API spec") {
		t.Error("expected blockers in handoff")
	}
}

func TestHandleCreateHandoff_OptionalFields(t *testing.T) {
	vault := setupHandlerTest(t)
	os.MkdirAll(vault, 0o755)

	result, _, err := handleCreateHandoff(context.Background(), nil, createHandoffInput{
		Summary: "Just a summary",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Handoff saved") {
		t.Errorf("expected 'Handoff saved', got %q", text)
	}

	today := time.Now().Format("2006-01-02") + "-" + time.Now().Format("150405")
	handoffPath := filepath.Join(vault, "sessions", today+"-handoff.md")
	content, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("handoff file not created: %v", err)
	}
	contentStr := string(content)
	if strings.Contains(contentStr, "Pending") {
		t.Error("should not have Pending section when empty")
	}
	if strings.Contains(contentStr, "Blockers") {
		t.Error("should not have Blockers section when empty")
	}
}

// --- handleSearchNotes ---

func TestHandleSearchNotes_EmbedError(t *testing.T) {
	setupHandlerTest(t)
	embedClient = &mockEmbedProvider{failNext: true}

	result, _, err := handleSearchNotes(context.Background(), nil, searchInput{
		Query: "test query",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	// With graceful fallback, embedding failure falls through to keyword search
	// which returns "No results" on an empty index (not an embedding error)
	if !strings.Contains(text, "No results") {
		t.Errorf("expected 'No results' fallback, got %q", text)
	}
}

func TestHandleSearchNotes_NilEmbedClient(t *testing.T) {
	setupHandlerTest(t)
	embedClient = nil

	result, _, err := handleSearchNotes(context.Background(), nil, searchInput{
		Query: "test query",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No results") {
		t.Errorf("expected 'No results' fallback with nil embedClient, got %q", text)
	}
}

func TestHandleSearchNotes_EmptyIndex(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSearchNotes(context.Background(), nil, searchInput{
		Query: "test query",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No results") {
		t.Errorf("expected 'No results' for empty index, got %q", text)
	}
}

// --- handleSearchNotesFiltered ---

func TestHandleSearchNotesFiltered_EmbedError(t *testing.T) {
	setupHandlerTest(t)
	embedClient = &mockEmbedProvider{failNext: true}

	result, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query: "test query",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	// With graceful fallback, embedding failure falls through to keyword search
	if !strings.Contains(text, "No results") {
		t.Errorf("expected 'No results' fallback, got %q", text)
	}
}

func TestHandleSearchNotesFiltered_EmptyIndex(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query:  "test query",
		TopK:   5,
		Domain: "engineering",
		Tags:   "go, testing",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No results") {
		t.Errorf("expected 'No results' for empty index, got %q", text)
	}
}

// --- handleFindSimilar ---

func TestHandleFindSimilar_InvalidPath(t *testing.T) {
	setupHandlerTest(t)

	tests := []struct {
		name string
		path string
	}{
		{"traversal", "../../../etc/passwd"},
		{"private", "_PRIVATE/secret.md"},
		{"dot path", ".git/config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := handleFindSimilar(context.Background(), nil, similarInput{
				Path: tt.path,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			text := resultText(t, result)
			if !strings.Contains(text, "invalid note path") {
				t.Errorf("expected 'invalid note path' for %q, got %q", tt.path, text)
			}
		})
	}
}

func TestHandleFindSimilar_NoteNotInIndex(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleFindSimilar(context.Background(), nil, similarInput{
		Path: "nonexistent.md",
		TopK: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No similar notes found") && !strings.Contains(text, "requires semantic search") {
		t.Errorf("expected 'No similar notes found' or 'requires semantic search', got %q", text)
	}
}

// --- handleReindex ---

func TestHandleReindex_Cooldown(t *testing.T) {
	setupHandlerTest(t)

	// Set lastReindexTime to now to trigger cooldown
	reindexMu.Lock()
	lastReindexTime = time.Now()
	reindexMu.Unlock()

	result, _, err := handleReindex(context.Background(), nil, reindexInput{Force: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "cooldown") {
		t.Errorf("expected cooldown message, got %q", text)
	}

	// Reset for other tests
	reindexMu.Lock()
	lastReindexTime = time.Time{}
	reindexMu.Unlock()
}

// --- handleRecentActivity ---

func TestHandleRecentActivity_EmptyDB(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleRecentActivity(context.Background(), nil, recentInput{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No notes found") {
		t.Errorf("expected 'No notes found', got %q", text)
	}
}

func TestHandleRecentActivity_WithNotes(t *testing.T) {
	setupHandlerTest(t)

	// Insert test notes into the in-memory DB
	vec := make([]float32, 768)
	db.InsertNote(&store.NoteRecord{
		Path:     "notes/first.md",
		Title:    "First Note",
		Text:     "First note content",
		Modified: float64(time.Now().Unix()),
	}, vec)
	db.InsertNote(&store.NoteRecord{
		Path:     "notes/second.md",
		Title:    "Second Note",
		Text:     "Second note content",
		Modified: float64(time.Now().Unix() - 60),
	}, vec)

	result, _, err := handleRecentActivity(context.Background(), nil, recentInput{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "first.md") {
		t.Errorf("expected 'first.md' in results, got %q", text)
	}
	if !strings.Contains(text, "second.md") {
		t.Errorf("expected 'second.md' in results, got %q", text)
	}
}

func TestHandleRecentActivity_DefaultLimit(t *testing.T) {
	setupHandlerTest(t)

	// limit=0 should default to 10
	result, _, err := handleRecentActivity(context.Background(), nil, recentInput{Limit: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No notes in DB, so should get "No notes found"
	text := resultText(t, result)
	if !strings.Contains(text, "No notes found") {
		t.Errorf("expected 'No notes found', got %q", text)
	}
}

func TestHandleRecentActivity_NegativeLimit(t *testing.T) {
	setupHandlerTest(t)

	// Negative should default to 10
	result, _, err := handleRecentActivity(context.Background(), nil, recentInput{Limit: -5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No notes found") {
		t.Errorf("expected 'No notes found', got %q", text)
	}
}

func TestHandleRecentActivity_MaxLimit(t *testing.T) {
	setupHandlerTest(t)

	// limit=999 should be capped to 50
	result, _, err := handleRecentActivity(context.Background(), nil, recentInput{Limit: 999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No notes, but handler should not error on capped limit
	text := resultText(t, result)
	if !strings.Contains(text, "No notes found") {
		t.Errorf("expected 'No notes found', got %q", text)
	}
}

// --- handleIndexStats ---

func TestHandleIndexStats_EmptyDB(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleIndexStats(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	// Should return some JSON with stats
	if text == "" {
		t.Error("expected non-empty stats response")
	}
}

// --- handleGetSessionContext ---

func TestHandleGetSessionContext_EmptyDB(t *testing.T) {
	setupHandlerTest(t)

	result, _, err := handleGetSessionContext(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if text == "" {
		t.Error("expected non-empty session context")
	}
	// Should contain stats at minimum
	if !strings.Contains(text, "stats") {
		t.Errorf("expected 'stats' in session context, got %q", text)
	}
}

func TestHandleGetSessionContext_WithPinnedNotes(t *testing.T) {
	setupHandlerTest(t)

	// Insert a note and pin it
	vec := make([]float32, 768)
	db.InsertNote(&store.NoteRecord{
		Path:     "notes/pinned.md",
		Title:    "Pinned Note",
		Text:     "This note is pinned for context",
		Modified: float64(time.Now().Unix()),
	}, vec)
	db.PinNote("notes/pinned.md")

	result, _, err := handleGetSessionContext(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "pinned_notes") {
		t.Errorf("expected 'pinned_notes' in context, got %q", text)
	}
	if !strings.Contains(text, "pinned.md") {
		t.Errorf("expected 'pinned.md' in pinned notes, got %q", text)
	}
}

func TestHandleGetSessionContext_WithRecentNotes(t *testing.T) {
	setupHandlerTest(t)

	vec := make([]float32, 768)
	db.InsertNote(&store.NoteRecord{
		Path:     "notes/recent.md",
		Title:    "Recent Note",
		Text:     "Recently modified",
		Modified: float64(time.Now().Unix()),
	}, vec)

	result, _, err := handleGetSessionContext(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "recent_notes") {
		t.Errorf("expected 'recent_notes' in context, got %q", text)
	}
	if !strings.Contains(text, "recent.md") {
		t.Errorf("expected 'recent.md' in recent notes, got %q", text)
	}
}

func TestHandleGetSessionContext_WithActiveClaims(t *testing.T) {
	setupHandlerTest(t)

	if err := db.UpsertClaim("cmd/same/main.go", "codex", store.ClaimTypeWrite, 30*time.Minute); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}

	result, _, err := handleGetSessionContext(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(resultText(t, result)), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	rawClaims, ok := payload["active_claims"].([]any)
	if !ok || len(rawClaims) != 1 {
		t.Fatalf("expected one active claim in context, got %#v", payload["active_claims"])
	}
}

func TestHandleSearchNotesFiltered_AgentFilter(t *testing.T) {
	setupHandlerTest(t)
	embedClient = nil // force keyword fallback path

	vec := make([]float32, 768)
	if err := db.InsertNote(&store.NoteRecord{
		Path:         "notes/auth.md",
		Title:        "Auth Note",
		Agent:        "codex",
		Text:         "authentication strategy and tokens",
		ChunkID:      0,
		ChunkHeading: "(full)",
		Modified:     float64(time.Now().Unix()),
		ContentHash:  "hash-auth",
		ContentType:  "note",
		Confidence:   0.6,
	}, vec); err != nil {
		t.Fatalf("InsertNote: %v", err)
	}

	result, _, err := handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query: "authentication strategy",
		Agent: "claude",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := resultText(t, result); !strings.Contains(text, "No results") {
		t.Fatalf("expected no results for mismatched agent filter, got %q", text)
	}

	result, _, err = handleSearchNotesFiltered(context.Background(), nil, searchFilteredInput{
		Query: "authentication strategy",
		Agent: "codex",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := resultText(t, result); !strings.Contains(text, "notes/auth.md") {
		t.Fatalf("expected codex-authored note, got %q", text)
	}
}

// --- registerTools ---

func TestRegisterTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "same-test",
		Version: "test",
	}, nil)

	// Should not panic
	registerTools(server)
}

// --- reindexCooldown constant ---

func TestReindexCooldown(t *testing.T) {
	if reindexCooldown != 60*time.Second {
		t.Errorf("expected reindexCooldown to be 60s, got %v", reindexCooldown)
	}
}
