package hooks

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

const (
	minPromptChars   = 20
	maxSnippetChars  = 300
	minSemanticFloor = 0.25 // absolute floor: if semantic score < this, skip regardless of boost
)

// Recency-aware weights: when query has recency intent, shift weight heavily to recency.
const (
	recencyRelWeight  = 0.1
	recencyRecWeight  = 0.7
	recencyConfWeight = 0.2
	recencyMinComposite = 0.45 // lower threshold since semantic score may be weak
	recencyMaxResults   = 3    // show more results for "what did I work on" queries
)

var priorityTypes = map[string]bool{
	"handoff":  true,
	"decision": true,
	"research": true,
	"hub":      true,
}

// SECURITY: Paths that must never be auto-surfaced via hooks.
// _PRIVATE/ contains client-sensitive content. Defense-in-depth:
// indexer also skips these, but we filter here in case of stale index data.
const privateDirPrefix = "_PRIVATE/"

// Noise filter: paths that produce low-value context surfacing results.
// Experiment raw outputs contain broad vault-related discussions that
// semantically match almost any query, drowning out actual reference notes.
var noisyPathPrefixes = []string{
	"experiments/",  // PE lab raw outputs (broad vault discussions)
}

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

type scored struct {
	path        string
	title       string
	contentType string
	confidence  float64
	snippet     string
	composite   float64
	semantic    float64
	distance    float64
	tokens      int      // estimated tokens (set after selection)
	matchTerms  []string // terms from prompt that matched this note
}

// runContextSurfacing embeds the user's prompt, searches the vault,
// and injects relevant context.
func runContextSurfacing(db *store.DB, input *HookInput) *HookOutput {
	prompt := input.Prompt
	if len(prompt) < minPromptChars {
		return nil
	}

	// Skip slash commands
	if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
		return nil
	}

	// Check display mode: SAME_QUIET suppresses output, SAME_COMPACT shows one-liner
	// Default is full box (verbose)
	quietMode := os.Getenv("SAME_QUIET") == "1" || os.Getenv("SAME_QUIET") == "true"
	compactMode := os.Getenv("SAME_COMPACT") == "1" || os.Getenv("SAME_COMPACT") == "true"

	// Load configurable memory parameters once per invocation
	maxResults := config.MemoryMaxResults()
	maxDistance := config.MemoryDistanceThreshold()
	minComposite := config.MemoryCompositeThreshold()
	maxTokenBudget := config.MemoryMaxTokenBudget()

	isRecency := memory.HasRecencyIntent(prompt)

	// Embed the prompt
	embedProvider, err := newEmbedProvider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: embedding provider: %v\n", err)
		return nil
	}
	queryVec, err := embedProvider.GetQueryEmbedding(prompt)
	if err != nil {
		return nil
	}

	// Set prompt for keyword extraction fallback
	keyTermsPrompt = prompt

	// Get total vault note count for display
	totalVault, _ := db.NoteCount()

	var candidates []scored

	if isRecency {
		candidates = recencyHybridSearch(db, queryVec, maxDistance)
	} else {
		candidates = standardSearch(db, queryVec, maxDistance, minComposite)
	}

	// If no candidates found, show empty state (unless quiet)
	if len(candidates) == 0 {
		if !quietMode {
			cli.SurfacingEmpty(totalVault)
		}
		return nil
	}

	// Extract match terms from prompt for display
	promptTerms := extractDisplayTerms(prompt)

	effectiveMax := maxResults
	if isRecency {
		effectiveMax = recencyMaxResults
	}
	if len(candidates) > effectiveMax {
		candidates = candidates[:effectiveMax]
	}

	// Build context string, capped at token budget
	// Track which candidates are included vs excluded
	var parts []string
	var included []scored
	var excluded []scored
	totalTokens := 0

	for i := range candidates {
		entry := fmt.Sprintf("**%s** (%s, score: %.3f)\n%s\n%s",
			candidates[i].title, candidates[i].contentType, candidates[i].composite,
			candidates[i].path, candidates[i].snippet)
		entryTokens := memory.EstimateTokens(entry)

		// Find which prompt terms appear in this note's title/snippet
		candidates[i].matchTerms = findMatchingTerms(promptTerms, candidates[i].title, candidates[i].snippet)

		if totalTokens+entryTokens > maxTokenBudget {
			excluded = append(excluded, candidates[i])
			continue
		}
		candidates[i].tokens = entryTokens
		parts = append(parts, entry)
		included = append(included, candidates[i])
		totalTokens += entryTokens
	}

	if len(parts) == 0 {
		if !quietMode {
			cli.SurfacingEmpty(totalVault)
		}
		return nil
	}

	// Display surfacing feedback to stderr
	if !quietMode {
		if compactMode {
			// Compact mode (one-liner)
			cli.SurfacingCompact(len(included), len(candidates))
		} else {
			// Full box (default)
			var displayNotes []cli.SurfacedNote
			for _, s := range included {
				displayNotes = append(displayNotes, cli.SurfacedNote{
					Title:      s.title,
					Tokens:     s.tokens,
					Included:   true,
					HighConf:   s.semantic >= 0.7, // high confidence threshold
					MatchTerms: s.matchTerms,
				})
			}
			for _, s := range excluded {
				displayNotes = append(displayNotes, cli.SurfacedNote{
					Title:      s.title,
					Tokens:     0,
					Included:   false,
					HighConf:   false,
					MatchTerms: s.matchTerms,
				})
			}
			cli.SurfacingVerbose(displayNotes, totalVault)
		}
	}

	// Collect injected paths for usage tracking
	var injectedPaths []string
	for _, s := range included {
		injectedPaths = append(injectedPaths, s.path)
	}

	contextText := strings.Join(parts, "\n---\n")

	// Log the injection for budget tracking
	if input.SessionID != "" {
		memory.LogInjection(db, input.SessionID, "context_surfacing", injectedPaths, contextText)
	}

	return &HookOutput{
		HookSpecificOutput: &HookSpecific{
			HookEventName: "UserPromptSubmit",
			AdditionalContext: fmt.Sprintf(
				"\n<vault-context>\nRelevant vault notes for this prompt:\n\n%s\n</vault-context>\n",
				contextText,
			),
		},
	}
}

// extractDisplayTerms extracts meaningful terms from the prompt for display purposes.
// Returns short, recognizable terms that users will understand.
func extractDisplayTerms(prompt string) []string {
	var terms []string
	seen := make(map[string]bool)

	// Extract quoted phrases
	quotedRe := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range quotedRe.FindAllStringSubmatch(prompt, -1) {
		t := strings.TrimSpace(m[1])
		lower := strings.ToLower(t)
		if len(t) >= 2 && !seen[lower] {
			terms = append(terms, t)
			seen[lower] = true
		}
	}

	// Extract significant words (4+ chars, skip common words)
	wordRe := regexp.MustCompile(`\b[a-zA-Z]{4,}\b`)
	for _, m := range wordRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if displayStopWords[lower] || seen[lower] {
			continue
		}
		terms = append(terms, m)
		seen[lower] = true
		if len(terms) >= 5 { // cap at 5 terms for display
			break
		}
	}

	return terms
}

// findMatchingTerms returns which prompt terms appear in the note content.
func findMatchingTerms(promptTerms []string, title, snippet string) []string {
	content := strings.ToLower(title + " " + snippet)
	var matched []string
	for _, term := range promptTerms {
		if strings.Contains(content, strings.ToLower(term)) {
			matched = append(matched, term)
		}
		if len(matched) >= 3 { // cap at 3 for display
			break
		}
	}
	return matched
}

// displayStopWords are common words to skip in match term extraction.
var displayStopWords = map[string]bool{
	"what": true, "when": true, "where": true, "which": true, "while": true,
	"with": true, "would": true, "could": true, "should": true, "about": true,
	"after": true, "before": true, "being": true, "between": true, "both": true,
	"each": true, "from": true, "have": true, "having": true, "here": true,
	"into": true, "just": true, "like": true, "make": true, "more": true,
	"most": true, "need": true, "only": true, "other": true, "over": true,
	"same": true, "some": true, "such": true, "than": true, "that": true,
	"their": true, "them": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "through": true, "under": true,
	"very": true, "want": true, "were": true, "will": true, "your": true,
	"also": true, "been": true, "does": true, "done": true, "going": true,
	"help": true, "know": true, "look": true, "many": true, "much": true,
	"show": true, "tell": true, "think": true, "using": true, "work": true,
}

// standardSearch performs vector search with keyword fallback.
func standardSearch(db *store.DB, queryVec []float32, maxDistance, minComposite float64) []scored {
	// Fetch extra candidates to compensate for path filtering (experiments, _PRIVATE)
	maxResults := config.MemoryMaxResults()
	raw, err := db.VectorSearchRaw(queryVec, maxResults*15)
	vectorEmpty := err != nil || len(raw) == 0 || raw[0].Distance > maxDistance

	var candidates []scored

	if !vectorEmpty {
		deduped := dedup(raw)
		if len(deduped) > 0 {
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

				candidates = append(candidates, makeScored(r, comp, semScore))
			}
		}
	}

	// Keyword fallback disabled: eval showed net-negative results (0 wins, 2 losses
	// across 50 test cases). Keyword matching on broad terms surfaced irrelevant notes
	// that vector search correctly rejected. The keyword search infrastructure is
	// preserved in store/search.go for future use if a more targeted approach is found.

	if len(candidates) == 0 {
		return nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		iPri := priorityTypes[candidates[i].contentType]
		jPri := priorityTypes[candidates[j].contentType]
		if iPri != jPri {
			return iPri
		}
		return candidates[i].composite > candidates[j].composite
	})

	return candidates
}

// keyTermsPrompt stores the current prompt for keyword extraction.
var keyTermsPrompt string

// extractKeyTerms pulls meaningful search terms from the current prompt.
// Returns two slices: specific (high-signal: acronyms, quoted, hyphenated)
// and broad (individual words).
func extractKeyTerms() (specific []string, broad []string) {
	prompt := keyTermsPrompt
	if prompt == "" {
		return nil, nil
	}

	seen := make(map[string]bool)

	// Extract quoted phrases → specific
	quotedRe := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range quotedRe.FindAllStringSubmatch(prompt, -1) {
		t := strings.TrimSpace(m[1])
		if len(t) >= 2 && !seen[strings.ToLower(t)] {
			specific = append(specific, t)
			seen[strings.ToLower(t)] = true
		}
	}

	// Extract uppercase acronyms (2+ chars) → specific
	acronymRe := regexp.MustCompile(`\b[A-Z]{2,}\b`)
	for _, m := range acronymRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if lower == "the" || lower == "and" || lower == "for" || lower == "not" || lower == "api" {
			continue
		}
		if commonTechAcronyms[lower] {
			continue
		}
		if !seen[lower] {
			specific = append(specific, m)
			seen[lower] = true
		}
	}

	// Extract hyphenated terms → specific
	hyphenRe := regexp.MustCompile(`\b\w+-\w+(?:-\w+)*\b`)
	for _, m := range hyphenRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if commonHyphenated[lower] {
			continue
		}
		if !seen[lower] {
			specific = append(specific, m)
			seen[lower] = true
		}
	}

	// Extract significant individual words (5+ chars) → broad
	wordRe := regexp.MustCompile(`\b[a-zA-Z]{5,}\b`)
	for _, m := range wordRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if stopWords[lower] || seen[lower] {
			continue
		}
		broad = append(broad, m)
		seen[lower] = true
	}

	return specific, broad
}

var stopWords = map[string]bool{
	"about": true, "above": true, "after": true, "again": true, "being": true,
	"below": true, "between": true, "could": true, "doing": true, "during": true,
	"every": true, "found": true, "going": true, "having": true, "might": true,
	"never": true, "other": true, "should": true, "their": true, "there": true,
	"these": true, "thing": true, "think": true, "those": true, "under": true,
	"until": true, "using": true, "where": true, "which": true, "while": true,
	"would": true, "write": true, "yours": true, "really": true, "please": true,
	"right": true, "since": true, "still": true, "today": true,
}

var commonTechAcronyms = map[string]bool{
	"tcp": true, "udp": true, "http": true, "https": true, "ssh": true,
	"ftp": true, "dns": true, "ssl": true, "tls": true, "ip": true,
	"smtp": true, "imap": true, "grpc": true, "ws": true, "wss": true,
	"json": true, "xml": true, "csv": true, "html": true, "css": true,
	"sql": true, "yaml": true, "toml": true, "svg": true, "pdf": true,
	"aws": true, "gcp": true, "vpc": true, "cdn": true, "iam": true,
	"ec2": true, "ecs": true, "eks": true, "rds": true, "sqs": true,
	"sns": true, "alb": true, "elb": true, "nat": true,
	"cli": true, "sdk": true, "ide": true, "jwt": true, "url": true,
	"uri": true, "dom": true, "orm": true, "oop": true, "tdd": true,
	"bdd": true, "ddd": true, "mvp": true, "mvc": true, "gpu": true,
	"cpu": true, "ram": true, "ssd": true, "hdd": true, "ci": true,
	"cd": true, "os": true, "vm": true,
	"rest": true,
}

var commonHyphenated = map[string]bool{
	"three-way": true, "two-way": true, "one-way": true,
	"real-time": true, "non-blocking": true, "multi-threaded": true,
	"single-threaded": true, "open-source": true, "built-in": true,
	"read-only": true, "read-write": true, "write-only": true,
	"end-to-end": true, "peer-to-peer": true, "point-to-point": true,
	"client-side": true, "server-side": true, "front-end": true,
	"back-end": true, "full-stack": true, "high-level": true,
	"low-level": true, "long-running": true, "well-known": true,
	"re-use": true, "re-run": true, "pre-built": true,
}

// recencyHybridSearch merges vector results with time-sorted results.
// Uses recency-heavy weights and includes recently modified notes even
// if they aren't strong semantic matches.
func recencyHybridSearch(db *store.DB, queryVec []float32, maxDistance float64) []scored {
	// Get vector search results (relaxed distance threshold)
	raw, err := db.VectorSearchRaw(queryVec, recencyMaxResults*15)
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
			snippet := smartTruncate(n.Text, maxSnippetChars)
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

	// Collect and sort by session-relevance priority, then composite.
	// Handoff/session notes rank above generic hubs for recency queries.
	var candidates []scored
	for _, s := range candidateMap {
		candidates = append(candidates, *s)
	}

	sort.Slice(candidates, func(i, j int) bool {
		iPri := recencyPriority(candidates[i].path)
		jPri := recencyPriority(candidates[j].path)
		if iPri != jPri {
			return iPri < jPri
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

func distRange(results []store.RawSearchResult) (float64, float64) {
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
	snippet := smartTruncate(r.Text, maxSnippetChars)
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

// isNoisyPath returns true if the path is a known source of low-value matches.
func isNoisyPath(path string) bool {
	for _, prefix := range noisyPathPrefixes {
		if strings.Contains(path, prefix) {
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

// smartTruncate truncates text at a sentence or paragraph boundary near maxLen.
// Falls back to word boundary if no sentence break is found.
func smartTruncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}

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
