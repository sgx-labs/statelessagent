package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestHandleStatus_ReturnsJSON(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	insertLiteNote(t, db, "notes/status.md", "Status", "status body")
	s := &server{db: db, version: "vtest", vaultPath: "/tmp/vault"}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	s.handleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode status payload: %v", err)
	}
	if payload["version"] != "vtest" {
		t.Fatalf("expected version vtest, got %#v", payload["version"])
	}
	if payload["search_mode"] == nil {
		t.Fatalf("expected search_mode field, got payload: %+v", payload)
	}
	if payload["note_count"] == nil {
		t.Fatalf("expected note_count field, got payload: %+v", payload)
	}
}

func TestHandleRecentNotes_RespectsLimit(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	for i := 0; i < 5; i++ {
		insertLiteNote(t, db, "notes/recent-"+string(rune('a'+i))+".md", "Recent", "recent body")
	}
	s := &server{db: db}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notes/recent?limit=2", nil)
	s.handleRecentNotes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var notes []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&notes); err != nil {
		t.Fatalf("decode notes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected exactly 2 notes, got %d", len(notes))
	}
}

func TestHandleSearch_KeywordResults(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	insertLiteNote(t, db, "notes/auth.md", "Authentication", "authentication uses jwt for tokens")
	s := &server{db: db}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=authentication", nil)
	s.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode search payload: %v", err)
	}
	results, ok := payload["results"].([]any)
	if !ok {
		t.Fatalf("expected results array, got: %#v", payload["results"])
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one search result, payload: %+v", payload)
	}
}

func TestHandleSearch_EmptyQuery(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	s := &server{db: db}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=", nil)
	s.handleSearch(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
}

func TestHandlePinned_Empty(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	s := &server{db: db}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pinned", nil)
	s.handlePinned(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("expected empty JSON array, got: %q", rr.Body.String())
	}
}

func TestHandleNoteByPath_ReturnsContent(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	insertLiteNote(t, db, "test/note.md", "Test Note", "hello world")
	s := &server{db: db}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notes/test/note.md", nil)
	s.handleNoteByPath(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var note map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&note); err != nil {
		t.Fatalf("decode note payload: %v", err)
	}
	if note["title"] != "Test Note" {
		t.Fatalf("expected title Test Note, got %#v", note["title"])
	}
	if text, _ := note["text"].(string); !strings.Contains(text, "hello world") {
		t.Fatalf("expected note content in response, got %#v", note["text"])
	}
}

func TestHandleNoteByPath_NotFound(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	s := &server{db: db}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notes/nonexistent.md", nil)
	s.handleNoteByPath(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}
