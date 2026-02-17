package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/graph"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestWebGraphStatsAndConnections(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	insertLiteNote(t, db, "notes/architecture.md", "Architecture", "References notes/privacy.md for compliance.")
	insertLiteNote(t, db, "notes/privacy.md", "Privacy", "Privacy decisions and boundaries.")

	gdb := graph.NewDB(db.Conn())
	aID, err := gdb.UpsertNode(&graph.Node{Type: graph.NodeNote, Name: "notes/architecture.md"})
	if err != nil {
		t.Fatalf("upsert architecture node: %v", err)
	}
	pID, err := gdb.UpsertNode(&graph.Node{Type: graph.NodeNote, Name: "notes/privacy.md"})
	if err != nil {
		t.Fatalf("upsert privacy node: %v", err)
	}
	if _, err := gdb.UpsertEdge(&graph.Edge{SourceID: aID, TargetID: pID, Relationship: graph.RelReferences, Weight: 1}); err != nil {
		t.Fatalf("upsert graph edge: %v", err)
	}

	s := &server{db: db}

	rrStats := httptest.NewRecorder()
	s.handleGraphStats(rrStats, httptest.NewRequest(http.MethodGet, "/api/graph/stats", nil))
	if rrStats.Code != http.StatusOK {
		t.Fatalf("expected graph stats 200, got %d", rrStats.Code)
	}

	var stats map[string]any
	if err := json.NewDecoder(rrStats.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats response: %v", err)
	}
	if totalNodes, ok := stats["total_nodes"].(float64); !ok || int(totalNodes) < 2 {
		t.Fatalf("expected at least 2 graph nodes, got %#v", stats["total_nodes"])
	}
	if totalEdges, ok := stats["total_edges"].(float64); !ok || int(totalEdges) < 1 {
		t.Fatalf("expected at least 1 graph edge, got %#v", stats["total_edges"])
	}

	rrConn := httptest.NewRecorder()
	reqConn := httptest.NewRequest(http.MethodGet, "/api/graph/connections/notes/architecture.md?depth=2&dir=forward", nil)
	s.handleGraphConnections(rrConn, reqConn)
	if rrConn.Code != http.StatusOK {
		t.Fatalf("expected graph connections 200, got %d", rrConn.Code)
	}

	var conn map[string]any
	if err := json.NewDecoder(rrConn.Body).Decode(&conn); err != nil {
		t.Fatalf("decode connections response: %v", err)
	}
	if count, ok := conn["count"].(float64); !ok || int(count) < 1 {
		t.Fatalf("expected at least one graph path, got %#v", conn["count"])
	}
}

func TestWebGraphConnections_PathSecurityAndPrivateFiltering(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	insertLiteNote(t, db, "notes/public.md", "Public", "Public content")
	insertLiteNote(t, db, "_PRIVATE/secret.md", "Secret", "Should never leak")

	gdb := graph.NewDB(db.Conn())
	pubID, err := gdb.UpsertNode(&graph.Node{Type: graph.NodeNote, Name: "notes/public.md"})
	if err != nil {
		t.Fatalf("upsert public node: %v", err)
	}
	secretID, err := gdb.UpsertNode(&graph.Node{Type: graph.NodeNote, Name: "_PRIVATE/secret.md"})
	if err != nil {
		t.Fatalf("upsert private node: %v", err)
	}
	if _, err := gdb.UpsertEdge(&graph.Edge{SourceID: pubID, TargetID: secretID, Relationship: graph.RelReferences, Weight: 1}); err != nil {
		t.Fatalf("upsert private edge: %v", err)
	}

	s := &server{db: db}

	rrTraversal := httptest.NewRecorder()
	reqTraversal := httptest.NewRequest(http.MethodGet, "/api/graph/connections/../etc/passwd", nil)
	s.handleGraphConnections(rrTraversal, reqTraversal)
	if rrTraversal.Code != http.StatusBadRequest {
		t.Fatalf("expected traversal path to be rejected, got %d", rrTraversal.Code)
	}

	rrRel := httptest.NewRecorder()
	reqRel := httptest.NewRequest(http.MethodGet, "/api/graph/connections/notes/public.md?rel=bad%20relationship", nil)
	s.handleGraphConnections(rrRel, reqRel)
	if rrRel.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid relationship filter to be rejected, got %d", rrRel.Code)
	}

	rrPrivate := httptest.NewRecorder()
	reqPrivate := httptest.NewRequest(http.MethodGet, "/api/graph/connections/_PRIVATE/secret.md", nil)
	s.handleGraphConnections(rrPrivate, reqPrivate)
	if rrPrivate.Code != http.StatusNotFound {
		t.Fatalf("expected private start path to be hidden, got %d", rrPrivate.Code)
	}

	rrConn := httptest.NewRecorder()
	reqConn := httptest.NewRequest(http.MethodGet, "/api/graph/connections/notes/public.md?depth=2", nil)
	s.handleGraphConnections(rrConn, reqConn)
	if rrConn.Code != http.StatusOK {
		t.Fatalf("expected graph connections 200, got %d", rrConn.Code)
	}
	body := rrConn.Body.String()
	if strings.Contains(strings.ToUpper(body), "_PRIVATE/") {
		t.Fatalf("private graph node leaked in response: %s", body)
	}
}
