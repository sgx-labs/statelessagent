package hooks

import (
	"sort"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// standardSearch performs vector search with always-on title overlap matching
// and keyword fallback.
func standardSearch(db *store.DB, queryVec []float32) []scored {
	// Extract title-match terms (permissive: 3+ chars, alphanumeric)
	// and keyword terms (strict: 5+ chars alphabetic) separately.
	titleTerms := queryWordsForTitleMatch()
	specificTerms, broadTerms := extractKeyTerms()

	raw, err := db.VectorSearchRaw(queryVec, maxResults*6)
	vectorEmpty := err != nil || len(raw) == 0 || raw[0].Distance > maxDistance

	var candidates []scored
	seen := make(map[string]bool)

	if !vectorEmpty {
		deduped := dedup(raw)
		minDist, maxDist := distRange(deduped)
		dRange := maxDist - minDist
		if dRange <= 0 {
			dRange = 1.0
		}

		for _, r := range deduped {
			if r.Distance > maxDistance {
				continue
			}
			// SECURITY: never auto-surface _PRIVATE/ content
			if shouldSkipPath(r.Path) {
				continue
			}

			semScore := 1.0 - ((r.Distance - minDist) / dRange)
			if semScore < minSemanticFloor {
				continue
			}

			comp := memory.CompositeScore(semScore, r.Modified, r.Confidence, r.ContentType,
				0.3, 0.3, 0.4)
			if comp < minComposite {
				continue
			}

			s := makeScored(r, comp, semScore)
			s.titleOverlap = overlapForSort(titleTerms, r.Title, r.Path)

			candidates = append(candidates, s)
			seen[r.Path] = true
		}
	}

	// Mode 2 (name overlap): ALWAYS runs. Uses title+path matching with
	// minMatches=1, then filters by bidirectional overlap score. Uses
	// permissive titleTerms (3+ chars) to catch short words like "home",
	// "same" that keyword extraction misses. Searches both title and path
	// to find notes in project directories (e.g., project-alpha/design-brief.md).
	if len(titleTerms) > 0 {
		titleResults, titleErr := db.KeywordSearchTitleMatch(titleTerms, 1, maxResults*10)
		if titleErr == nil {
			for _, r := range titleResults {
				if seen[r.Path] {
					continue
				}
				// SECURITY: never auto-surface _PRIVATE/ content
				if shouldSkipPath(r.Path) {
					continue
				}
				// Filter by max of title-only and path-inclusive overlap.
				// Path components can DILUTE overlap when they add non-matching
				// words (e.g., "my-projects" adds {my, projects} to the
				// denominator without matching query terms). Using just
				// fullOverlap can reject candidates whose titles match.
				fullOverlap := titleOverlapScore(titleTerms, r.Title, r.Path)
				titleOnly := titleOverlapScore(titleTerms, r.Title, "")
				filterOverlap := fullOverlap
				if titleOnly > filterOverlap {
					filterOverlap = titleOnly
				}
				if filterOverlap < minTitleOverlap {
					continue
				}
				seen[r.Path] = true
				comp := memory.CompositeScore(0.85, r.Modified, r.Confidence, r.ContentType,
					0.3, 0.3, 0.4)
				if comp >= minComposite {
					s := makeScored(r, comp, 0.85)
					s.titleOverlap = overlapForSort(titleTerms, r.Title, r.Path)

					candidates = append(candidates, s)
				}
			}
		}
	}

	// Mode 2b: Hub/overview rescue. Large directories (e.g., 86 experiment
	// files) can crowd hub notes out of Mode 2's LIMIT. This targeted pass
	// searches title-only for hub-type notes with strong title overlap
	// (>= 0.50). Uses real titleOverlap so hubs sort correctly alongside
	// other results with path-based overlap.
	if len(titleTerms) > 0 {
		hubResults, hubErr := db.KeywordSearchTitleMatch(titleTerms, 1, 10, true)
		if hubErr == nil {
			for _, r := range hubResults {
				if seen[r.Path] {
					continue
				}
				// SECURITY: never auto-surface _PRIVATE/ content
				if shouldSkipPath(r.Path) {
					continue
				}
				if r.ContentType != "hub" {
					continue
				}
				overlap := titleOverlapScore(titleTerms, r.Title, "")
				if overlap < 0.50 {
					continue
				}
				seen[r.Path] = true
				comp := memory.CompositeScore(0.85, r.Modified, r.Confidence, r.ContentType,
					0.3, 0.3, 0.4)
				if comp >= minComposite {
					s := makeScored(r, comp, 0.85)
					s.titleOverlap = overlap
					candidates = append(candidates, s)
				}
			}
		}
	}

	// Check for strong/positive candidates before keyword fallback
	hasStrongCandidateForKW := false
	hasPositiveCandidateForKW := false
	for _, c := range candidates {
		if c.titleOverlap >= highTierOverlap {
			hasStrongCandidateForKW = true
		}
		if c.titleOverlap > 0 {
			hasPositiveCandidateForKW = true
		}
	}

	// Mode 1: specific terms -> full-text search.
	// Runs when candidates below target OR when vector search failed
	// entirely with no strong title-match candidate (e.g., "career
	// strategy and PM skills" where the expected note has terms only
	// in content, not title). Uses ContentTermSearch (all chunks) to
	// find notes where query terms appear in body text.
	if len(specificTerms) > 0 && (len(candidates) < maxResults || (vectorEmpty && !hasStrongCandidateForKW)) {
		terms := specificTerms
		if len(broadTerms) > 0 {
			terms = append(terms, broadTerms...)
		}

		// Build content-verified path set when vector is empty.
		// ContentTermSearch checks all chunks, solving the issue
		// where terms like "career" and "skills" appear in later
		// chunks but not chunk 0. Uses N-1 matching (min 2) to
		// handle cross-chunk term distribution.
		contentVerified := make(map[string]bool)
		var contentVerifiedResults []store.RawSearchResult
		if vectorEmpty && len(broadTerms) >= 3 {
			contentMinTerms := len(broadTerms) - 1
			if contentMinTerms < 2 {
				contentMinTerms = 2
			}
			cResults, _ := db.ContentTermSearch(broadTerms, contentMinTerms, maxResults*10)
			contentVerifiedResults = cResults
			for _, cr := range cResults {
				contentVerified[cr.Path] = true
			}
		}

		kwResults, err := db.KeywordSearch(terms, maxResults*3)
		if err == nil {
			for _, r := range kwResults {
				if seen[r.Path] {
					continue
				}
				// SECURITY: never auto-surface _PRIVATE/ content
				if shouldSkipPath(r.Path) {
					continue
				}
				seen[r.Path] = true
				comp := memory.CompositeScore(0.85, r.Modified, r.Confidence, r.ContentType,
					0.3, 0.3, 0.4)
				if comp >= minComposite {
					s := makeScored(r, comp, 0.85)
					if contentVerified[r.Path] {
						s.titleOverlap = overlapForSort(titleTerms, r.Title, r.Path)
						if s.titleOverlap < 0.15 && !hasPositiveCandidateForKW {
							s.titleOverlap = 0.15
						}
					}
					candidates = append(candidates, s)
				}
			}
		}

		// Rescue: add content-verified notes that KeywordSearch missed.
		firstContentRescue := true
		for _, cr := range contentVerifiedResults {
			if seen[cr.Path] {
				continue
			}
			// SECURITY: never auto-surface _PRIVATE/ content
			if shouldSkipPath(cr.Path) {
				continue
			}
			seen[cr.Path] = true
			comp := memory.CompositeScore(0.85, cr.Modified, cr.Confidence, cr.ContentType,
				0.3, 0.3, 0.4)
			if comp >= minComposite {
				s := makeScored(cr, comp, 0.85)
				s.titleOverlap = overlapForSort(titleTerms, cr.Title, cr.Path)
				if firstContentRescue && s.titleOverlap < highTierOverlap && !hasStrongCandidateForKW && !hasPositiveCandidateForKW {
					s.titleOverlap = 0.25
					s.contentBoosted = true
					firstContentRescue = false
				} else if s.titleOverlap < 0.15 && !hasPositiveCandidateForKW {
					s.titleOverlap = 0.15
				}
				candidates = append(candidates, s)
			}
		}
	}

	// Mode 3: broad fallback — original behavior for when vector is empty
	if len(candidates) < maxResults && vectorEmpty && len(broadTerms) >= 2 && len(specificTerms) == 0 {
		kwResults, err := db.KeywordSearchTitleMatch(broadTerms, 2, maxResults*3)
		if err == nil {
			for _, r := range kwResults {
				if seen[r.Path] {
					continue
				}
				// SECURITY: never auto-surface _PRIVATE/ content
				if shouldSkipPath(r.Path) {
					continue
				}
				seen[r.Path] = true
				comp := memory.CompositeScore(0.85, r.Modified, r.Confidence, r.ContentType,
					0.3, 0.3, 0.4)
				if comp >= minComposite {
					candidates = append(candidates, makeScored(r, comp, 0.85))
				}
			}
		}
	}

	// Mode 4: fuzzy title search for misspellings (e.g., "kubernetes" -> "kuberntes").
	// Uses edit distance 1 matching. Runs when there's room for more results.
	// Only adds hub-type notes or notes with very high title overlap (>= 0.40)
	// to avoid false positives from broad fuzzy matching.
	if len(candidates) < maxResults {
		searchTerms := store.ExtractSearchTerms(keyTermsPrompt)
		fuzzyResults, _ := db.FuzzyTitleSearch(searchTerms, maxResults*3)
		for _, r := range fuzzyResults {
			if seen[r.Path] {
				continue
			}
			// SECURITY: never auto-surface _PRIVATE/ content
			if shouldSkipPath(r.Path) {
				continue
			}
			overlap := titleOverlapScore(titleTerms, r.Title, "")
			isHub := r.ContentType == "hub"
			if !isHub && overlap < 0.40 {
				continue
			}
			if isHub && overlap < 0.20 {
				continue
			}
			seen[r.Path] = true
			comp := memory.CompositeScore(0.85, r.Modified, r.Confidence, r.ContentType,
				0.3, 0.3, 0.4)
			if comp >= minComposite {
				s := makeScored(r, comp, 0.85)
				s.titleOverlap = overlap
				candidates = append(candidates, s)
			}
		}
	}

	// Mode 5: broad content keyword search. Searches note content using
	// broad terms via ContentTermSearch (checks ALL chunks, not just
	// chunk 0). Runs when:
	// - candidates below target, OR
	// - vector search was empty, OR
	// - no existing candidate has strong title overlap (>= 0.20)
	hasStrongCandidate := false
	hasPositiveCandidate := false
	for _, c := range candidates {
		if c.titleOverlap >= highTierOverlap {
			hasStrongCandidate = true
		}
		if c.titleOverlap > 0 {
			hasPositiveCandidate = true
		}
	}
	candidatesBeforeMode5 := len(candidates)
	if len(specificTerms) == 0 && len(broadTerms) >= 3 && (candidatesBeforeMode5 < maxResults || vectorEmpty || !hasStrongCandidate) {
		contentResults, err := db.ContentTermSearch(broadTerms, len(broadTerms), maxResults*10)
		if err == nil {
			needsFill := candidatesBeforeMode5 < maxResults
			firstContentAdd := true
			for _, r := range contentResults {
				if seen[r.Path] {
					continue
				}
				// SECURITY: never auto-surface _PRIVATE/ content
				if shouldSkipPath(r.Path) {
					continue
				}
				seen[r.Path] = true
				comp := memory.CompositeScore(0.85, r.Modified, r.Confidence, r.ContentType,
					0.3, 0.3, 0.4)
				if comp >= minComposite {
					s := makeScored(r, comp, 0.85)
					s.titleOverlap = overlapForSort(titleTerms, r.Title, r.Path)
					if needsFill {
						if s.titleOverlap < highTierOverlap && firstContentAdd && !hasStrongCandidate && !hasPositiveCandidate {
							s.titleOverlap = 0.25
							s.contentBoosted = true
							firstContentAdd = false
						} else if s.titleOverlap < 0.15 && !hasPositiveCandidate {
							s.titleOverlap = 0.15
						}
					}
					candidates = append(candidates, s)
				}
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Near-dedup: collapse versioned copies in the same directory.
	candidates = nearDedup(candidates, titleTerms)

	// Sort: three tiers — high title overlap (>= 0.20), positive title
	// overlap (> 0), and zero overlap. Within each tier: priority content
	// types first, then composite score.
	sort.Slice(candidates, func(i, j int) bool {
		iHigh := candidates[i].titleOverlap >= highTierOverlap
		jHigh := candidates[j].titleOverlap >= highTierOverlap
		if iHigh != jHigh {
			return iHigh
		}
		if iHigh && jHigh && candidates[i].titleOverlap != candidates[j].titleOverlap {
			return candidates[i].titleOverlap > candidates[j].titleOverlap
		}
		iPos := candidates[i].titleOverlap > 0
		jPos := candidates[j].titleOverlap > 0
		if iPos != jPos {
			return iPos
		}
		iPri := priorityTypes[candidates[i].contentType]
		jPri := priorityTypes[candidates[j].contentType]
		if iPri != jPri {
			return iPri
		}
		return candidates[i].composite > candidates[j].composite
	})

	return candidates
}

// keywordFallbackSearch uses FTS5 full-text search when embedding provider is unavailable.
// Quality is lower than semantic search but functional.
func keywordFallbackSearch(db *store.DB) []scored {
	prompt := keyTermsPrompt
	if prompt == "" {
		return nil
	}

	results, err := db.FTS5Search(prompt, store.SearchOptions{TopK: maxResults})
	if err != nil {
		// FTS5 table may not exist yet — fall back to LIKE-based keyword search
		terms := store.ExtractSearchTerms(prompt)
		if len(terms) == 0 {
			return nil
		}
		kwResults, kwErr := db.KeywordSearch(terms, maxResults)
		if kwErr != nil || len(kwResults) == 0 {
			return nil
		}
		var candidates []scored
		for _, r := range kwResults {
			if shouldSkipPath(r.Path) {
				continue
			}
			snippet := r.Text
			if len(snippet) > int(maxSnippetChars) {
				snippet = snippet[:maxSnippetChars]
			}
			candidates = append(candidates, scored{
				path:        r.Path,
				title:       r.Title,
				contentType: r.ContentType,
				confidence:  r.Confidence,
				snippet:     sanitizeSnippet(snippet),
				composite:   0.5,
				semantic:    0,
			})
		}
		return candidates
	}

	var candidates []scored
	for _, r := range results {
		if shouldSkipPath(r.Path) {
			continue
		}
		candidates = append(candidates, scored{
			path:        r.Path,
			title:       r.Title,
			contentType: r.ContentType,
			confidence:  r.Confidence,
			snippet:     sanitizeSnippet(r.Snippet),
			composite:   0.5,
			semantic:    0,
		})
	}
	return candidates
}

// recencyHybridSearch merges vector results with time-sorted results.
// Uses recency-heavy weights and includes recently modified notes even
// if they aren't strong semantic matches.
func recencyHybridSearch(db *store.DB, queryVec []float32) []scored {
	titleTerms := queryWordsForTitleMatch()

	// Get vector search results (relaxed distance threshold)
	raw, err := db.VectorSearchRaw(queryVec, recencyMaxResults*6)
	if err != nil {
		raw = nil
	}

	// Get most recently modified notes
	recentNotes, err := db.RecentNotes(recencyMaxResults * 3)
	if err != nil {
		recentNotes = nil
	}

	// Merge: build candidate set from both sources
	candidateMap := make(map[string]*scored)

	// Process vector results (if any matched)
	if len(raw) > 0 {
		deduped := dedup(raw)
		minDist, maxDist := distRange(deduped)
		dRange := maxDist - minDist
		if dRange <= 0 {
			dRange = 1.0
		}

		for _, r := range deduped {
			// Relaxed distance gate for recency queries
			if r.Distance > maxDistance+2.0 {
				continue
			}
			// SECURITY: never auto-surface _PRIVATE/ content
			if shouldSkipPath(r.Path) {
				continue
			}
			semScore := 1.0 - ((r.Distance - minDist) / dRange)
			if semScore < 0 {
				semScore = 0
			}

			comp := memory.CompositeScore(semScore, r.Modified, r.Confidence, r.ContentType,
				recencyRelWeight, recencyRecWeight, recencyConfWeight)

			if comp >= recencyMinComposite {
				s := makeScored(r, comp, semScore)
				candidateMap[r.Path] = &s
			}
		}
	}

	// Process recent notes (time-sorted, no vector match required)
	// Filter to session-relevant content types to reduce false positives from
	// random notes that happen to be recently modified.
	for _, n := range recentNotes {
		if _, exists := candidateMap[n.Path]; exists {
			continue // already from vector results, keep that score
		}
		// SECURITY: never auto-surface _PRIVATE/ content
		if shouldSkipPath(n.Path) {
			continue
		}
		// Only merge session-relevant content types for recency
		if !isRecencyRelevantType(n.ContentType) {
			continue
		}

		// Score purely on recency + confidence (no semantic component)
		comp := memory.CompositeScore(0, n.Modified, n.Confidence, n.ContentType,
			recencyRelWeight, recencyRecWeight, recencyConfWeight)

		if comp >= recencyMinComposite {
			snippet := queryBiasedSnippet(n.Text, maxSnippetChars)
			snippet = sanitizeSnippet(snippet)
			candidateMap[n.Path] = &scored{
				path:        n.Path,
				title:       n.Title,
				contentType: n.ContentType,
				confidence:  n.Confidence,
				snippet:     snippet,
				composite:   comp,
				semantic:    0,
				distance:    0,
			}
		}
	}

	// Title keyword search: discover notes with matching titles that
	// vector search and RecentNotes may have missed. Critical for queries
	// like "latest session handoff notes" where the user wants actual
	// session handoff files, not documents ABOUT session handoffs.
	if len(titleTerms) > 0 {
		titleResults, titleErr := db.KeywordSearchTitleMatch(titleTerms, 2, recencyMaxResults*10)
		if titleErr == nil {
			for _, r := range titleResults {
				// SECURITY: never auto-surface _PRIVATE/ content
				if shouldSkipPath(r.Path) {
					continue
				}
				overlap := titleOverlapScore(titleTerms, r.Title, r.Path)
				if overlap < minTitleOverlap*0.5 {
					continue
				}
				// If already in map (from vector/recent), just update
				// titleOverlap so it gets the title-matching sort boost.
				if existing, exists := candidateMap[r.Path]; exists {
					if overlap > existing.titleOverlap {
						existing.titleOverlap = overlap
					}
					continue
				}
				comp := memory.CompositeScore(0.85, r.Modified, r.Confidence, r.ContentType,
					recencyRelWeight, recencyRecWeight, recencyConfWeight)
				if comp >= recencyMinComposite {
					s := makeScored(r, comp, 0.85)
					s.titleOverlap = overlap
					candidateMap[r.Path] = &s
				}
			}
		}
	}

	// Collect candidates. Vector/recent entries now get their titleOverlap
	// updated by title keyword search (above) so that notes found by both
	// vector and title search benefit from the title-matching sort boost.
	var candidates []scored
	for _, s := range candidateMap {
		candidates = append(candidates, *s)
	}

	// Sort: title-matching results first (these are what the user asked
	// about), then by composite (recency-heavy) within each tier.
	sort.Slice(candidates, func(i, j int) bool {
		iHigh := candidates[i].titleOverlap >= 0.05
		jHigh := candidates[j].titleOverlap >= 0.05
		if iHigh != jHigh {
			return iHigh
		}
		if iHigh && jHigh && candidates[i].titleOverlap != candidates[j].titleOverlap {
			return candidates[i].titleOverlap > candidates[j].titleOverlap
		}
		return candidates[i].composite > candidates[j].composite
	})

	return candidates
}

func dedup(raw []store.RawSearchResult) []store.RawSearchResult {
	seen := make(map[string]bool)
	var out []store.RawSearchResult
	for _, r := range raw {
		if seen[r.Path] {
			continue
		}
		seen[r.Path] = true
		out = append(out, r)
	}
	return out
}

// nearDedup collapses versioned copies in the same directory. When two
// candidates share a parent directory and one filename (sans .md) is a
// prefix of the other, they're treated as near-duplicates and only the
// better-scoring one is kept. Uses full-path overlap (title + path) to
// correctly resolve when the query mentions a version suffix.
func nearDedup(candidates []scored, queryTerms []string) []scored {
	type key struct{ dir, base string }
	pathParts := func(p string) key {
		slash := strings.LastIndex(p, "/")
		if slash < 0 {
			return key{"", strings.TrimSuffix(p, ".md")}
		}
		return key{p[:slash], strings.TrimSuffix(p[slash+1:], ".md")}
	}

	// Build pairs of near-duplicates (same dir, one prefix of other)
	remove := make(map[int]bool)
	for i := 0; i < len(candidates); i++ {
		if remove[i] {
			continue
		}
		ki := pathParts(candidates[i].path)
		for j := i + 1; j < len(candidates); j++ {
			if remove[j] {
				continue
			}
			kj := pathParts(candidates[j].path)
			if ki.dir != kj.dir {
				continue
			}
			li, lj := strings.ToLower(ki.base), strings.ToLower(kj.base)
			if !strings.HasPrefix(lj, li) && !strings.HasPrefix(li, lj) {
				continue
			}
			// Near-duplicate found — use full-path overlap to pick winner
			oi := titleOverlapScore(queryTerms, candidates[i].title, candidates[i].path)
			oj := titleOverlapScore(queryTerms, candidates[j].title, candidates[j].path)
			if oj > oi || (oj == oi && candidates[j].composite > candidates[i].composite) {
				remove[i] = true
				break
			}
			remove[j] = true
		}
	}

	var out []scored
	for i, c := range candidates {
		if !remove[i] {
			out = append(out, c)
		}
	}
	return out
}

func distRange(results []store.RawSearchResult) (float64, float64) {
	if len(results) == 0 {
		return 0, 1
	}
	minD := results[0].Distance
	maxD := results[0].Distance
	for _, r := range results[1:] {
		if r.Distance < minD {
			minD = r.Distance
		}
		if r.Distance > maxD {
			maxD = r.Distance
		}
	}
	return minD, maxD
}

func makeScored(r store.RawSearchResult, comp, sem float64) scored {
	snippet := queryBiasedSnippet(r.Text, maxSnippetChars)
	snippet = sanitizeSnippet(snippet)
	return scored{
		path:        r.Path,
		title:       r.Title,
		contentType: r.ContentType,
		confidence:  r.Confidence,
		snippet:     snippet,
		composite:   comp,
		semantic:    sem,
		distance:    r.Distance,
	}
}

// isPrivatePath returns true if the path is under the _PRIVATE/ directory.
func isPrivatePath(path string) bool {
	return strings.HasPrefix(path, privateDirPrefix) ||
		strings.HasPrefix(path, "_PRIVATE\\")
}

// isNoisyPath returns true if the path matches a user-configured noise prefix.
func isNoisyPath(path string) bool {
	for _, prefix := range noisyPathPrefixes() {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// shouldSkipPath returns true if the path should be excluded from surfacing.
func shouldSkipPath(path string) bool {
	return isPrivatePath(path) || isNoisyPath(path)
}

// recencyPriority returns a sorting priority for recency queries.
// Lower number = higher priority. Handoff/session notes are prioritized
// because recency queries typically want session context, not generic hubs.
func recencyPriority(path string) int {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "handoff") || strings.Contains(lower, "session") {
		return 0
	}
	if strings.Contains(lower, "decision") {
		return 1
	}
	if strings.Contains(lower, "working_notes") || strings.Contains(lower, "progress") {
		return 2
	}
	return 3
}

// isRecencyRelevantType returns true if a content type is session-relevant.
// Used to filter RecentNotes merge to avoid surfacing random notes that
// happen to be recently modified.
func isRecencyRelevantType(contentType string) bool {
	switch contentType {
	case "handoff", "hub", "progress", "decision":
		return true
	}
	return false
}
