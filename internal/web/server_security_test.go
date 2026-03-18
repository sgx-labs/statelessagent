package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func insertLiteNote(t *testing.T, db *store.DB, path, title, text string) {
	t.Helper()
	rec := store.NoteRecord{
		Path:         path,
		Title:        title,
		Tags:         "[]",
		Domain:       "",
		Workstream:   "",
		ChunkID:      0,
		ChunkHeading: "(full)",
		Text:         text,
		Modified:     1700000000,
		ContentHash:  path + "-hash",
		ContentType:  "note",
		Confidence:   0.5,
	}
	if _, err := db.BulkInsertNotesLite([]store.NoteRecord{rec}); err != nil {
		t.Fatalf("insert note %s: %v", path, err)
	}
}

func TestLocalhostOnly_AllowsLoopbackHosts(t *testing.T) {
	h := localhostOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	hosts := []string{"localhost:4078", "127.0.0.1:4078", "[::1]:4078"}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
			req.Host = host
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("expected loopback host %q to pass, got status %d", host, rr.Code)
			}
		})
	}
}

func TestLocalhostOnly_RejectsNonLoopbackHost(t *testing.T) {
	h := localhostOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://evil.example/", nil)
	req.Host = "evil.example:4078"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected non-loopback host to be rejected, got status %d", rr.Code)
	}
}

func TestSecurityHeaders_SetCSPAndFrameProtections(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))

	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected X-Frame-Options DENY, got %q", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options nosniff, got %q", got)
	}
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("expected strict CSP default-src self, got %q", csp)
	}
}

func TestWebAPI_PrivateNotesAreFiltered(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	insertLiteNote(t, db, "_PRIVATE/secret.md", "Secret", "do not leak")
	insertLiteNote(t, db, "notes/public.md", "Public", "safe")

	s := &server{db: db}

	rr := httptest.NewRecorder()
	s.handleAllNotes(rr, httptest.NewRequest(http.MethodGet, "/api/notes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/notes, got %d", rr.Code)
	}

	var payload struct {
		Notes     []map[string]any `json:"notes"`
		Truncated bool             `json:"truncated"`
		Limit     int              `json:"limit"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode notes response: %v", err)
	}
	if len(payload.Notes) != 1 {
		t.Fatalf("expected only non-private notes, got %d entries", len(payload.Notes))
	}
	if payload.Truncated {
		t.Fatalf("unexpected truncation in small notes response")
	}
	if payload.Limit != 200 {
		t.Fatalf("expected default limit 200, got %d", payload.Limit)
	}
	if strings.HasPrefix(strings.ToUpper(payload.Notes[0]["path"].(string)), "_PRIVATE/") {
		t.Fatalf("private note leaked in API response: %+v", payload.Notes[0])
	}

	privateReq := httptest.NewRequest(http.MethodGet, "/api/notes/_PRIVATE/secret.md", nil)
	privateRR := httptest.NewRecorder()
	s.handleNoteByPath(privateRR, privateReq)
	if privateRR.Code != http.StatusNotFound {
		t.Fatalf("expected private note endpoint to return 404, got %d", privateRR.Code)
	}
}

func TestWebNoteJSONAndRendererDefensesAgainstXSS(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	insertLiteNote(t, db, "notes/xss.md", "XSS", "<script>alert(1)</script>\n[click](javascript:alert(2))")
	s := &server{db: db}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notes/notes/xss.md", nil)
	s.handleNoteByPath(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from note endpoint, got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "<script>") {
		t.Fatalf("expected JSON response to escape script tags, got %q", body)
	}

	indexBytes, err2 := staticFS.ReadFile("static/index.html")
	if err2 != nil {
		t.Fatalf("read embedded index.html: %v", err2)
	}
	page := string(indexBytes)
	mustContain := []string{
		"function escapeHTML(str)",
		"var escaped = escapeHTML(text);",
		"if (/^(javascript|data|vbscript)\\s*:/i.test(scheme))",
	}
	for _, token := range mustContain {
		if !strings.Contains(page, token) {
			t.Fatalf("expected embedded UI to include XSS guard %q", token)
		}
	}
}

func TestAllNotes_DefaultLimit(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	for i := 0; i < 250; i++ {
		path := fmt.Sprintf("notes/n-%03d.md", i)
		insertLiteNote(t, db, path, fmt.Sprintf("N-%03d", i), "note body")
	}

	s := &server{db: db}
	rr := httptest.NewRecorder()
	s.handleAllNotes(rr, httptest.NewRequest(http.MethodGet, "/api/notes", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/notes, got %d", rr.Code)
	}

	var payload struct {
		Notes     []map[string]any `json:"notes"`
		Truncated bool             `json:"truncated"`
		Limit     int              `json:"limit"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode notes payload: %v", err)
	}
	if !payload.Truncated {
		t.Fatalf("expected truncated=true for >200 notes")
	}
	if payload.Limit != 200 {
		t.Fatalf("expected default limit 200, got %d", payload.Limit)
	}
	if len(payload.Notes) != 200 {
		t.Fatalf("expected 200 notes in default response, got %d", len(payload.Notes))
	}
}
