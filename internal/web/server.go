// Package web provides a local read-only web dashboard for SAME vaults.
package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/graph"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// Serve starts the web server on the given address.
// embedClient may be nil if no embedding provider is available (keyword-only mode).
// vaultPath is the resolved vault directory, shown in the dashboard for orientation.
func Serve(addr string, embedClient embedding.Provider, version string, vaultPath string) error {
	db, err := store.Open()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	s := &server{
		db:          db,
		embedClient: embedClient,
		version:     version,
		vaultPath:   vaultPath,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/notes/recent", s.handleRecentNotes)
	mux.HandleFunc("/api/notes/", s.handleNoteByPath) // /api/notes/{path}
	mux.HandleFunc("/api/notes", s.handleAllNotes)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/pinned", s.handlePinned)
	mux.HandleFunc("/api/related/", s.handleRelated) // /api/related/{path}
	mux.HandleFunc("/api/graph/stats", s.handleGraphStats)
	mux.HandleFunc("/api/graph/connections/", s.handleGraphConnections) // /api/graph/connections/{path}

	handler := localhostOnly(securityHeaders(mux))

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	fmt.Fprintf(os.Stderr, "SAME web dashboard: http://%s\n", listener.Addr())
	return http.Serve(listener, handler)
}

type server struct {
	db          *store.DB
	embedClient embedding.Provider
	version     string
	vaultPath   string
}

// --- Middleware ---

func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		host = strings.Trim(host, "[]") // strip IPv6 brackets

		if host == "localhost" {
			next.ServeHTTP(w, r)
			return
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

// --- Handlers ---

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	noteCount, _ := s.db.NoteCount()
	chunkCount, _ := s.db.ChunkCount()

	searchMode := "keyword"
	if s.embedClient != nil && s.db.HasVectors() {
		searchMode = "semantic"
	}

	dbSize := int64(0)
	if info, err := os.Stat(config.DBPath()); err == nil {
		dbSize = info.Size()
	}

	indexAge := ""
	if age, err := s.db.IndexAge(); err == nil && age > 0 {
		indexAge = age.String()
	}

	// Show just the vault directory name for display, full path for tooltip
	vaultName := filepath.Base(s.vaultPath)

	writeJSON(w, map[string]any{
		"note_count":  noteCount,
		"chunk_count": chunkCount,
		"search_mode": searchMode,
		"db_size":     dbSize,
		"index_age":   indexAge,
		"version":     s.version,
		"vault_name":  vaultName,
		"vault_path":  s.vaultPath,
	})
}

func (s *server) handleRecentNotes(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	notes, err := s.db.RecentNotes(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, filterPrivateNotes(notes))
}

func (s *server) handleAllNotes(w http.ResponseWriter, r *http.Request) {
	notes, err := s.db.AllNotes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, filterPrivateNotes(notes))
}

// maxNoteSize caps the total text returned for a single note (5 MB).
const maxNoteSize = 5 * 1024 * 1024

func (s *server) handleNoteByPath(w http.ResponseWriter, r *http.Request) {
	// Extract path after /api/notes/
	raw := strings.TrimPrefix(r.URL.Path, "/api/notes/")
	if raw == "" {
		s.handleAllNotes(w, r)
		return
	}

	// URL-decode once, then normalize
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path encoding")
		return
	}
	clean := filepath.ToSlash(filepath.Clean(decoded))

	// Security: block path traversal and private/hidden paths
	if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, ".") || strings.Contains(clean, "/..") {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	if isPrivatePath(clean) {
		http.NotFound(w, r)
		return
	}

	chunks, err := s.db.GetNoteByPath(clean)
	if err != nil || len(chunks) == 0 {
		http.NotFound(w, r)
		return
	}

	// Join all chunk texts with size cap
	var texts []string
	total := 0
	for _, c := range chunks {
		if total+len(c.Text) > maxNoteSize {
			break
		}
		texts = append(texts, c.Text)
		total += len(c.Text)
	}

	first := chunks[0]
	writeJSON(w, map[string]any{
		"path":         first.Path,
		"title":        first.Title,
		"tags":         first.Tags,
		"domain":       first.Domain,
		"workstream":   first.Workstream,
		"agent":        first.Agent,
		"content_type": first.ContentType,
		"modified":     first.Modified,
		"text":         strings.Join(texts, "\n\n"),
	})
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" || len(query) > 10000 {
		writeError(w, http.StatusBadRequest, "missing or oversized query")
		return
	}

	topK := 10
	if v := r.URL.Query().Get("top_k"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			topK = n
		}
	}
	domain := r.URL.Query().Get("domain")

	opts := store.SearchOptions{TopK: topK, Domain: domain}
	var results []store.SearchResult
	var mode string

	// Search fallback chain (matches MCP server pattern)
	if s.embedClient != nil && s.db.HasVectors() {
		queryVec, err := s.embedClient.GetQueryEmbedding(query)
		if err == nil {
			results, err = s.db.HybridSearch(queryVec, query, opts)
			if err == nil {
				mode = "semantic"
			}
		}
	}

	// Fallback to FTS5
	if results == nil && s.db.FTSAvailable() {
		var err error
		results, err = s.db.FTS5Search(query, opts)
		if err == nil {
			mode = "keyword"
		}
	}

	// Fallback to LIKE-based keyword search
	if results == nil {
		terms := store.ExtractSearchTerms(query)
		rawResults, err := s.db.KeywordSearch(terms, topK)
		if err == nil {
			mode = "keyword"
			for _, rr := range rawResults {
				snippet := rr.Text
				if len(snippet) > 500 {
					snippet = snippet[:500]
				}
				results = append(results, store.SearchResult{
					Path:        rr.Path,
					Title:       rr.Title,
					Snippet:     snippet,
					Domain:      rr.Domain,
					Workstream:  rr.Workstream,
					Agent:       rr.Agent,
					Tags:        rr.Tags,
					ContentType: rr.ContentType,
					Score:       0.5,
				})
			}
		}
	}

	// Filter private paths from results
	var filtered []store.SearchResult
	for _, r := range results {
		if !isPrivatePath(r.Path) {
			filtered = append(filtered, r)
		}
	}

	writeJSON(w, map[string]any{
		"results": filtered,
		"mode":    mode,
		"query":   query,
	})
}

func (s *server) handlePinned(w http.ResponseWriter, r *http.Request) {
	notes, err := s.db.GetPinnedNotes()
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, filterPrivateNotes(notes))
}

func (s *server) handleRelated(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/api/related/")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing path")
		return
	}

	decoded, err := url.PathUnescape(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path encoding")
		return
	}
	clean := filepath.ToSlash(filepath.Clean(decoded))

	if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, ".") || strings.Contains(clean, "/..") {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	if isPrivatePath(clean) {
		http.NotFound(w, r)
		return
	}

	if !s.db.HasVectors() {
		writeJSON(w, []store.SearchResult{})
		return
	}

	noteVec, err := s.db.GetNoteEmbedding(clean)
	if err != nil || noteVec == nil {
		writeJSON(w, []store.SearchResult{})
		return
	}

	results, err := s.db.VectorSearch(noteVec, store.SearchOptions{TopK: 8})
	if err != nil {
		writeJSON(w, []store.SearchResult{})
		return
	}

	var filtered []store.SearchResult
	for _, res := range results {
		if res.Path == clean || isPrivatePath(res.Path) {
			continue
		}
		filtered = append(filtered, res)
	}
	if len(filtered) > 5 {
		filtered = filtered[:5]
	}
	if filtered == nil {
		filtered = []store.SearchResult{}
	}
	writeJSON(w, filtered)
}

func (s *server) handleGraphStats(w http.ResponseWriter, r *http.Request) {
	gdb := graph.NewDB(s.db.Conn())
	stats, err := gdb.GetStats()
	if err != nil {
		if isNoSuchGraphTableErr(err) {
			writeJSON(w, map[string]any{
				"total_nodes":           0,
				"total_edges":           0,
				"avg_degree":            0,
				"nodes_by_type":         map[string]int{},
				"edges_by_relationship": map[string]int{},
				"top_relationships":     []map[string]any{},
				"available":             false,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "graph stats unavailable")
		return
	}

	top := make([]map[string]any, 0, len(stats.EdgesByRelationship))
	for rel, count := range stats.EdgesByRelationship {
		top = append(top, map[string]any{
			"relationship": rel,
			"count":        count,
		})
	}
	sort.Slice(top, func(i, j int) bool {
		ci, _ := top[i]["count"].(int)
		cj, _ := top[j]["count"].(int)
		if ci == cj {
			ri, _ := top[i]["relationship"].(string)
			rj, _ := top[j]["relationship"].(string)
			return ri < rj
		}
		return ci > cj
	})
	if len(top) > 5 {
		top = top[:5]
	}

	writeJSON(w, map[string]any{
		"total_nodes":           stats.TotalNodes,
		"total_edges":           stats.TotalEdges,
		"avg_degree":            stats.AvgDegree,
		"nodes_by_type":         stats.NodesByType,
		"edges_by_relationship": stats.EdgesByRelationship,
		"top_relationships":     top,
		"available":             true,
	})
}

func (s *server) handleGraphConnections(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/api/graph/connections/")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing path")
		return
	}

	decoded, err := url.PathUnescape(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path encoding")
		return
	}
	clean := filepath.ToSlash(filepath.Clean(decoded))

	if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, ".") || strings.Contains(clean, "/..") {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	if isPrivatePath(clean) {
		http.NotFound(w, r)
		return
	}

	depth := 2
	if v := r.URL.Query().Get("depth"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 1 || n > 5 {
			writeError(w, http.StatusBadRequest, "depth must be between 1 and 5")
			return
		}
		depth = n
	}

	direction := r.URL.Query().Get("dir")
	if direction == "" {
		direction = "forward"
	}
	if direction != "forward" && direction != "reverse" {
		writeError(w, http.StatusBadRequest, "dir must be forward or reverse")
		return
	}

	relationship := strings.TrimSpace(r.URL.Query().Get("rel"))
	if relationship != "" && !isGraphToken(relationship) {
		writeError(w, http.StatusBadRequest, "invalid relationship filter")
		return
	}

	gdb := graph.NewDB(s.db.Conn())
	startNode, err := resolveGraphNode(gdb, graph.NodeNote, clean)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, map[string]any{
				"start_path": clean,
				"count":      0,
				"paths":      []graph.Path{},
				"hint":       "No graph node found for this note. Run same reindex to extract graph links.",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "graph lookup failed")
		return
	}

	paths, err := gdb.QueryGraph(graph.QueryOptions{
		FromNodeID:   startNode.ID,
		Relationship: relationship,
		MaxDepth:     depth,
		Direction:    direction,
	})
	if err != nil {
		if isNoSuchGraphTableErr(err) {
			writeJSON(w, map[string]any{
				"start_path": clean,
				"count":      0,
				"paths":      []graph.Path{},
				"hint":       "Graph tables are unavailable in this vault. Run same reindex.",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "graph query failed")
		return
	}

	filtered := make([]graph.Path, 0, len(paths))
	for _, p := range paths {
		if graphPathContainsPrivateNode(p) {
			continue
		}
		filtered = append(filtered, p)
		if len(filtered) >= 60 {
			break
		}
	}

	writeJSON(w, map[string]any{
		"start_path":    clean,
		"start_node_id": startNode.ID,
		"count":         len(filtered),
		"depth":         depth,
		"direction":     direction,
		"relationship":  relationship,
		"paths":         filtered,
	})
}

// --- Helpers ---

func resolveGraphNode(gdb *graph.DB, nodeType, nodeName string) (*graph.Node, error) {
	node, err := gdb.FindNode(nodeType, nodeName)
	if err == nil {
		return node, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	switch nodeType {
	case graph.NodeNote:
		return gdb.FindNode(graph.NodeFile, nodeName)
	case graph.NodeFile:
		return gdb.FindNode(graph.NodeNote, nodeName)
	default:
		return nil, err
	}
}

func isGraphToken(v string) bool {
	if len(v) == 0 || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func graphPathContainsPrivateNode(p graph.Path) bool {
	for _, n := range p.Nodes {
		if n.Type != graph.NodeNote && n.Type != graph.NodeFile {
			continue
		}
		if isPrivatePath(filepath.ToSlash(n.Name)) {
			return true
		}
	}
	return false
}

func isNoSuchGraphTableErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: graph_")
}

func isPrivatePath(path string) bool {
	upper := strings.ToUpper(path)
	return strings.HasPrefix(upper, "_PRIVATE/") || strings.HasPrefix(upper, "_PRIVATE\\")
}

func filterPrivateNotes(notes []store.NoteRecord) []noteJSON {
	out := make([]noteJSON, 0, len(notes))
	for _, n := range notes {
		if isPrivatePath(n.Path) {
			continue
		}
		snippet := n.Text
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		out = append(out, noteJSON{
			Path:        n.Path,
			Title:       n.Title,
			Tags:        n.Tags,
			Domain:      n.Domain,
			Workstream:  n.Workstream,
			ContentType: n.ContentType,
			Modified:    n.Modified,
			Text:        snippet,
		})
	}
	return out
}

type noteJSON struct {
	Path        string  `json:"path"`
	Title       string  `json:"title"`
	Tags        string  `json:"tags,omitempty"`
	Domain      string  `json:"domain,omitempty"`
	Workstream  string  `json:"workstream,omitempty"`
	ContentType string  `json:"content_type,omitempty"`
	Modified    float64 `json:"modified"`
	Text        string  `json:"text,omitempty"`
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
