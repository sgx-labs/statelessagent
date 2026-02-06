package store

import (
	"encoding/json"
	"fmt"
	"sort"
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
		WHERE n.chunk_id = 0 AND n.path NOT LIKE '_PRIVATE/%%' AND n.path IN (
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

// ContentTermSearch finds notes where a minimum number of search terms
// appear across ANY chunk. Unlike KeywordSearch (which ranks by chunk_id=0
// match count), this function counts distinct terms across all chunks,
// correctly finding notes where terms appear in later sections.
// Returns chunk_id=0 data for matching notes, ranked by term coverage
// (highest first), then recency as tiebreaker.
func (db *DB) ContentTermSearch(terms []string, minTerms int, limit int) ([]RawSearchResult, error) {
	if len(terms) == 0 || limit <= 0 || minTerms <= 0 {
		return nil, nil
	}

	// Build per-term coverage expressions that check across all chunks.
	// Each expression evaluates to 1 if ANY chunk of the note contains
	// the term, 0 otherwise.
	// Also build chunk-frequency expressions for content density ranking.
	// Content relevance = chunk_freq^2 / chunk_count. This rewards notes
	// that are genuinely ABOUT the topic (high frequency AND high density)
	// over notes that are simply long (high frequency, low density) or
	// short (high density from one mention).
	var coverageExprs []string
	var freqExprs []string
	var covArgs []interface{}
	var freqArgs []interface{}
	for _, term := range terms {
		pattern := "%" + term + "%"
		coverageExprs = append(coverageExprs,
			"(CASE WHEN SUM(CASE WHEN LOWER(n2.title) LIKE LOWER(?) OR LOWER(n2.text) LIKE LOWER(?) THEN 1 ELSE 0 END) > 0 THEN 1 ELSE 0 END)")
		covArgs = append(covArgs, pattern, pattern)
		freqExprs = append(freqExprs,
			"SUM(CASE WHEN LOWER(n2.text) LIKE LOWER(?) THEN 1 ELSE 0 END)")
		freqArgs = append(freqArgs, pattern)
	}
	coverageExpr := strings.Join(coverageExprs, " + ")
	freqExpr := strings.Join(freqExprs, " + ")

	// Args order: coverage args, freq args, minTerms, limit
	var args []interface{}
	args = append(args, covArgs...)
	args = append(args, freqArgs...)
	args = append(args, minTerms, limit)

	query := fmt.Sprintf(`
		WITH note_coverage AS (
			SELECT n2.path, (%s) as cov,
				(%s) as chunk_freq, COUNT(*) as chunk_count
			FROM vault_notes n2
			GROUP BY n2.path
		)
		SELECT 0 as distance, n.id, n.path, n.title, n.chunk_heading, n.text,
			n.domain, n.workstream, n.tags, n.content_type, n.confidence, n.modified
		FROM vault_notes n
		JOIN note_coverage nc ON n.path = nc.path
		WHERE n.chunk_id = 0 AND nc.cov >= ?
		ORDER BY nc.cov DESC,
			CAST(nc.chunk_freq * nc.chunk_freq AS REAL) / nc.chunk_count DESC,
			n.modified DESC
		LIMIT ?`,
		coverageExpr, freqExpr)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("content term search: %w", err)
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

// KeywordSearchTitleMatch performs a keyword search on note titles (and
// optionally paths), requiring at least minMatches terms to appear.
// When titleOnly is true, only n.title is checked (more precise, avoids
// false positives from folder names). When false, both n.title and n.path
// are checked (catches folder-organized content like "01_Projects/SAME Security Audit/").
func (db *DB) KeywordSearchTitleMatch(terms []string, minMatches int, limit int, titleOnly ...bool) ([]RawSearchResult, error) {
	if len(terms) == 0 || limit <= 0 || minMatches <= 0 {
		return nil, nil
	}

	onlyTitle := len(titleOnly) > 0 && titleOnly[0]

	var matchExprs []string
	var args []interface{}
	for _, term := range terms {
		pattern := "%" + term + "%"
		if onlyTitle {
			matchExprs = append(matchExprs,
				"(CASE WHEN LOWER(n.title) LIKE LOWER(?) THEN 1 ELSE 0 END)")
			args = append(args, pattern)
		} else {
			matchExprs = append(matchExprs,
				"(CASE WHEN LOWER(n.title) LIKE LOWER(?) OR LOWER(n.path) LIKE LOWER(?) THEN 1 ELSE 0 END)")
			args = append(args, pattern, pattern)
		}
	}
	scoreExpr := strings.Join(matchExprs, " + ")

	// scoreExpr appears twice (WHERE + ORDER BY), so build args in correct
	// order: [WHERE match args..., minMatches, ORDER BY match args..., limit]
	matchArgs := make([]interface{}, len(args))
	copy(matchArgs, args)

	var finalArgs []interface{}
	finalArgs = append(finalArgs, args...)      // WHERE match args
	finalArgs = append(finalArgs, minMatches)   // >= ?
	finalArgs = append(finalArgs, matchArgs...) // ORDER BY match args
	finalArgs = append(finalArgs, limit)        // LIMIT ?

	query := fmt.Sprintf(`
		SELECT 0 as distance, n.id, n.path, n.title, n.chunk_heading, n.text,
			n.domain, n.workstream, n.tags, n.content_type, n.confidence, n.modified
		FROM vault_notes n
		WHERE n.chunk_id = 0 AND n.path NOT LIKE '_PRIVATE/%%' AND (%s) >= ?
		ORDER BY (%s) DESC, n.modified DESC
		LIMIT ?`,
		scoreExpr, scoreExpr)

	rows, err := db.conn.Query(query, finalArgs...)
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

// HybridSearch combines vector KNN search with keyword title matching.
// Vector results fill most of TopK; keyword-only results are scored by
// term coverage and interleaved by score so strong title matches rank high.
func (db *DB) HybridSearch(queryVec []float32, queryText string, opts SearchOptions) ([]SearchResult, error) {
	// 1. Vector search (primary)
	vectorResults, err := db.VectorSearch(queryVec, opts)
	if err != nil {
		return nil, err
	}

	// 2. Keyword title search (supplemental)
	terms := ExtractSearchTerms(queryText)

	var kwResults []RawSearchResult
	if len(terms) > 0 {
		kwResults, _ = db.KeywordSearchTitleMatch(terms, 1, opts.TopK*2, true)
	}

	// Steps 3-8: Merge keyword results into vector results.
	// Wrapped in a block so that step 9 (post-processing) always runs.
	merged := vectorResults
	if len(kwResults) > 0 {
		// Build terms set (used for both vector boost and keyword scoring).
		termsSet := make(map[string]bool, len(terms))
		for _, t := range terms {
			termsSet[t] = true
		}

		// 3. Build keyword path->score map for score fusion.
		// When a vector result also appears in keyword results, we boost
		// its score using the keyword signal so it ranks higher in the
		// final sort.
		kwPathScore := make(map[string]float64, len(kwResults))
		for _, r := range kwResults {
			titleLower := strings.ToLower(r.Title)
			score := keywordTitleScore(titleLower, terms, termsSet)
			if existing, ok := kwPathScore[r.Path]; !ok || score > existing {
				kwPathScore[r.Path] = score
			}
		}

		// Fuse keyword score into vector results additively, but only when
		// the keyword match is strong (score >= 0.7, meaning most query terms
		// appear in the title). Weak matches (few terms) tend to be noise
		// and cause regressions when boosted.
		for i, r := range merged {
			if kwScore, ok := kwPathScore[r.Path]; ok && kwScore >= 0.7 {
				merged[i].Score = round3(r.Score + 0.5*kwScore)
			}
		}

		// 4. Cut vector results to make room for keyword additions.
		// Reserve up to 30% of TopK (min 2) for keyword results.
		maxReplace := (opts.TopK*3 + 9) / 10 // ceil(topK * 0.3)
		if maxReplace < 2 {
			maxReplace = 2
		}
		if len(merged) >= opts.TopK {
			cutAt := len(merged) - maxReplace
			if cutAt < 0 {
				cutAt = 0
			}
			merged = merged[:cutAt]
		}

		// 5. Build seen map from REMAINING vector results (not pre-cut ones).
		seen := make(map[string]bool, len(merged))
		for _, r := range merged {
			seen[r.Path] = true
		}

		var newKW []SearchResult
		for _, r := range kwResults {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true

			snippet := r.Text
			if len(snippet) > 500 {
				snippet = snippet[:500]
			}

			titleLower := strings.ToLower(r.Title)
			score := keywordTitleScore(titleLower, terms, termsSet)

			newKW = append(newKW, SearchResult{
				Path:         r.Path,
				Title:        r.Title,
				ChunkHeading: r.Heading,
				Score:        score,
				Distance:     0,
				Snippet:      snippet,
				Domain:       r.Domain,
				Workstream:   r.Workstream,
				Tags:         r.Tags,
				ContentType:  r.ContentType,
				Confidence:   round3(r.Confidence),
			})
		}

		if len(newKW) > 0 {
			// 6. Sort keyword results by score so highest-value matches
			// (e.g. exact title matches at 0.95) get picked first when
			// we have more candidates than available slots.
			sort.Slice(newKW, func(i, j int) bool {
				return newKW[i].Score > newKW[j].Score
			})

			// 7. Merge keyword results into vector results (filling up to TopK).
			remaining := opts.TopK - len(merged)
			if remaining > len(newKW) {
				remaining = len(newKW)
			}
			full := make([]SearchResult, 0, opts.TopK)
			full = append(full, merged...)
			full = append(full, newKW[:remaining]...)
			merged = full
		}

		// 8. Sort merged results by score descending so high-confidence
		// keyword matches interleave with vector results instead of
		// being stuck at the bottom.
		sort.Slice(merged, func(i, j int) bool {
			return merged[i].Score > merged[j].Score
		})

		// 8b. If slots remain unfilled, try fuzzy title matching
		// to catch single-character typos/omissions.
		filled := len(merged)
		if filled < opts.TopK {
			fuzzyResults, _ := db.FuzzyTitleSearch(terms, opts.TopK*2)
			for _, r := range fuzzyResults {
				if filled >= opts.TopK {
					break
				}
				if seen[r.Path] {
					continue
				}
				seen[r.Path] = true

				snippet := r.Text
				if len(snippet) > 500 {
					snippet = snippet[:500]
				}

				merged = append(merged, SearchResult{
					Path:         r.Path,
					Title:        r.Title,
					ChunkHeading: r.Heading,
					Score:        0.4,
					Distance:     0,
					Snippet:      snippet,
					Domain:       r.Domain,
					Workstream:   r.Workstream,
					Tags:         r.Tags,
					ContentType:  r.ContentType,
					Confidence:   round3(r.Confidence),
				})
				filled++
			}
		}
	}

	// 9. Post-process: apply title-overlap-aware ranking.
	// Uses bidirectional term overlap to re-sort results so that
	// title-relevant notes rank above vector-only semantic matches.
	// Also filters /raw_outputs/ noise and near-dedups versioned files.
	// This step ALWAYS runs, regardless of whether keyword results
	// were merged, to ensure filtering and dedup apply to all results.
	queryTerms := QueryWordsForTitleMatch(queryText)
	if len(queryTerms) > 0 {
		merged = RankSearchResults(merged, queryTerms)
	}

	return merged, nil
}

// keywordTitleScore computes a score for a keyword result based on how many
// search terms appear in its title. Exact title matches get the highest score.
// Scores are calibrated to interleave well with vector results (which range 0-1).
func keywordTitleScore(titleLower string, terms []string, termsSet map[string]bool) float64 {
	trimmed := strings.TrimSpace(titleLower)

	// Exact title match -> score just below top vector result
	if termsSet[trimmed] {
		return 0.95
	}

	// Count how many search terms appear in the title
	matchCount := 0
	for _, t := range terms {
		if strings.Contains(titleLower, t) {
			matchCount++
		}
	}

	if matchCount == 0 {
		return 0.5
	}

	// Score range: 0.55 (1 of many) to 0.85 (all terms match)
	fraction := float64(matchCount) / float64(len(terms))
	return round3(0.5 + 0.35*fraction)
}

// FuzzyTitleSearch finds notes whose titles contain words within edit distance 1
// of any search term. Only considers terms >= 5 chars to avoid false positives.
func (db *DB) FuzzyTitleSearch(terms []string, limit int) ([]RawSearchResult, error) {
	// Filter to terms long enough for meaningful fuzzy matching
	var fuzzyTerms []string
	for _, t := range terms {
		if len(t) >= 5 {
			fuzzyTerms = append(fuzzyTerms, strings.ToLower(t))
		}
	}
	if len(fuzzyTerms) == 0 || limit <= 0 {
		return nil, nil
	}

	rows, err := db.conn.Query(`
		SELECT 0 as distance, n.id, n.path, n.title, n.chunk_heading, n.text,
			n.domain, n.workstream, n.tags, n.content_type, n.confidence, n.modified
		FROM vault_notes n WHERE n.chunk_id = 0
		ORDER BY n.modified DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RawSearchResult
	for rows.Next() {
		var r RawSearchResult
		if err := rows.Scan(
			&r.Distance, &r.NoteID, &r.Path, &r.Title, &r.Heading, &r.Text,
			&r.Domain, &r.Workstream, &r.Tags, &r.ContentType, &r.Confidence, &r.Modified,
		); err != nil {
			continue
		}

		titleLower := strings.ToLower(r.Title)
		titleWords := splitTitleWords(titleLower)
		for _, term := range fuzzyTerms {
			matched := false
			for _, word := range titleWords {
				if editDistance1(term, word) {
					matched = true
					break
				}
			}
			if matched {
				results = append(results, r)
				break
			}
		}
		if len(results) >= limit {
			break
		}
	}
	return results, rows.Err()
}

// splitTitleWords splits a title into lowercase words, treating common
// punctuation as separators.
func splitTitleWords(title string) []string {
	f := func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '(' || r == ')' ||
			r == ',' || r == '.' || r == '/' || r == ':' || r == '\u2014' || r == '&'
	}
	return strings.FieldsFunc(title, f)
}

// editDistance1 checks if two strings have edit distance exactly 1
// (one substitution, insertion, or deletion).
func editDistance1(a, b string) bool {
	la, lb := len(a), len(b)
	if la == lb {
		diffs := 0
		for i := range a {
			if a[i] != b[i] {
				diffs++
			}
			if diffs > 1 {
				return false
			}
		}
		return diffs == 1
	}
	if la == lb+1 {
		return canDeleteOne(a, b)
	}
	if la+1 == lb {
		return canDeleteOne(b, a)
	}
	return false
}

// canDeleteOne checks if removing one character from longer produces shorter.
func canDeleteOne(longer, shorter string) bool {
	i, j := 0, 0
	skipped := false
	for i < len(longer) && j < len(shorter) {
		if longer[i] == shorter[j] {
			i++
			j++
		} else if !skipped {
			skipped = true
			i++
		} else {
			return false
		}
	}
	return true
}

// searchStopWords are common English words filtered from keyword search terms.
var searchStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "could": true, "should": true,
	"may": true, "might": true, "shall": true, "can": true,
	"of": true, "in": true, "to": true, "for": true, "with": true,
	"on": true, "at": true, "from": true, "by": true, "about": true,
	"as": true, "into": true, "through": true, "during": true,
	"and": true, "or": true, "but": true, "not": true, "so": true,
	"what": true, "how": true, "when": true, "where": true, "which": true,
	"who": true, "whom": true, "this": true, "that": true, "these": true,
	"those": true, "it": true, "its": true, "my": true, "your": true,
	"our": true, "their": true, "i": true, "me": true, "we": true,
	"you": true, "he": true, "she": true, "they": true, "them": true,
	"explain": true, "describe": true, "tell": true, "show": true,
	"work": true, "works": true, "tracked": true, "area": true,
	"project": true, "help": true, "find": true, "search": true,
}

// meaningfulShortTerms are 2-character terms that carry domain meaning.
var meaningfulShortTerms = map[string]bool{
	"ai": true, "os": true, "pm": true, "qa": true,
	"ui": true, "ux": true, "hr": true, "ml": true,
}

// ExtractSearchTerms extracts meaningful search terms from a natural language
// query, filtering stop words and short terms. Exported for use by MCP and CLI.
func ExtractSearchTerms(query string) []string {
	words := strings.Fields(query)
	var terms []string
	seen := make(map[string]bool)
	for _, w := range words {
		lower := strings.ToLower(w)
		lower = strings.Trim(lower, ".,;:!?\"'()[]{}")
		if len(lower) < 2 {
			continue
		}
		if len(lower) == 2 && !meaningfulShortTerms[lower] {
			continue
		}
		if searchStopWords[lower] || seen[lower] {
			continue
		}
		seen[lower] = true
		terms = append(terms, lower)
	}
	return terms
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
