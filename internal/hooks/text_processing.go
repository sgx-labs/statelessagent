package hooks

import (
	"regexp"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/store"
)

// Prompt injection patterns — content matching these is stripped from snippets
// before injection. Prevents vault notes from hijacking agent behavior.
var injectionPatterns = []string{
	"ignore previous",
	"ignore all previous",
	"ignore above",
	"disregard previous",
	"disregard all previous",
	"you are now",
	"new instructions",
	"system prompt",
	"<system>",
	"</system>",
	"IMPORTANT:",
	"CRITICAL:",
	"override",
}

// smartTruncate truncates text at a sentence or paragraph boundary near maxLen.
// Falls back to word boundary if no sentence break is found.
func smartTruncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}

	// Look for the last sentence-ending punctuation before maxLen
	// Search in the last 30% of the allowed range for a good break
	searchStart := maxLen * 7 / 10
	candidate := text[:maxLen]

	// Try paragraph break first (double newline)
	if idx := strings.LastIndex(candidate[searchStart:], "\n\n"); idx >= 0 {
		return strings.TrimSpace(candidate[:searchStart+idx])
	}

	// Try sentence break (. ! ? followed by space or newline)
	bestBreak := -1
	for i := searchStart; i < maxLen-1; i++ {
		if (candidate[i] == '.' || candidate[i] == '!' || candidate[i] == '?') &&
			(candidate[i+1] == ' ' || candidate[i+1] == '\n') {
			bestBreak = i + 1
		}
	}
	if bestBreak > 0 {
		return strings.TrimSpace(candidate[:bestBreak])
	}

	// Try single newline
	if idx := strings.LastIndex(candidate[searchStart:], "\n"); idx >= 0 {
		return strings.TrimSpace(candidate[:searchStart+idx])
	}

	// Fall back to word boundary
	if idx := strings.LastIndex(candidate[searchStart:], " "); idx >= 0 {
		return strings.TrimSpace(candidate[:searchStart+idx])
	}

	return candidate
}

// stripLeadingHeadings removes leading markdown headings (# Title lines)
// from text. The snippet already shows the title in bold, so repeating it
// as a heading wastes tokens. Returns the text starting from the first
// non-heading, non-empty line.
func stripLeadingHeadings(text string) string {
	lines := strings.SplitN(text, "\n", 20) // only check first 20 lines
	start := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			start = i + 1
			continue
		}
		break
	}
	if start >= len(lines) {
		return "" // all lines were headings, omit snippet
	}
	// Rejoin from the first content line
	return strings.TrimSpace(strings.Join(lines[start:], "\n"))
}

// queryBiasedSnippet extracts the most query-relevant window of text.
// Instead of always showing the first N chars (which may just be an intro),
// it finds the paragraph with the most query-term overlap and starts there.
// Falls back to the beginning if no query terms match.
func queryBiasedSnippet(text string, maxLen int) string {
	text = stripLeadingHeadings(text)
	if text == "" || len(text) <= maxLen {
		return text
	}

	prompt := keyTermsPrompt
	if prompt == "" {
		return smartTruncate(text, maxLen)
	}

	words := store.QueryWordsForTitleMatch(prompt)
	if len(words) == 0 {
		return smartTruncate(text, maxLen)
	}

	// Split into paragraphs (double newline) or single lines
	sep := "\n\n"
	paragraphs := strings.Split(text, sep)
	if len(paragraphs) <= 1 {
		sep = "\n"
		paragraphs = strings.Split(text, sep)
	}

	// Score each paragraph by query-term overlap
	bestIdx := 0
	bestScore := 0
	for i, para := range paragraphs {
		paraLower := strings.ToLower(para)
		score := 0
		for _, w := range words {
			if strings.Contains(paraLower, w) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestScore == 0 {
		return smartTruncate(text, maxLen)
	}

	// Start from the best paragraph, or one earlier for context
	startIdx := bestIdx
	if startIdx > 0 && len(paragraphs[startIdx-1]) < 100 {
		startIdx--
	}

	// Calculate byte offset by summing paragraph lengths + separators
	offset := 0
	for i := 0; i < startIdx; i++ {
		offset += len(paragraphs[i]) + len(sep)
	}

	if offset >= len(text) {
		return smartTruncate(text, maxLen)
	}

	return smartTruncate(text[offset:], maxLen)
}

// contentTermCoverage returns the fraction of terms that appear in the text.
// Used to evaluate whether a keyword search result covers the query well.
func contentTermCoverage(text string, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(text)
	matches := 0
	for _, t := range terms {
		if strings.Contains(lower, strings.ToLower(t)) {
			matches++
		}
	}
	return float64(matches) / float64(len(terms))
}

// sanitizeSnippet removes prompt injection patterns from snippet text.
// Primary detection uses go-promptguard's multi-detector (pattern matching +
// statistical analysis). The legacy string-match list is kept as a fallback
// for belt-and-suspenders defense.
func sanitizeSnippet(text string) string {
	// Primary: go-promptguard multi-detector
	if detectInjection(text) {
		return "[content filtered for security]"
	}
	// Fallback: legacy pattern matching
	lower := strings.ToLower(text)
	for _, pattern := range injectionPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return "[content filtered for security]"
		}
	}
	return text
}

// sanitizeContextTags strips XML-like tags from note content that could
// break structural wrappers (vault-context, plugin-context, session-bootstrap,
// vault-handoff, vault-decisions, same-diagnostic) and enable indirect prompt
// injection. A crafted note containing "</vault-context>\n<same-diagnostic>"
// would escape the context wrapper and inject system-level instructions.
func sanitizeContextTags(text string) string {
	// All tag names used as structural wrappers in the hook system.
	// Each pair (open + close) must be neutralized to prevent escape.
	tagNames := []string{
		"vault-context",
		"plugin-context",
		"session-bootstrap",
		"vault-handoff",
		"vault-decisions",
		"same-diagnostic",
	}

	// Case-insensitive replacement: scan character-by-character and replace
	// any matching XML open/close tag with bracket-escaped equivalents.
	lower := strings.ToLower(text)
	var result strings.Builder
	result.Grow(len(text))
	i := 0
	for i < len(text) {
		matched := false
		for _, tag := range tagNames {
			closeTag := "</" + tag + ">"
			openTag := "<" + tag + ">"
			if i+len(closeTag) <= len(text) && lower[i:i+len(closeTag)] == closeTag {
				result.WriteString("[/" + tag + "]")
				i += len(closeTag)
				matched = true
				break
			}
			if i+len(openTag) <= len(text) && lower[i:i+len(openTag)] == openTag {
				result.WriteString("[" + tag + "]")
				i += len(openTag)
				matched = true
				break
			}
		}
		if !matched {
			result.WriteByte(text[i])
			i++
		}
	}
	return result.String()
}

// titleWordRe matches word tokens in titles (letters, digits, underscores).
var titleWordRe = regexp.MustCompile(`[\w]+`)

// queryWordsForTitleMatch extracts all meaningful words from the prompt
// for title overlap matching. More permissive than extractKeyTerms —
// includes short words (3+ chars) and alphanumeric tokens — because
// title overlap scoring handles false positives via bidirectional threshold.
func queryWordsForTitleMatch() []string {
	prompt := keyTermsPrompt
	if prompt == "" {
		return nil
	}

	words := titleWordRe.FindAllString(prompt, -1)
	seen := make(map[string]bool)
	var result []string
	for _, w := range words {
		lower := strings.ToLower(w)
		if len(w) < 3 {
			// Allow meaningful 2-char terms (domain acronyms commonly found
			// in vault note titles). Skips common English 2-char words.
			if len(w) == 2 && meaningful2CharTerms[lower] {
				// keep it
			} else {
				continue
			}
		}
		if titleMatchStopWords[lower] || seen[lower] {
			continue
		}
		result = append(result, w)
		seen[lower] = true
	}
	return result
}

// titleMatchStopWords filters common English words from title matching terms.
// More comprehensive than the keyword stopWords since title matching extracts
// shorter words (3+ chars).
var titleMatchStopWords = map[string]bool{
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
	// 5+ letter (same as keyword stopWords)
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

// meaningful2CharTerms are short terms that carry domain-specific meaning
// and should be kept as title match terms despite being only 2 chars.
// These commonly appear in vault note titles (e.g., "AI Experiments Hub").
var meaningful2CharTerms = map[string]bool{
	"ai": true, "os": true, "pm": true, "qa": true,
	"ui": true, "ux": true, "hr": true, "ml": true,
	"v1": true, "v2": true, "v3": true, "v4": true, "v5": true,
}

// titleOverlapScore computes bidirectional term overlap between query terms
// and a note's title + path. Returns queryCoverage * wordCoverage in [0, 1].
//
// Words are extracted from both the title and path (directory components),
// with underscore splitting (team_roles -> team, roles) and simple plural
// matching (project <-> projects). This catches notes where the project/folder
// name contains query terms even if the filename is generic (e.g., design-brief.md).
func titleOverlapScore(queryTerms []string, title, path string) float64 {
	if len(queryTerms) == 0 {
		return 0
	}

	// Extract words from title
	allWords := titleWordRe.FindAllString(title, -1)

	// Extract words from path components (strip .md extension first)
	cleanPath := strings.TrimSuffix(path, ".md")
	for _, part := range strings.Split(cleanPath, "/") {
		allWords = append(allWords, titleWordRe.FindAllString(part, -1)...)
	}

	// Build lowercase set, splitting underscores and filtering short tokens
	wordSet := make(map[string]bool, len(allWords))
	for _, w := range allWords {
		// Split underscore-separated words: "team_roles" -> "team", "roles"
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

	// Expand hyphenated query terms: "chain-of-thought" -> ["chain","of","thought"]
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

	// Count expanded terms that match. Each wordSet entry can only be matched
	// once to prevent inflated scores when multiple query terms match the same
	// word (e.g., "prompt" and "prompting" both matching wordSet "prompting").
	// Matching cascades: exact -> plural -> edit distance 1 -> common stem.
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
			// Fuzzy matching: edit distance 1 and common root/stem
			for w := range wordSet {
				if matchedEntries[w] {
					continue
				}
				if isEditDistance1(lower, w) || sharesStem(lower, w) {
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

	// Short word sets (1-2 unique words) are noisy for single-term matches
	// because wordCoverage is trivially high. Require higher queryCoverage
	// to compensate.
	if wordCount <= 2 && queryCoverage < 0.30 {
		return 0
	}

	return queryCoverage * wordCoverage
}

// isEditDistance1 returns true if two lowercase strings differ by exactly one
// character (insertion, deletion, or substitution). Only applies to words
// >= 7 chars to avoid false positives on short words.
// Example: "kubernetes" vs "kuberntes" (one deleted char).
func isEditDistance1(a, b string) bool {
	la, lb := len(a), len(b)
	if la < 7 && lb < 7 {
		return false
	}
	diff := la - lb
	if diff < -1 || diff > 1 {
		return false
	}
	if diff == 0 {
		// Same length: check for exactly one substitution
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
	// Different length by 1: check for exactly one insertion/deletion
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
			continue // skip the extra char in longer
		}
		j++
	}
	return true
}

// sharesStem returns true if two lowercase words likely share the same root.
// Requires both words >= 5 chars and a common prefix that covers all but
// the last char of the shorter word, with at most 3 extra chars on the longer.
// Examples: "invoice"/"invoicing", "finance"/"financing", "report"/"reporting".
func sharesStem(a, b string) bool {
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
	// Find common prefix length
	common := 0
	for i := 0; i < shorter; i++ {
		if a[i] != b[i] {
			break
		}
		common++
	}
	// Common prefix must cover all but the last char of the shorter word
	return common >= shorter-1 && common >= 5
}

// overlapForSort returns the overlap score to use for sorting and gap-cap.
// Uses title-only overlap as the primary signal, but when path-inclusive
// overlap is strong (>= 0.25), provides a reduced score (half-strength)
// so path-matched notes survive gap-cap without competing with direct
// title matches. This allows notes like "design-brief.md" in well-named
// project directories to appear alongside title-matched results.
func overlapForSort(queryTerms []string, title, path string) float64 {
	titleOnly := titleOverlapScore(queryTerms, title, "")
	if titleOnly > 0 {
		return titleOnly
	}
	fullOverlap := titleOverlapScore(queryTerms, title, path)
	if fullOverlap >= 0.25 {
		return fullOverlap * 0.5
	}
	return 0
}
