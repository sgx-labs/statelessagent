package store

import (
	"encoding/json"
	"fmt"
	"strings"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

// SearchResult represents a single search result with scoring.
type SearchResult struct {
	Path         string  `json:"path"`
	Title        string  `json:"title"`
	ChunkHeading string  `json:"chunk_heading"`
	Score        float64 `json:"score"`
	Distance     float64 `json:"distance"`
	Snippet      string  `json:"snippet"`
	Domain       string  `json:"domain"`
	Workstream   string  `json:"workstream"`
	Tags         string  `json:"tags"`
	ContentType  string  `json:"content_type,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
}

// SearchOptions configures a vector search.
type SearchOptions struct {
	TopK       int
	Domain     string
	Workstream string
	Tags       []string
}

// VectorSearch performs a KNN vector search with optional metadata filtering
// and per-path deduplication.
func (db *DB) VectorSearch(queryVec []float32, opts SearchOptions) ([]SearchResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if opts.TopK > 100 {
		opts.TopK = 100
	}

	vecData, err := sqlite_vec.SerializeFloat32(queryVec)
	if err != nil {
		return nil, fmt.Errorf("serialize query: %w", err)
	}

	// Fetch extra results for deduplication and filtering
	fetchK := opts.TopK * 5

	rows, err := db.conn.Query(`
		SELECT v.distance, n.id, n.path, n.title, n.chunk_heading, n.text,
			n.domain, n.workstream, n.tags, n.content_type, n.confidence, n.modified
		FROM vault_notes_vec v
		JOIN vault_notes n ON n.id = v.note_id
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance`,
		vecData, fetchK,
	)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	type rawResult struct {
		distance    float64
		id          int64
		path        string
		title       string
		heading     string
		text        string
		domain      string
		workstream  string
		tags        string
		contentType string
		confidence  float64
		modified    float64
	}

	var raw []rawResult
	for rows.Next() {
		var r rawResult
		if err := rows.Scan(
			&r.distance, &r.id, &r.path, &r.title, &r.heading, &r.text,
			&r.domain, &r.workstream, &r.tags, &r.contentType, &r.confidence, &r.modified,
		); err != nil {
			return nil, err
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Apply metadata filters
	filtered := raw[:0]
	for _, r := range raw {
		if opts.Domain != "" && !strings.EqualFold(r.domain, opts.Domain) {
			continue
		}
		if opts.Workstream != "" && !strings.EqualFold(r.workstream, opts.Workstream) {
			continue
		}
		if len(opts.Tags) > 0 && !hasTags(r.tags, opts.Tags) {
			continue
		}
		filtered = append(filtered, r)
	}

	// Deduplicate by path (keep best-scoring chunk per note)
	seen := make(map[string]bool)
	var deduped []rawResult
	for _, r := range filtered {
		if seen[r.path] {
			continue
		}
		seen[r.path] = true
		deduped = append(deduped, r)
		if len(deduped) >= opts.TopK {
			break
		}
	}

	if len(deduped) == 0 {
		return nil, nil
	}

	// Normalize distances to 0-1 scores
	minDist := deduped[0].distance
	maxDist := deduped[len(deduped)-1].distance
	distRange := maxDist - minDist
	if distRange <= 0 {
		distRange = 1.0
	}

	results := make([]SearchResult, 0, len(deduped))
	for _, r := range deduped {
		score := 1.0 - ((r.distance - minDist) / distRange)

		snippet := r.text
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}

		results = append(results, SearchResult{
			Path:         r.path,
			Title:        r.title,
			ChunkHeading: r.heading,
			Score:        round3(score),
			Distance:     round1(r.distance),
			Snippet:      snippet,
			Domain:       r.domain,
			Workstream:   r.workstream,
			Tags:         r.tags,
			ContentType:  r.contentType,
			Confidence:   round3(r.confidence),
		})
	}

	return results, nil
}

// VectorSearchRaw returns raw results with full metadata for composite scoring.
// Does not normalize scores — caller is responsible for scoring.
type RawSearchResult struct {
	NoteID      int64
	Distance    float64
	Path        string
	Title       string
	Heading     string
	Text        string
	Domain      string
	Workstream  string
	Tags        string
	ContentType string
	Confidence  float64
	Modified    float64
}

// VectorSearchRaw performs a raw vector search without score normalization.
func (db *DB) VectorSearchRaw(queryVec []float32, fetchK int) ([]RawSearchResult, error) {
	vecData, err := sqlite_vec.SerializeFloat32(queryVec)
	if err != nil {
		return nil, fmt.Errorf("serialize query: %w", err)
	}

	rows, err := db.conn.Query(`
		SELECT v.distance, n.id, n.path, n.title, n.chunk_heading, n.text,
			n.domain, n.workstream, n.tags, n.content_type, n.confidence, n.modified
		FROM vault_notes_vec v
		JOIN vault_notes n ON n.id = v.note_id
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance`,
		vecData, fetchK,
	)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var results []RawSearchResult
	for rows.Next() {
		var r RawSearchResult
		if err := rows.Scan(
			&r.Distance, &r.NoteID, &r.Path, &r.Title, &r.Heading, &r.Text,
			&r.Domain, &r.Workstream, &r.Tags, &r.ContentType, &r.Confidence, &r.Modified,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func hasTags(tagsJSON string, required []string) bool {
	var noteTags []string
	if err := json.Unmarshal([]byte(tagsJSON), &noteTags); err != nil {
		return false
	}
	noteTagsLower := make(map[string]bool, len(noteTags))
	for _, t := range noteTags {
		noteTagsLower[strings.ToLower(t)] = true
	}
	for _, req := range required {
		if noteTagsLower[strings.ToLower(req)] {
			return true
		}
	}
	return false
}

// KeywordSearch performs a SQL LIKE search on title and text fields.
// Uses OR between terms and ranks by match count. Used as a fallback when
// vector search misses exact-term queries.
func (db *DB) KeywordSearch(terms []string, limit int) ([]RawSearchResult, error) {
	if len(terms) == 0 || limit <= 0 {
		return nil, nil
	}

	// Build a score expression: count how many terms match in title or text
	var matchExprs []string
	var args []interface{}
	for _, term := range terms {
		pattern := "%" + term + "%"
		matchExprs = append(matchExprs,
			"(CASE WHEN LOWER(n.title) LIKE LOWER(?) OR LOWER(n.text) LIKE LOWER(?) THEN 1 ELSE 0 END)")
		args = append(args, pattern, pattern)
	}

	// Build OR conditions: at least one term must match
	var conditions []string
	for _, term := range terms {
		pattern := "%" + term + "%"
		conditions = append(conditions, "(LOWER(n.title) LIKE LOWER(?) OR LOWER(n.text) LIKE LOWER(?))")
		args = append(args, pattern, pattern)
	}

	scoreExpr := strings.Join(matchExprs, " + ")

	query := fmt.Sprintf(`
		SELECT 0 as distance, n.id, n.path, n.title, n.chunk_heading, n.text,
			n.domain, n.workstream, n.tags, n.content_type, n.confidence, n.modified
		FROM vault_notes n
		WHERE n.chunk_id = 0 AND n.path IN (
			SELECT DISTINCT n2.path FROM vault_notes n2
			WHERE (%s)
		)
		ORDER BY (%s) DESC, n.modified DESC
		LIMIT ?`,
		strings.Join(conditions, " OR "),
		scoreExpr)
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()

	var results []RawSearchResult
	for rows.Next() {
		var r RawSearchResult
		if err := rows.Scan(
			&r.Distance, &r.NoteID, &r.Path, &r.Title, &r.Heading, &r.Text,
			&r.Domain, &r.Workstream, &r.Tags, &r.ContentType, &r.Confidence, &r.Modified,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// KeywordSearchTitleMatch performs a keyword search on note titles only,
// requiring at least minMatches terms to appear in the title. This is the
// most conservative keyword fallback — used when only broad (generic) terms
// are available and vector search returned nothing.
func (db *DB) KeywordSearchTitleMatch(terms []string, minMatches int, limit int) ([]RawSearchResult, error) {
	if len(terms) == 0 || limit <= 0 || minMatches <= 0 {
		return nil, nil
	}

	var matchExprs []string
	var args []interface{}
	for _, term := range terms {
		pattern := "%" + term + "%"
		matchExprs = append(matchExprs,
			"(CASE WHEN LOWER(n.title) LIKE LOWER(?) OR LOWER(n.path) LIKE LOWER(?) THEN 1 ELSE 0 END)")
		args = append(args, pattern, pattern)
	}
	scoreExpr := strings.Join(matchExprs, " + ")

	query := fmt.Sprintf(`
		SELECT 0 as distance, n.id, n.path, n.title, n.chunk_heading, n.text,
			n.domain, n.workstream, n.tags, n.content_type, n.confidence, n.modified
		FROM vault_notes n
		WHERE n.chunk_id = 0 AND (%s) >= ?
		ORDER BY n.modified DESC
		LIMIT ?`,
		scoreExpr)
	args = append(args, minMatches, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword title search: %w", err)
	}
	defer rows.Close()

	var results []RawSearchResult
	for rows.Next() {
		var r RawSearchResult
		if err := rows.Scan(
			&r.Distance, &r.NoteID, &r.Path, &r.Title, &r.Heading, &r.Text,
			&r.Domain, &r.Workstream, &r.Tags, &r.ContentType, &r.Confidence, &r.Modified,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
