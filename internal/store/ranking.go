package store

import (
	"regexp"
	"strings"
)

// Title overlap scoring for ranking search results.
// Ported from hooks/context_surfacing.go to be shared across
// the hook and MCP search paths.

const (
	// HighTierOverlap is the floating-point-safe threshold for >= 0.20.
	// IEEE 754: 3/5 * 3/9 = 0.19999..., so we use 0.199 to avoid
	// rejecting mathematically-0.20 overlaps.
	HighTierOverlap = 0.199

	// MinTitleOverlap is the minimum bidirectional overlap for a
	// title match to be considered relevant.
	MinTitleOverlap = 0.10
)

// rankingWordRe matches word tokens (letters, digits, underscores).
var rankingWordRe = regexp.MustCompile(`[\w]+`)

// rankingTitleMatchStopWords filters common English words from title matching.
var rankingTitleMatchStopWords = map[string]bool{
	// 3-letter
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "has": true,
	"her": true, "his": true, "how": true, "its": true, "may": true,
	"new": true, "now": true, "our": true, "out": true, "own": true,
	"too": true, "use": true, "was": true, "who": true, "why": true,
	"did": true, "get": true, "got": true, "had": true, "let": true,
	"say": true, "she": true, "any": true, "way": true, "yet": true,
	// 4-letter
	"also": true, "area": true, "back": true, "been": true, "best": true,
	"call": true, "case": true, "come": true, "data": true, "does": true,
	"done": true, "each": true, "even": true, "find": true, "from": true,
	"give": true, "goes": true, "good": true, "have": true, "help": true,
	"here": true, "into": true, "just": true, "keep": true, "kind": true,
	"know": true, "last": true, "left": true, "like": true, "list": true,
	"long": true, "look": true, "made": true, "main": true, "make": true,
	"many": true, "more": true, "most": true, "much": true, "must": true,
	"need": true, "next": true, "once": true, "only": true,
	"open": true, "over": true, "part": true,
	"show": true, "side": true, "some": true, "such": true, "sure": true,
	"take": true, "talk": true, "tell": true, "test": true, "than": true,
	"that": true, "them": true, "then": true, "they": true, "this": true,
	"time": true, "turn": true, "type": true, "used": true, "uses": true,
	"very": true, "want": true, "well": true, "went": true, "were": true,
	"what": true, "when": true, "will": true, "with": true, "work": true,
	"your": true,
	// 5+ letter
	"about": true, "above": true, "after": true, "again": true, "being": true,
	"below": true, "between": true, "could": true, "doing": true, "during": true,
	"every": true, "found": true, "going": true, "having": true, "might": true,
	"never": true, "other": true, "should": true, "their": true, "there": true,
	"these": true, "thing": true, "think": true, "those": true, "under": true,
	"until": true, "using": true, "where": true, "which": true, "while": true,
	"would": true, "write": true, "yours": true, "really": true, "please": true,
	"right": true, "since": true, "still": true, "today": true,
	// Query boilerplate
	"explain": true, "tracked": true, "defined": true,
}

// rankingMeaningful2Char are 2-char terms with domain meaning.
var rankingMeaningful2Char = map[string]bool{
	"ai": true, "os": true, "pm": true, "qa": true,
	"ui": true, "ux": true, "hr": true, "ml": true,
	"v1": true, "v2": true, "v3": true, "v4": true, "v5": true,
}

// QueryWordsForTitleMatch extracts meaningful words from a query for title
// overlap scoring. More permissive than ExtractSearchTerms — includes short
// words (3+ chars) and alphanumeric tokens — because title overlap scoring
// handles false positives via bidirectional threshold.
func QueryWordsForTitleMatch(query string) []string {
	words := rankingWordRe.FindAllString(query, -1)
	seen := make(map[string]bool)
	var result []string
	for _, w := range words {
		lower := strings.ToLower(w)
		if len(w) < 3 {
			if len(w) == 2 && rankingMeaningful2Char[lower] {
				// keep meaningful 2-char terms
			} else {
				continue
			}
		}
		if rankingTitleMatchStopWords[lower] || seen[lower] {
			continue
		}
		result = append(result, w)
		seen[lower] = true
	}
	return result
}

// TitleOverlapScore computes bidirectional term overlap between query terms
// and a note's title + path. Returns queryCoverage * wordCoverage in [0, 1].
//
// Words are extracted from both the title and path (directory components),
// with underscore splitting and plural/stem matching.
func TitleOverlapScore(queryTerms []string, title, path string) float64 {
	if len(queryTerms) == 0 {
		return 0
	}

	// Extract words from title
	allWords := rankingWordRe.FindAllString(title, -1)

	// Extract words from path components (strip .md extension first)
	cleanPath := strings.TrimSuffix(path, ".md")
	for _, part := range strings.Split(cleanPath, "/") {
		allWords = append(allWords, rankingWordRe.FindAllString(part, -1)...)
	}

	// Build lowercase set, splitting underscores and filtering short tokens
	wordSet := make(map[string]bool, len(allWords))
	for _, w := range allWords {
		subWords := []string{w}
		if strings.Contains(w, "_") {
			subWords = strings.Split(w, "_")
		}
		for _, sub := range subWords {
			if len(sub) >= 2 {
				wordSet[strings.ToLower(sub)] = true
			}
		}
	}
	wordCount := len(wordSet)
	if wordCount == 0 {
		return 0
	}

	// Expand hyphenated query terms
	var expanded []string
	for _, t := range queryTerms {
		if strings.Contains(t, "-") {
			for _, part := range strings.Split(t, "-") {
				if len(part) >= 2 {
					expanded = append(expanded, part)
				}
			}
		} else {
			expanded = append(expanded, t)
		}
	}
	if len(expanded) == 0 {
		return 0
	}

	// Count terms that match. Each wordSet entry can only be matched once.
	// Matching cascade: exact -> plural -> edit distance 1 -> common stem.
	matchCount := 0
	matchedEntries := make(map[string]bool, len(wordSet))
	for _, t := range expanded {
		lower := strings.ToLower(t)
		var matched string
		if wordSet[lower] && !matchedEntries[lower] {
			matched = lower
		} else if wordSet[lower+"s"] && !matchedEntries[lower+"s"] {
			matched = lower + "s"
		} else if len(lower) > 2 && strings.HasSuffix(lower, "s") && wordSet[lower[:len(lower)-1]] && !matchedEntries[lower[:len(lower)-1]] {
			matched = lower[:len(lower)-1]
		}
		if matched == "" {
			for w := range wordSet {
				if matchedEntries[w] {
					continue
				}
				if rankingEditDistance1(lower, w) || rankingSharesStem(lower, w) {
					matched = w
					break
				}
			}
		}
		if matched != "" {
			matchedEntries[matched] = true
			matchCount++
		}
	}
	if matchCount == 0 {
		return 0
	}

	queryCoverage := float64(matchCount) / float64(len(expanded))
	wordCoverage := float64(matchCount) / float64(wordCount)

	// Short word sets are noisy for single-term matches
	if wordCount <= 2 && queryCoverage < 0.30 {
		return 0
	}

	return queryCoverage * wordCoverage
}

// OverlapForSort returns the overlap score for sorting. Uses title-only
// overlap as primary signal; when path-inclusive overlap is strong (>= 0.25),
// provides half-strength so path-matched notes survive ranking without
// competing with direct title matches.
func OverlapForSort(queryTerms []string, title, path string) float64 {
	titleOnly := TitleOverlapScore(queryTerms, title, "")
	if titleOnly > 0 {
		return titleOnly
	}
	fullOverlap := TitleOverlapScore(queryTerms, title, path)
	if fullOverlap >= 0.25 {
		return fullOverlap * 0.5
	}
	return 0
}

// rankingEditDistance1 returns true if two lowercase strings differ by exactly
// one character. Only applies to words >= 7 chars to avoid false positives.
func rankingEditDistance1(a, b string) bool {
	la, lb := len(a), len(b)
	if la < 7 && lb < 7 {
		return false
	}
	diff := la - lb
	if diff < -1 || diff > 1 {
		return false
	}
	if diff == 0 {
		diffs := 0
		for i := 0; i < la; i++ {
			if a[i] != b[i] {
				diffs++
				if diffs > 1 {
					return false
				}
			}
		}
		return diffs == 1
	}
	longer, shorter := a, b
	if lb > la {
		longer, shorter = b, a
	}
	diffs := 0
	j := 0
	for i := 0; i < len(longer) && j < len(shorter); i++ {
		if longer[i] != shorter[j] {
			diffs++
			if diffs > 1 {
				return false
			}
			continue
		}
		j++
	}
	return true
}

// rankingSharesStem returns true if two lowercase words likely share the same
// root. Requires both >= 5 chars and a common prefix covering all but the
// last char of the shorter word, with at most 3 extra chars on the longer.
func rankingSharesStem(a, b string) bool {
	la, lb := len(a), len(b)
	if la < 5 || lb < 5 {
		return false
	}
	shorter := la
	if lb < shorter {
		shorter = lb
	}
	lengthDiff := la - lb
	if lengthDiff < 0 {
		lengthDiff = -lengthDiff
	}
	if lengthDiff > 3 {
		return false
	}
	common := 0
	for i := 0; i < shorter; i++ {
		if a[i] != b[i] {
			break
		}
		common++
	}
	return common >= shorter-1 && common >= 5
}

// rankedResult pairs a SearchResult with its computed title overlap score.
type rankedResult struct {
	result       SearchResult
	titleOverlap float64
}

// RankSearchResults applies title-overlap-aware ranking to search results.
// This is the main entry point for post-processing MCP search results.
// It computes title overlap, filters noise, near-dedups, and re-sorts
// using a three-tier sort (high overlap > positive overlap > zero overlap).
func RankSearchResults(results []SearchResult, queryTerms []string) []SearchResult {
	if len(results) == 0 || len(queryTerms) == 0 {
		return results
	}

	// Compute title overlap for each result
	items := make([]rankedResult, 0, len(results))
	for _, r := range results {
		overlap := OverlapForSort(queryTerms, r.Title, r.Path)
		items = append(items, rankedResult{result: r, titleOverlap: overlap})
	}

	// Filter raw experiment outputs
	{
		var filtered []rankedResult
		for _, item := range items {
			if !strings.Contains(item.result.Path, "/raw_outputs/") {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) > 0 {
			items = filtered
		}
	}

	// Near-dedup: collapse versioned copies in the same directory
	items = nearDedupRanked(items, queryTerms)

	// Three-tier sort:
	//   High tier:   titleOverlap >= 0.20 (strong title match)
	//   Medium tier:  titleOverlap >= 0.10 (meaningful title signal)
	//   Low tier:    everything else (vector-only or trivial overlap)
	//
	// The medium tier threshold (MinTitleOverlap = 0.10) prevents
	// single-word matches on generic terms (e.g., "model" in "Seed
	// Model" for a "semantic search embedding model" query) from
	// outranking semantically better vector results.
	priorityTypes := map[string]bool{
		"handoff": true, "decision": true, "research": true, "hub": true,
	}
	stableSort(items, func(a, b rankedResult) bool {
		aHigh := a.titleOverlap >= HighTierOverlap
		bHigh := b.titleOverlap >= HighTierOverlap
		if aHigh != bHigh {
			return aHigh
		}
		if aHigh && bHigh && a.titleOverlap != b.titleOverlap {
			return a.titleOverlap > b.titleOverlap
		}
		aMed := a.titleOverlap >= MinTitleOverlap
		bMed := b.titleOverlap >= MinTitleOverlap
		if aMed != bMed {
			return aMed
		}
		// Priority type preference only in medium/high tiers where
		// title overlap provides signal. In the low tier (no meaningful
		// overlap), the original Score from vector/keyword search is
		// a better ranking signal than content type.
		if aMed && bMed {
			aPri := priorityTypes[a.result.ContentType]
			bPri := priorityTypes[b.result.ContentType]
			if aPri != bPri {
				return aPri
			}
		}
		return a.result.Score > b.result.Score
	})

	// Rebuild result slice
	out := make([]SearchResult, len(items))
	for i, item := range items {
		out[i] = item.result
	}
	return out
}

// nearDedupRanked collapses versioned copies in the same directory.
// When two results share a parent directory and one filename (sans .md) is a
// prefix of the other, keeps only the better one (by title overlap then score).
func nearDedupRanked(items []rankedResult, queryTerms []string) []rankedResult {
	type key struct{ dir, base string }
	pathParts := func(p string) key {
		slash := strings.LastIndex(p, "/")
		if slash < 0 {
			return key{"", strings.TrimSuffix(p, ".md")}
		}
		return key{p[:slash], strings.TrimSuffix(p[slash+1:], ".md")}
	}

	remove := make(map[int]bool)
	for i := 0; i < len(items); i++ {
		if remove[i] {
			continue
		}
		ki := pathParts(items[i].result.Path)
		for j := i + 1; j < len(items); j++ {
			if remove[j] {
				continue
			}
			kj := pathParts(items[j].result.Path)
			if ki.dir != kj.dir {
				continue
			}
			li, lj := strings.ToLower(ki.base), strings.ToLower(kj.base)
			if !strings.HasPrefix(lj, li) && !strings.HasPrefix(li, lj) {
				continue
			}
			// Near-duplicate found — use full-path overlap to pick winner
			oi := TitleOverlapScore(queryTerms, items[i].result.Title, items[i].result.Path)
			oj := TitleOverlapScore(queryTerms, items[j].result.Title, items[j].result.Path)
			if oj > oi || (oj == oi && items[j].result.Score > items[i].result.Score) {
				remove[i] = true
				break
			}
			remove[j] = true
		}
	}

	var out []rankedResult
	for i, item := range items {
		if !remove[i] {
			out = append(out, item)
		}
	}
	return out
}

// stableSort sorts a slice of ranked items using a stable sort and a
// less function.
func stableSort(items []rankedResult, less func(a, b rankedResult) bool) {
	// Simple insertion sort for stability (result sets are small, typically < 20)
	for i := 1; i < len(items); i++ {
		key := items[i]
		j := i - 1
		for j >= 0 && less(key, items[j]) {
			items[j+1] = items[j]
			j--
		}
		items[j+1] = key
	}
}
