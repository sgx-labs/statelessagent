package graph

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Run graph schema SQL
	for _, stmt := range GraphSchemaSQL() {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { conn.Close() })
	return NewDB(conn)
}

func TestUpsertNode(t *testing.T) {
	db := setupTestDB(t)

	n := &Node{Type: NodeNote, Name: "foo.md"}
	id, err := db.UpsertNode(n)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Idempotency
	id2, err := db.UpsertNode(n)
	if err != nil {
		t.Fatal(err)
	}
	if id != id2 {
		t.Errorf("expected same ID %d, got %d", id, id2)
	}

	// Update
	n.Properties = `{"foo":"bar"}`
	id3, err := db.UpsertNode(n)
	if err != nil {
		t.Fatal(err)
	}
	if id != id3 {
		t.Errorf("expected same ID %d, got %d", id, id3)
	}

	fetched, err := db.GetNode(id)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Properties != n.Properties {
		t.Errorf("expected properties %s, got %s", n.Properties, fetched.Properties)
	}
}

func TestUpsertEdge(t *testing.T) {
	db := setupTestDB(t)

	n1 := &Node{Type: NodeNote, Name: "foo.md"}
	n2 := &Node{Type: NodeFile, Name: "foo.go"}
	id1, _ := db.UpsertNode(n1)
	id2, _ := db.UpsertNode(n2)

	e := &Edge{
		SourceID:     id1,
		TargetID:     id2,
		Relationship: RelReferences,
	}
	id, err := db.UpsertEdge(e)
	if err != nil {
		t.Fatal(err)
	}

	// Idempotency
	id2Edge, err := db.UpsertEdge(e)
	if err != nil {
		t.Fatal(err)
	}
	if id != id2Edge {
		t.Errorf("expected same ID %d, got %d", id, id2Edge)
	}
}

func TestGetNeighbors(t *testing.T) {
	db := setupTestDB(t)

	// A -> B (imports)
	// B -> C (imports)
	// A -> C (depends_on)
	n1, _ := db.UpsertNode(&Node{Type: "A", Name: "A"})
	n2, _ := db.UpsertNode(&Node{Type: "B", Name: "B"})
	n3, _ := db.UpsertNode(&Node{Type: "C", Name: "C"})

	db.UpsertEdge(&Edge{SourceID: n1, TargetID: n2, Relationship: RelImports})
	db.UpsertEdge(&Edge{SourceID: n2, TargetID: n3, Relationship: RelImports})
	db.UpsertEdge(&Edge{SourceID: n1, TargetID: n3, Relationship: RelDependsOn})

	// Forward from A
	neighbors, err := db.GetNeighbors(n1, "", "forward")
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 2 {
		t.Errorf("expected 2 neighbors, got %d", len(neighbors))
	}

	// Reverse from C
	neighbors, err = db.GetNeighbors(n3, "", "reverse")
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 2 { // A and B
		t.Errorf("expected 2 neighbors, got %d", len(neighbors))
	}

	// Filter by relationship
	neighbors, err = db.GetNeighbors(n1, RelImports, "forward")
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 1 {
		t.Errorf("expected 1 neighbor, got %d", len(neighbors))
	}
	if neighbors[0].ID != n2 {
		t.Errorf("expected B, got %d", neighbors[0].ID)
	}
}

func TestQueryGraph(t *testing.T) {
	db := setupTestDB(t)

	// Chain: 1 -> 2 -> 3 -> 4
	ids := make([]int64, 5)
	for i := 1; i <= 4; i++ {
		ids[i], _ = db.UpsertNode(&Node{Type: "N", Name: fmt.Sprintf("%d", i)})
	}
	for i := 1; i < 4; i++ {
		db.UpsertEdge(&Edge{SourceID: ids[i], TargetID: ids[i+1], Relationship: "next"})
	}

	// Traversal from 1
	paths, err := db.QueryGraph(QueryOptions{
		FromNodeID: ids[1],
		Direction:  "forward",
		MaxDepth:   5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should find paths to 2, 3, 4
	// The implementation returns one Path object per discovered node in traversal
	if len(paths) < 3 {
		t.Errorf("expected at least 3 paths (to 2, 3, 4), got %d", len(paths))
	}

	foundRichPath := false
	for _, p := range paths {
		if len(p.Nodes) >= 2 && len(p.Edges) == len(p.Nodes)-1 {
			foundRichPath = true
			for _, e := range p.Edges {
				if e.Relationship != "next" {
					t.Errorf("expected relationship 'next', got %q", e.Relationship)
				}
			}
		}
	}
	if !foundRichPath {
		t.Fatalf("expected at least one path with reconstructed edges")
	}
}

func TestFindShortestPath(t *testing.T) {
	db := setupTestDB(t)

	// 1 -> 2 -> 3
	// 1 -> 3 (shortcut)
	n1, _ := db.UpsertNode(&Node{Type: "N", Name: "1"})
	n2, _ := db.UpsertNode(&Node{Type: "N", Name: "2"})
	n3, _ := db.UpsertNode(&Node{Type: "N", Name: "3"})

	db.UpsertEdge(&Edge{SourceID: n1, TargetID: n2, Relationship: "next"})
	db.UpsertEdge(&Edge{SourceID: n2, TargetID: n3, Relationship: "next"})
	db.UpsertEdge(&Edge{SourceID: n1, TargetID: n3, Relationship: "shortcut"})

	path, err := db.FindShortestPath(n1, n3)
	if err != nil {
		t.Fatal(err)
	}
	if path == nil {
		t.Fatal("path not found")
	}

	// Should take shortcut (length 2 nodes: 1, 3)
	if len(path.Nodes) != 2 {
		t.Errorf("expected path length 2 (1->3), got %d", len(path.Nodes))
	}
}

func TestFindShortestPath_SameNode(t *testing.T) {
	db := setupTestDB(t)

	n1, _ := db.UpsertNode(&Node{Type: "N", Name: "1"})

	path, err := db.FindShortestPath(n1, n1)
	if err != nil {
		t.Fatal(err)
	}
	if path == nil {
		t.Fatal("expected self path")
	}
	if len(path.Nodes) != 1 {
		t.Fatalf("expected 1 node for self path, got %d", len(path.Nodes))
	}
	if len(path.Edges) != 0 {
		t.Fatalf("expected 0 edges for self path, got %d", len(path.Edges))
	}
	if path.Nodes[0].ID != n1 {
		t.Fatalf("expected self node %d, got %d", n1, path.Nodes[0].ID)
	}
}

func TestExtractFromNote(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	content := `
	package foo
	import "github.com/example/pkg"
	
	// Check internal/bar/baz.go for details.
	// We decided: use SQLite.
	`

	// Mock vault note exists? No, graph logic creates note node if missing in graph,
	// but note_id FK might fail if we enforce FK constraint and vault_notes doesn't exist.
	// However, in testDB setup, we didn't create vault_notes table.
	// Graph schema has FOREIGN KEY (note_id) REFERENCES vault_notes(id).
	// SQLite ignores FKs by default unless PRAGMA foreign_keys = ON.
	// We didn't enable it in setupTestDB, so it should pass.

	err := ext.ExtractFromNote(100, "foo.go", content, "AgentSmith")
	if err != nil {
		t.Fatal(err)
	}

	// Check Agent node
	agent, err := db.FindNode(NodeAgent, "AgentSmith")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name != "AgentSmith" {
		t.Errorf("expected AgentSmith, got %s", agent.Name)
	}

	// Check Decision node
	// "use SQLite"
	// We don't know the exact name extracted, but let's count decisions
	stats, _ := db.GetStats()
	if stats.NodesByType[NodeDecision] != 1 {
		t.Errorf("expected 1 decision, got %d", stats.NodesByType[NodeDecision])
	}

	// Check File node
	if stats.NodesByType[NodeFile] != 1 {
		t.Errorf("expected 1 file (internal/bar/baz.go), got %d", stats.NodesByType[NodeFile])
	}
}

func TestExtractFromNote_MarkdownReferenceBecomesNoteNode(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	content := `
	See notes/other.md for prior context.
	`

	if err := ext.ExtractFromNote(200, "notes/current.md", content, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := db.FindNode(NodeNote, "notes/other.md"); err != nil {
		t.Fatalf("expected markdown reference to create note node: %v", err)
	}

	if _, err := db.FindNode(NodeFile, "notes/other.md"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no file node for markdown reference, got: %v", err)
	}

	var count int
	if err := db.conn.QueryRow(`
		SELECT COUNT(*)
		FROM graph_edges e
		JOIN graph_nodes src ON src.id = e.source_id
		JOIN graph_nodes dst ON dst.id = e.target_id
		WHERE src.type = 'note' AND src.name = 'notes/current.md'
		  AND dst.type = 'note' AND dst.name = 'notes/other.md'
		  AND e.relationship = 'references'
	`).Scan(&count); err != nil {
		t.Fatalf("count note->note references: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one note->note reference edge, got %d", count)
	}
}

func TestExtractFromNote_IgnoresExternalOrAbsoluteLikeReferences(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	content := `
	See Users/jdoe/.windsurf/worktrees/myproject/main.go and tmp/same-graph-test/notes/a.md.
	Also see internal/store/db.go and notes/next.md.
	`

	if err := ext.ExtractFromNote(300, "notes/current.md", content, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := db.FindNode(NodeFile, "Users/jdoe/.windsurf/worktrees/myproject/main.go"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected external Users/ path to be ignored, got err=%v", err)
	}
	if _, err := db.FindNode(NodeNote, "tmp/same-graph-test/notes/a.md"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected tmp/ path to be ignored, got err=%v", err)
	}

	if _, err := db.FindNode(NodeFile, "internal/store/db.go"); err != nil {
		t.Fatalf("expected in-vault file node: %v", err)
	}
	if _, err := db.FindNode(NodeNote, "notes/next.md"); err != nil {
		t.Fatalf("expected in-vault note node: %v", err)
	}
}

func TestExtractFromNote_IgnoresExternalFileURLReferences(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	content := `*Viewed [AGENTS.md](file:///Users/jdoe/.windsurf/worktrees/myproject/myproject-f716fdc5/AGENTS.md)*`

	if err := ext.ExtractFromNote(301, "notes/current.md", content, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := db.FindNode(NodeNote, "Users/jdoe/.windsurf/worktrees/myproject/myproject-f716fdc5/AGENTS.md"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected file:// external path to be ignored, got err=%v", err)
	}
}

func TestExtractFromNote_IgnoresExternalHTTPDomainReferences(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	content := `
See github.com/mark3labs/mcp-filesystem-server/blob/main/smithery.yaml
https://ollama.com/install.sh
raw.githubusercontent.com/org/repo/main/install.sh
statelessagent.com/install.sh
static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json
Also see internal/graph/extraction.go.
`

	if err := ext.ExtractFromNote(302, "notes/current.md", content, ""); err != nil {
		t.Fatal(err)
	}

	// External domains should never be modeled as local file/note nodes.
	var externalCount int
	if err := db.conn.QueryRow(`
		SELECT COUNT(*)
		FROM graph_nodes
		WHERE type IN ('file', 'note')
		  AND (
			name LIKE 'http%'
			OR name LIKE '%.com/%'
			OR name LIKE '%.io/%'
			OR name LIKE '%.org/%'
			OR name LIKE '%.ai/%'
		  )
	`).Scan(&externalCount); err != nil {
		t.Fatalf("count external domain nodes: %v", err)
	}
	if externalCount != 0 {
		t.Fatalf("expected 0 external domain nodes, got %d", externalCount)
	}

	if _, err := db.FindNode(NodeFile, "internal/graph/extraction.go"); err != nil {
		t.Fatalf("expected local file node to be retained: %v", err)
	}
}

func TestExtractFromNote_IgnoresPlaceholderTemplateAndMalformedPaths(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	content := `
VAULT_PATH/.same/config.toml
internal/pkg/foo.go
vault/path/to/note.md
sessions/YYYY-MM-DD-auto-handoff.md
sessions/YYYY-MM-DD-rich-handoff.md
test_vault/notes/a.md
test_vault/notes/b.md
test_vault/notes/c.md
_PRIVATE/api-keys.md
_PRIVATE/secret.md
README.md/llms-install.md
internal/graph/extraction.go
notes/real.md
`

	if err := ext.ExtractFromNote(303, "notes/current.md", content, ""); err != nil {
		t.Fatal(err)
	}

	rejected := []struct {
		typ  string
		name string
	}{
		{NodeFile, "VAULT_PATH/.same/config.toml"},
		{NodeFile, "internal/pkg/foo.go"},
		{NodeNote, "vault/path/to/note.md"},
		{NodeNote, "sessions/YYYY-MM-DD-auto-handoff.md"},
		{NodeNote, "sessions/YYYY-MM-DD-rich-handoff.md"},
		{NodeNote, "test_vault/notes/a.md"},
		{NodeNote, "test_vault/notes/b.md"},
		{NodeNote, "test_vault/notes/c.md"},
		{NodeNote, "_PRIVATE/api-keys.md"},
		{NodeNote, "_PRIVATE/secret.md"},
		{NodeNote, "README.md/llms-install.md"},
	}

	for _, r := range rejected {
		if _, err := db.FindNode(r.typ, r.name); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected %s node %q to be rejected, got err=%v", r.typ, r.name, err)
		}
	}

	if _, err := db.FindNode(NodeFile, "internal/graph/extraction.go"); err != nil {
		t.Fatalf("expected valid local file node to be retained: %v", err)
	}
	if _, err := db.FindNode(NodeNote, "notes/real.md"); err != nil {
		t.Fatalf("expected valid local note node to be retained: %v", err)
	}
}

func TestExtractFromNote_DecisionsSkipFencedCodeAndLowQualityFragments(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	content := `
We decided: use SQLite." > test_vault/notes/b.md && echo "# Note C\n\n..."
Example docs: "We chose to ...".
Decision: whether SAME injected or skipped, the conversation mode detected, and log fields were emitted.

` + "```bash" + `
Decision: use Redis for caching.
echo "We chose to shell out for everything"
` + "```" + `

We chose to keep regex extraction as the default fallback.
Decision: adopt deterministic chunking for indexing.
`

	if err := ext.ExtractFromNote(303, "notes/current.md", content, ""); err != nil {
		t.Fatal(err)
	}

	rows, err := db.conn.Query(`SELECT name FROM graph_nodes WHERE type = ?`, NodeDecision)
	if err != nil {
		t.Fatalf("query decision nodes: %v", err)
	}
	defer rows.Close()

	got := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan decision node: %v", err)
		}
		got[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate decision rows: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected exactly 2 high-quality decision nodes, got %d (%v)", len(got), got)
	}
	if !got["keep regex extraction as the default fallback"] {
		t.Fatalf("expected clean decision from prose extraction, got %v", got)
	}
	if !got["adopt deterministic chunking for indexing"] {
		t.Fatalf("expected clean decision from decision label extraction, got %v", got)
	}
	if got["use Redis for caching"] {
		t.Fatalf("expected fenced code decision to be ignored, got %v", got)
	}
	for name := range got {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "conversation mode detected") || strings.Contains(lower, "injected or skipped") {
			t.Fatalf("expected descriptive/non-decision text to be rejected, got %q", name)
		}
	}
}

func TestNormalizeGraphReferencePath(t *testing.T) {
	tests := []struct {
		name     string
		notePath string
		input    string
		want     string
		ok       bool
	}{
		{name: "relative same dir", notePath: "notes/current.md", input: "./next.md", want: "notes/next.md", ok: true},
		{name: "relative parent", notePath: "notes/current.md", input: "../README.md", want: "README.md", ok: true},
		{name: "escape parent rejected", notePath: "notes/current.md", input: "../../outside.md", ok: false},
		{name: "absolute rejected", notePath: "notes/current.md", input: "/Users/dev/file.go", ok: false},
		{name: "users prefix rejected", notePath: "notes/current.md", input: "Users/dev/file.go", ok: false},
		{name: "http url rejected", notePath: "notes/current.md", input: "https://ollama.com/install.sh", ok: false},
		{name: "domain path rejected", notePath: "notes/current.md", input: "github.com/org/repo/file.go", ok: false},
		{name: "placeholder vault path rejected", notePath: "notes/current.md", input: "VAULT_PATH/.same/config.toml", ok: false},
		{name: "placeholder date rejected", notePath: "notes/current.md", input: "sessions/YYYY-MM-DD-auto-handoff.md", ok: false},
		{name: "private path rejected", notePath: "notes/current.md", input: "_PRIVATE/secret.md", ok: false},
		{name: "test vault path rejected", notePath: "notes/current.md", input: "test_vault/notes/a.md", ok: false},
		{name: "path to placeholder rejected", notePath: "notes/current.md", input: "vault/path/to/note.md", ok: false},
		{name: "foo placeholder rejected", notePath: "notes/current.md", input: "internal/pkg/foo.go", ok: false},
		{name: "file as directory rejected", notePath: "notes/current.md", input: "README.md/llms-install.md", ok: false},
		{name: "normal repo path", notePath: "notes/current.md", input: "internal/store/db.go", want: "internal/store/db.go", ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeGraphReferencePath(tt.notePath, tt.input)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tt.ok, got)
			}
			if tt.ok && got != tt.want {
				t.Fatalf("path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGraphQualityFixture_NoteReferencesAreTraversable(t *testing.T) {
	db := setupTestDB(t)
	ext := NewExtractor(db)

	alphaContent := `
We decided: split command handlers for clarity.
See notes/beta.md and internal/indexer/indexer.go.
`
	betaContent := `
We chose to keep regex extraction as the default fallback.
`

	if err := ext.ExtractFromNote(1, "notes/alpha.md", alphaContent, "woody"); err != nil {
		t.Fatalf("extract alpha: %v", err)
	}
	if err := ext.ExtractFromNote(2, "notes/beta.md", betaContent, "buzz"); err != nil {
		t.Fatalf("extract beta: %v", err)
	}

	stats, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.NodesByType[NodeNote] < 2 {
		t.Fatalf("expected at least 2 note nodes, got %d", stats.NodesByType[NodeNote])
	}
	if stats.NodesByType[NodeAgent] < 2 {
		t.Fatalf("expected at least 2 agent nodes, got %d", stats.NodesByType[NodeAgent])
	}
	if stats.NodesByType[NodeDecision] < 2 {
		t.Fatalf("expected at least 2 decision nodes, got %d", stats.NodesByType[NodeDecision])
	}
	if stats.NodesByType[NodeFile] < 1 {
		t.Fatalf("expected at least 1 file node, got %d", stats.NodesByType[NodeFile])
	}

	alpha, err := db.FindNode(NodeNote, "notes/alpha.md")
	if err != nil {
		t.Fatalf("find alpha node: %v", err)
	}
	beta, err := db.FindNode(NodeNote, "notes/beta.md")
	if err != nil {
		t.Fatalf("find beta node: %v", err)
	}

	path, err := db.FindShortestPath(alpha.ID, beta.ID)
	if err != nil {
		t.Fatalf("FindShortestPath alpha->beta: %v", err)
	}
	if path == nil {
		t.Fatal("expected traversable path from alpha to beta")
	}
	if len(path.Nodes) != 2 {
		t.Fatalf("expected direct 2-node path alpha->beta, got %d nodes", len(path.Nodes))
	}
	if len(path.Edges) != 1 || path.Edges[0].Relationship != RelReferences {
		t.Fatalf("expected single references edge, got %#v", path.Edges)
	}
}

func BenchmarkQueryGraph(b *testing.B) {
	db := setupTestDB(&testing.T{})

	// Create chain of 1000 nodes
	prevID, _ := db.UpsertNode(&Node{Type: "N", Name: "0"})
	startID := prevID
	for i := 1; i < 1000; i++ {
		currID, _ := db.UpsertNode(&Node{Type: "N", Name: fmt.Sprintf("%d", i)})
		db.UpsertEdge(&Edge{SourceID: prevID, TargetID: currID, Relationship: "next"})
		prevID = currID
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.QueryGraph(QueryOptions{
			FromNodeID: startID,
			Direction:  "forward",
			MaxDepth:   5,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
