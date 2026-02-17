package graph

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Node types
const (
	NodeNote     = "note"
	NodeFile     = "file"
	NodeAgent    = "agent"
	NodeDecision = "decision"
	NodeSession  = "session"
	NodeEntity   = "entity"
)

// Relationship types
const (
	RelImports    = "imports"
	RelWorkedOn   = "worked_on"
	RelAffects    = "affects"
	RelProduced   = "produced"
	RelMentions   = "mentions"
	RelRelatedTo  = "related_to"
	RelDependsOn  = "depends_on"
	RelReferences = "references"
)

type Node struct {
	ID         int64
	Type       string
	Name       string
	NoteID     *int64 // nullable — link to vault_notes.id
	Properties string // JSON blob
	CreatedAt  int64  // unix timestamp
}

type Edge struct {
	ID           int64
	SourceID     int64
	TargetID     int64
	Relationship string
	Weight       float64
	Properties   string // JSON blob
	CreatedAt    int64
}

type Path struct {
	Nodes       []Node
	Edges       []Edge
	TotalWeight float64
}

type Subgraph struct {
	Nodes []Node
	Edges []Edge
}

type QueryOptions struct {
	FromNodeID   int64
	FromNodeType string
	FromNodeName string
	Relationship string  // filter by relationship type (empty = all)
	Direction    string  // "forward", "reverse", "both"
	MaxDepth     int     // limit traversal depth (default 5, max 10)
	MinWeight    float64 // filter by edge weight
}

type Stats struct {
	TotalNodes          int
	TotalEdges          int
	NodesByType         map[string]int
	EdgesByRelationship map[string]int
	AvgDegree           float64
}

// DB wraps a *sql.DB for graph operations.
// It does NOT own the connection — the caller (store.DB) owns it.
type DB struct {
	conn *sql.DB
}

func NewDB(conn *sql.DB) *DB {
	return &DB{conn: conn}
}

// UpsertNode inserts or updates a node by (type, name).
func (db *DB) UpsertNode(node *Node) (int64, error) {
	if node.Properties == "" {
		node.Properties = "{}"
	}
	if node.CreatedAt == 0 {
		node.CreatedAt = time.Now().Unix()
	}

	query := `
		INSERT INTO graph_nodes (type, name, note_id, properties, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(type, name) DO UPDATE SET
			note_id = COALESCE(excluded.note_id, graph_nodes.note_id),
			properties = excluded.properties
		RETURNING id`

	var id int64
	err := db.conn.QueryRow(query, node.Type, node.Name, node.NoteID, node.Properties, node.CreatedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert node: %w", err)
	}
	return id, nil
}

// UpsertEdge inserts or updates an edge by (source, target, relationship).
func (db *DB) UpsertEdge(edge *Edge) (int64, error) {
	if edge.Properties == "" {
		edge.Properties = "{}"
	}
	if edge.CreatedAt == 0 {
		edge.CreatedAt = time.Now().Unix()
	}
	if edge.Weight == 0 {
		edge.Weight = 1.0
	}

	query := `
		INSERT INTO graph_edges (source_id, target_id, relationship, weight, properties, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, target_id, relationship) DO UPDATE SET
			weight = excluded.weight,
			properties = excluded.properties
		RETURNING id`

	var id int64
	err := db.conn.QueryRow(query, edge.SourceID, edge.TargetID, edge.Relationship, edge.Weight, edge.Properties, edge.CreatedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert edge: %w", err)
	}
	return id, nil
}

// GetNode retrieves a node by ID.
func (db *DB) GetNode(id int64) (*Node, error) {
	var n Node
	err := db.conn.QueryRow(`
		SELECT id, type, name, note_id, properties, created_at
		FROM graph_nodes WHERE id = ?`, id).Scan(
		&n.ID, &n.Type, &n.Name, &n.NoteID, &n.Properties, &n.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// FindNode retrieves a node by type and name.
func (db *DB) FindNode(nodeType, name string) (*Node, error) {
	var n Node
	err := db.conn.QueryRow(`
		SELECT id, type, name, note_id, properties, created_at
		FROM graph_nodes WHERE type = ? AND name = ?`, nodeType, name).Scan(
		&n.ID, &n.Type, &n.Name, &n.NoteID, &n.Properties, &n.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// GetNeighbors returns adjacent nodes filtered by relationship and direction.
func (db *DB) GetNeighbors(nodeID int64, relationship string, direction string) ([]Node, error) {
	var query string
	var args []interface{}

	baseQuery := `SELECT n.id, n.type, n.name, n.note_id, n.properties, n.created_at FROM graph_nodes n `

	switch direction {
	case "forward":
		query = baseQuery + `JOIN graph_edges e ON e.target_id = n.id WHERE e.source_id = ?`
		args = append(args, nodeID)
	case "reverse":
		query = baseQuery + `JOIN graph_edges e ON e.source_id = n.id WHERE e.target_id = ?`
		args = append(args, nodeID)
	case "both":
		query = baseQuery + `
			JOIN graph_edges e ON (e.target_id = n.id AND e.source_id = ?) OR (e.source_id = n.id AND e.target_id = ?)
			WHERE n.id != ?`
		args = append(args, nodeID, nodeID, nodeID)
	default:
		return nil, fmt.Errorf("invalid direction: %s", direction)
	}

	if relationship != "" {
		query += ` AND e.relationship = ?`
		args = append(args, relationship)
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Type, &n.Name, &n.NoteID, &n.Properties, &n.CreatedAt); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

// QueryGraph performs a recursive traversal using CTEs to find paths.
// Currently supports 'forward' and 'reverse' directions.
// Returns paths from the start node to all discovered nodes within MaxDepth.
func (db *DB) QueryGraph(opts QueryOptions) ([]Path, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 5
	}
	if opts.MaxDepth > 10 {
		opts.MaxDepth = 10
	}

	startNodeID := opts.FromNodeID
	if startNodeID == 0 && opts.FromNodeType != "" && opts.FromNodeName != "" {
		n, err := db.FindNode(opts.FromNodeType, opts.FromNodeName)
		if err != nil {
			return nil, fmt.Errorf("start node not found: %w", err)
		}
		startNodeID = n.ID
	}

	if startNodeID == 0 {
		return nil, fmt.Errorf("start node required")
	}

	if opts.Direction != "forward" && opts.Direction != "reverse" {
		return nil, fmt.Errorf("direction '%s' not supported for recursive traversal", opts.Direction)
	}

	// Recursive CTE to capture the traversal tree
	// We capture the path string (comma-separated IDs) to reconstruct paths later
	cte := `
	WITH RECURSIVE traversal(id, source_id, target_id, relationship, weight, depth, path_ids, path_edge_ids) AS (
		-- Base case
		SELECT id, source_id, target_id, relationship, weight, 1, 
			cast(source_id as text) || ',' || cast(target_id as text),
			cast(id as text)
		FROM graph_edges
		WHERE ` + map[string]string{
		"forward": "source_id = ?",
		"reverse": "target_id = ?",
	}[opts.Direction] + `
		  AND (? = '' OR relationship = ?)
		  AND weight >= ?
		
		UNION ALL
		
		-- Recursive step
		SELECT e.id, e.source_id, e.target_id, e.relationship, e.weight, t.depth + 1, 
			t.path_ids || ',' || cast(` + map[string]string{
		"forward": "e.target_id",
		"reverse": "e.source_id",
	}[opts.Direction] + ` as text),
			t.path_edge_ids || ',' || cast(e.id as text)
		FROM graph_edges e
		JOIN traversal t ON ` + map[string]string{
		"forward": "t.target_id = e.source_id",
		"reverse": "t.source_id = e.target_id",
	}[opts.Direction] + `
		WHERE t.depth < ?
		  AND (? = '' OR e.relationship = ?)
		  AND e.weight >= ?
		  -- Cycle detection: check if next node is already in path
		  AND instr(',' || t.path_ids || ',', ',' || cast(` + map[string]string{
		"forward": "e.target_id",
		"reverse": "e.source_id",
	}[opts.Direction] + ` as text) || ',') = 0
	)
	SELECT id, source_id, target_id, relationship, weight, depth, path_ids, path_edge_ids FROM traversal
	LIMIT 1000`

	rows, err := db.conn.Query(cte,
		startNodeID,
		opts.Relationship, opts.Relationship,
		opts.MinWeight,
		opts.MaxDepth,
		opts.Relationship, opts.Relationship,
		opts.MinWeight,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Parse results and build Path objects
	// Since we can't easily JOIN nodes in the CTE without explosion, we'll fetch nodes separately
	type traversalRow struct {
		EdgeID      int64
		SourceID    int64
		TargetID    int64
		Rel         string
		Weight      float64
		Depth       int
		PathIDs     string
		PathEdgeIDs string
	}

	var rowsData []traversalRow
	nodeIDs := make(map[int64]bool)
	edgeIDs := make(map[int64]bool)
	nodeIDs[startNodeID] = true

	for rows.Next() {
		var r traversalRow
		if err := rows.Scan(&r.EdgeID, &r.SourceID, &r.TargetID, &r.Rel, &r.Weight, &r.Depth, &r.PathIDs, &r.PathEdgeIDs); err != nil {
			return nil, err
		}
		rowsData = append(rowsData, r)
		nodeIDs[r.SourceID] = true
		nodeIDs[r.TargetID] = true
		edgeIDs[r.EdgeID] = true
		for _, edgeID := range parseIDList(r.PathEdgeIDs) {
			edgeIDs[edgeID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch all nodes involved
	nodes := make(map[int64]Node)
	if len(nodeIDs) > 0 {
		// Batched fetch would be better, but loop is simple for now
		// Or use a single query with IN clause
		ids := make([]string, 0, len(nodeIDs))
		args := make([]interface{}, 0, len(nodeIDs))
		for id := range nodeIDs {
			ids = append(ids, "?")
			args = append(args, id)
		}
		q := "SELECT id, type, name, note_id, properties, created_at FROM graph_nodes WHERE id IN (" + strings.Join(ids, ",") + ")"
		nRows, err := db.conn.Query(q, args...)
		if err != nil {
			return nil, err
		}
		defer nRows.Close()
		for nRows.Next() {
			var n Node
			if err := nRows.Scan(&n.ID, &n.Type, &n.Name, &n.NoteID, &n.Properties, &n.CreatedAt); err != nil {
				return nil, err
			}
			nodes[n.ID] = n
		}
		if err := nRows.Err(); err != nil {
			return nil, err
		}
	}

	// Fetch all edges involved
	edges := make(map[int64]Edge)
	if len(edgeIDs) > 0 {
		ids := make([]string, 0, len(edgeIDs))
		args := make([]interface{}, 0, len(edgeIDs))
		for id := range edgeIDs {
			ids = append(ids, "?")
			args = append(args, id)
		}
		q := "SELECT id, source_id, target_id, relationship, weight, properties, created_at FROM graph_edges WHERE id IN (" + strings.Join(ids, ",") + ")"
		eRows, err := db.conn.Query(q, args...)
		if err != nil {
			return nil, err
		}
		defer eRows.Close()
		for eRows.Next() {
			var e Edge
			if err := eRows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.Relationship, &e.Weight, &e.Properties, &e.CreatedAt); err != nil {
				return nil, err
			}
			edges[e.ID] = e
		}
		if err := eRows.Err(); err != nil {
			return nil, err
		}
	}

	// Reconstruct paths
	var paths []Path
	seen := make(map[string]bool)
	for _, r := range rowsData {
		nodeSeq := parseIDList(r.PathIDs)
		if len(nodeSeq) < 2 {
			continue
		}
		edgeSeq := parseIDList(r.PathEdgeIDs)
		key := r.PathIDs + "|" + r.PathEdgeIDs
		if seen[key] {
			continue
		}
		seen[key] = true

		path := Path{
			Nodes: make([]Node, 0, len(nodeSeq)),
			Edges: make([]Edge, 0, len(edgeSeq)),
		}

		for _, id := range nodeSeq {
			if n, ok := nodes[id]; ok {
				path.Nodes = append(path.Nodes, n)
			}
		}
		for _, edgeID := range edgeSeq {
			if e, ok := edges[edgeID]; ok {
				path.Edges = append(path.Edges, e)
				path.TotalWeight += e.Weight
			}
		}
		paths = append(paths, path)
	}

	return paths, nil
}

func parseIDList(csv string) []int64 {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// FindShortestPath finds the shortest path between two nodes using BFS CTE.
func (db *DB) FindShortestPath(fromID, toID int64) (*Path, error) {
	if fromID == toID {
		n, err := db.GetNode(fromID)
		if err != nil {
			return nil, err
		}
		return &Path{
			Nodes: []Node{*n},
			Edges: []Edge{},
		}, nil
	}

	cte := `
	WITH RECURSIVE bfs(id, source_id, target_id, relationship, weight, depth, path_ids) AS (
		SELECT id, source_id, target_id, relationship, weight, 1, 
			cast(source_id as text) || ',' || cast(target_id as text)
		FROM graph_edges
		WHERE source_id = ?
		
		UNION ALL
		
		SELECT e.id, e.source_id, e.target_id, e.relationship, e.weight, b.depth + 1, 
			b.path_ids || ',' || cast(e.target_id as text)
		FROM graph_edges e
		JOIN bfs b ON e.source_id = b.target_id
		WHERE b.depth < 10
		AND instr(',' || b.path_ids || ',', ',' || cast(e.target_id as text) || ',') = 0
	)
	SELECT path_ids FROM bfs WHERE target_id = ? ORDER BY depth ASC LIMIT 1`

	var pathIDs string
	err := db.conn.QueryRow(cte, fromID, toID).Scan(&pathIDs)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No path
		}
		return nil, err
	}

	// Reconstruct path
	ids := parseIDList(pathIDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("invalid shortest-path IDs")
	}
	path := &Path{
		Nodes: make([]Node, 0, len(ids)),
	}

	for _, id := range ids {
		n, err := db.GetNode(id)
		if err != nil {
			return nil, err
		}
		path.Nodes = append(path.Nodes, *n)
	}

	// Populate edges between nodes
	for i := 0; i < len(path.Nodes)-1; i++ {
		// Find edge between i and i+1
		// This is an extra query per step, but fine for single shortest path
		var e Edge
		err := db.conn.QueryRow(`
			SELECT id, source_id, target_id, relationship, weight, properties, created_at
			FROM graph_edges WHERE source_id = ? AND target_id = ?`,
			path.Nodes[i].ID, path.Nodes[i+1].ID).Scan(
			&e.ID, &e.SourceID, &e.TargetID, &e.Relationship, &e.Weight, &e.Properties, &e.CreatedAt,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, err
		}
		path.Edges = append(path.Edges, e)
		path.TotalWeight += e.Weight
	}

	return path, nil
}

// GetSubgraph returns a subgraph centered on nodeID up to depth.
func (db *DB) GetSubgraph(nodeID int64, depth int) (*Subgraph, error) {
	if depth <= 0 {
		depth = 1
	}
	if depth > 3 {
		depth = 3 // Limit for subgraph
	}

	// CTE to get all nodes and edges in range
	cte := `
	WITH RECURSIVE subgraph(id, source_id, target_id, relationship, weight, depth) AS (
		SELECT id, source_id, target_id, relationship, weight, 1
		FROM graph_edges
		WHERE source_id = ? OR target_id = ?
		
		UNION
		
		SELECT e.id, e.source_id, e.target_id, e.relationship, e.weight, s.depth + 1
		FROM graph_edges e
		JOIN subgraph s ON (s.target_id = e.source_id OR s.source_id = e.target_id)
		WHERE s.depth < ?
	)
	SELECT DISTINCT id, source_id, target_id, relationship, weight FROM subgraph`

	rows, err := db.conn.Query(cte, nodeID, nodeID, depth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sub := &Subgraph{}
	nodeIDs := make(map[int64]bool)
	nodeIDs[nodeID] = true

	for rows.Next() {
		var e Edge
		// We don't fetch created_at/properties in CTE for brevity, let's just fetch basic edge info
		// To match Edge struct, we might need full info.
		// Let's assume CTE selects IDs and we fetch full objects?
		// Or simpler: just populate what we have.
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.Relationship, &e.Weight); err != nil {
			return nil, err
		}
		sub.Edges = append(sub.Edges, e)
		nodeIDs[e.SourceID] = true
		nodeIDs[e.TargetID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch all nodes
	ids := make([]string, 0, len(nodeIDs))
	args := make([]interface{}, 0, len(nodeIDs))
	for id := range nodeIDs {
		ids = append(ids, "?")
		args = append(args, id)
	}

	if len(ids) > 0 {
		q := "SELECT id, type, name, note_id, properties, created_at FROM graph_nodes WHERE id IN (" + strings.Join(ids, ",") + ")"
		nRows, err := db.conn.Query(q, args...)
		if err != nil {
			return nil, err
		}
		defer nRows.Close()
		for nRows.Next() {
			var n Node
			if err := nRows.Scan(&n.ID, &n.Type, &n.Name, &n.NoteID, &n.Properties, &n.CreatedAt); err != nil {
				return nil, err
			}
			sub.Nodes = append(sub.Nodes, n)
		}
		if err := nRows.Err(); err != nil {
			return nil, err
		}
	}

	return sub, nil
}

// GetStats returns graph statistics.
func (db *DB) GetStats() (*Stats, error) {
	s := &Stats{
		NodesByType:         make(map[string]int),
		EdgesByRelationship: make(map[string]int),
	}

	if err := db.conn.QueryRow("SELECT COUNT(*) FROM graph_nodes").Scan(&s.TotalNodes); err != nil {
		return nil, err
	}
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM graph_edges").Scan(&s.TotalEdges); err != nil {
		return nil, err
	}

	// Nodes by type
	rows, err := db.conn.Query("SELECT type, COUNT(*) FROM graph_nodes GROUP BY type")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		s.NodesByType[t] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Edges by relationship
	rows2, err := db.conn.Query("SELECT relationship, COUNT(*) FROM graph_edges GROUP BY relationship")
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var r string
		var c int
		if err := rows2.Scan(&r, &c); err != nil {
			return nil, err
		}
		s.EdgesByRelationship[r] = c
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	if s.TotalNodes > 0 {
		// Average degree = 2 * edges / nodes
		s.AvgDegree = 2.0 * float64(s.TotalEdges) / float64(s.TotalNodes)
	}

	return s, nil
}

// DeleteNodeByName deletes a node and its edges.
func (db *DB) DeleteNodeByName(nodeType, name string) error {
	// Foreign key CASCADE handles edges
	_, err := db.conn.Exec("DELETE FROM graph_nodes WHERE type = ? AND name = ?", nodeType, name)
	return err
}

// DeleteEdgesForNode deletes all edges connected to a node.
func (db *DB) DeleteEdgesForNode(nodeID int64) error {
	_, err := db.conn.Exec("DELETE FROM graph_edges WHERE source_id = ? OR target_id = ?", nodeID, nodeID)
	return err
}
