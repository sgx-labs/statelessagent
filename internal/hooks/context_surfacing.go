package hooks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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
	maxResults       = 3   // data shows expected notes often land at #3; sweep confirmed no precision loss
	maxSnippetChars  = 400
	maxDistance       = 16.3 // L2 distance; relaxed from 16.0→16.2→16.3 — matches within this range are relevant; off-topic > 16.8
	minComposite     = 0.70 // composite threshold; distance gate handles negative discrimination
	minSemanticFloor = 0.25 // absolute floor: if semantic score < this, skip regardless of boost
	maxTokenBudget   = 800  // tightened from 1000; less context waste
	minTitleOverlap  = 0.10 // bidirectional overlap threshold for title matching
	highTierOverlap  = 0.199 // effective 0.20 with floating point margin (e.g., 3/5*3/9 = 0.19999...)
)

// Recency-aware weights: when query has recency intent, shift weight heavily to recency.
const (
	recencyRelWeight    = 0.1
	recencyRecWeight    = 0.7
	recencyConfWeight   = 0.2
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

// noisyPathPrefixes returns the user-configured noise path prefixes.
// Defaults to empty — no paths are filtered unless configured via
// [vault] noise_paths in config.toml or SAME_NOISE_PATHS env var.
func noisyPathPrefixes() []string {
	return config.NoisePaths()
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
	path           string
	title          string
	contentType    string
	confidence     float64
	snippet        string
	composite      float64
	semantic       float64
	distance       float64
	titleOverlap   float64
	contentBoosted bool     // true when titleOverlap was set by Mode 5 content boost
	tokens         int      // estimated tokens (set after selection)
	matchTerms     []string // terms from prompt that matched this note
}

// isVerbose checks whether verbose monitoring is active on each invocation.
// Supports mid-session toggling via flag file or env var.
func isVerbose() bool {
	return config.VerboseEnabled()
}

// ANSI escape codes for styled terminal output.
const (
	cReset  = "\033[0m"
	cDim    = "\033[2m"
	cBold   = "\033[1m"
	cCyan   = "\033[36m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
)

// Rotating vocabulary for verbose output — keeps it human and alive.
var surfaceVerbs = []string{
	"surfaced", "recalled", "unearthed", "found something",
	"remembered", "connected", "dug up", "sparked",
}

var quietVerbs = []string{
	"quiet", "nothing needed", "all good", "standing by",
	"listening", "at ease", "moving on", "got it",
}

// verbosePickIndex is a simple counter that rotates through word lists.
// Not random — deterministic rotation so consecutive prompts always differ.
var verbosePickIndex int

func pickVerb(verbs []string) string {
	v := verbs[verbosePickIndex%len(verbs)]
	verbosePickIndex++
	return v
}

// verboseLogPath returns the file path for verbose output.
func verboseLogPath() string {
	return filepath.Join(config.DataDir(), "verbose.log")
}

// pendingVerboseMsg accumulates the verbose status line for this invocation.
// Read by Run() in runner.go to attach as systemMessage in the JSON output.
var pendingVerboseMsg string

// getPendingVerboseMsg returns and clears the pending verbose message.
func getPendingVerboseMsg() string {
	msg := pendingVerboseMsg
	pendingVerboseMsg = ""
	return msg
}

// verboseDecision writes a styled decision box to both stderr and verbose.log.
// stderr may be swallowed by Claude Code, but the log file is always available
// via: tail -f .scripts/same/data/verbose.log
func verboseDecision(decision, mode string, jaccard float64, prompt string, titles []string, tokens int) {
	if !isVerbose() {
		return
	}

	snippet := prompt
	if len(snippet) > 60 {
		snippet = snippet[:60] + "…"
	}

	// Pick verb once, use for both plain and styled output
	var verb, reasonStr string
	if decision == "inject" {
		verb = pickVerb(surfaceVerbs)
	} else {
		verb = pickVerb(quietVerbs)
	}
	switch {
	case jaccard >= 0:
		reasonStr = fmt.Sprintf("%s · jaccard=%.2f", mode, jaccard)
	case mode != "":
		reasonStr = mode
	default:
		reasonStr = decision
	}

	// --- Dual output: systemMessage (inject only) + stderr (Ctrl+O verbose) ---

	// 1. systemMessage: only for injects. Skips are silent — no noise, no error look.
	if decision == "inject" {
		pendingVerboseMsg = fmt.Sprintf("✦ %s — %d notes · ~%d tokens: %s",
			verb, len(titles), tokens, strings.Join(titles, ", "))
	}

	// 2. stderr: ANSI-styled box, visible when Ctrl+O verbose is expanded.
	//    Shows both inject and skip for debugging/monitoring.
	if decision == "inject" {
		fmt.Fprintf(os.Stderr, "\n%s╭─%s %sSAME%s %s─────────────────────────────────╮%s\n",
			cDim+cCyan, cReset, cBold+cCyan, cReset, cDim+cCyan, cReset)
		fmt.Fprintf(os.Stderr, "%s│%s  %s✦%s  %s%s%s — %d notes · ~%d tokens\n",
			cDim+cCyan, cReset, cGreen, cReset, cBold+cGreen, verb, cReset, len(titles), tokens)
		for i, t := range titles {
			conn := "├"
			if i == len(titles)-1 {
				conn = "└"
			}
			fmt.Fprintf(os.Stderr, "%s│%s      %s%s %s%s\n",
				cDim+cCyan, cReset, cDim, conn, t, cReset)
		}
		fmt.Fprintf(os.Stderr, "%s╰─────────────── %ssame verbose off%s %sto disable%s ─╯%s\n",
			cDim+cCyan, cCyan, cReset, cDim+cCyan, cDim+cCyan, cReset)
	} else {
		fmt.Fprintf(os.Stderr, "%s╭─%s %sSAME%s %s─╮%s %s·%s %s%s%s\n",
			cDim+cCyan, cReset, cBold+cCyan, cReset, cDim+cCyan, cReset,
			cDim, cReset, cDim+cYellow, verb, cReset)
	}

	// --- Styled box to log file (visible via same verbose watch) ---
	b := cDim + cCyan
	r := cReset
	var buf strings.Builder

	fmt.Fprintf(&buf, "\n%s╭─%s %sSAME%s %s────────────────────────────────────────────╮%s\n",
		b, r, cBold+cCyan, r, b, r)

	if decision == "inject" {
		fmt.Fprintf(&buf, "%s│%s  %s✦%s  %s%s%s%s (%s) · %d notes · ~%d tokens\n",
			b, r, cGreen, r, cBold, cGreen, verb, r, mode, len(titles), tokens)
		for i, t := range titles {
			conn := "├"
			if i == len(titles)-1 {
				conn = "└"
			}
			fmt.Fprintf(&buf, "%s│%s      %s%s %s%s\n", b, r, cDim, conn, t, r)
		}
	} else {
		fmt.Fprintf(&buf, "%s│%s  %s·%s  %s%s%s%s (%s): %q\n",
			b, r, cDim, r, cDim, cYellow, verb, r, reasonStr, snippet)
	}

	fmt.Fprintf(&buf, "%s╰──────────────────── %ssame verbose off%s %sto disable %s─╯%s\n",
		b, cCyan, r, b, b, r)

	writeVerboseLog(buf.String())
}

// writeVerboseLog appends content to the verbose log file with size-based rotation.
// If the log exceeds 5MB, it keeps only the last ~1MB before appending.
// Uses 0o600 permissions (owner-only) since the log may contain prompt snippets.
func writeVerboseLog(content string) {
	logPath := verboseLogPath()

	const maxSize = 5 * 1024 * 1024  // 5MB
	const keepSize = 1 * 1024 * 1024  // 1MB

	// Check if rotation is needed
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxSize {
		data, err := os.ReadFile(logPath)
		if err == nil && len(data) > keepSize {
			// Keep the last 1MB
			truncated := data[len(data)-keepSize:]
			// Find the first newline to avoid splitting mid-line
			if idx := bytes.IndexByte(truncated, '\n'); idx >= 0 {
				truncated = truncated[idx+1:]
			}
			os.WriteFile(logPath, truncated, 0o600)
		}
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.WriteString(content)
	f.Close()
}

// logDecision records a context surfacing gate decision to the DB
// and emits styled verbose output to stderr when verbose is enabled.
func logDecision(db *store.DB, sessionID, prompt, mode string, jaccard float64, decision string, paths []string) {
	snippet := prompt
	if len(snippet) > 80 {
		snippet = snippet[:80]
	}

	// Styled verbose output (inject is handled separately with titles/tokens)
	if decision != "inject" {
		verboseDecision(decision, mode, jaccard, prompt, nil, 0)
	}

	if sessionID == "" {
		return
	}
	_ = db.InsertDecision(&store.DecisionRecord{
		SessionID:     sessionID,
		PromptSnippet: snippet,
		Mode:          mode,
		JaccardScore:  jaccard,
		Decision:      decision,
		InjectedPaths: paths,
	})
}

// runContextSurfacing embeds the user's prompt, searches the vault,
// and injects relevant context.
func runContextSurfacing(db *store.DB, input *HookInput) *HookOutput {
	prompt := input.Prompt
	if len(prompt) < minPromptChars {
		logDecision(db, input.SessionID, prompt, "", -1, "skip_short", nil)
		return nil
	}

	// Skip slash commands
	if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
		logDecision(db, input.SessionID, prompt, "", -1, "skip_slash", nil)
		return nil
	}

	// Skip conversational/social prompts that don't need vault context
	if isConversational(prompt) {
		logDecision(db, input.SessionID, prompt, "socializing", -1, "skip_conversational", nil)
		return nil
	}

	// Check display mode from config, with env var override
	displayMode := config.DisplayMode() // "full", "compact", or "quiet"
	if os.Getenv("SAME_QUIET") == "1" || os.Getenv("SAME_QUIET") == "true" {
		displayMode = "quiet"
	} else if os.Getenv("SAME_COMPACT") == "1" || os.Getenv("SAME_COMPACT") == "true" {
		displayMode = "compact"
	}
	quietMode := displayMode == "quiet"
	compactMode := displayMode == "compact"

	isRecency := memory.HasRecencyIntent(prompt)

	// Set prompt early for term extraction (used by low-signal gate and search)
	keyTermsPrompt = prompt

	// Skip low-signal prompts: if term extraction finds no specific terms
	// (acronyms, quoted phrases, hyphenated) and at most 1 broad term
	// (5+ char non-stop words), the prompt lacks enough domain signal
	// for a meaningful vault search. Recency queries bypass this gate
	// since temporal intent ("what did I work on") is the signal.
	if !isRecency && hasLowSignal() {
		logDecision(db, input.SessionID, prompt, "", -1, "skip_lowsignal", nil)
		return nil
	}

	// --- Decision matrix: mode × topic change ---
	mode := detectMode(prompt)

	if !isRecency {
		switch mode {
		case ModeExecuting:
			logDecision(db, input.SessionID, prompt, mode.String(), -1, "skip_executing", nil)
			return nil
		case ModeSocializing:
			logDecision(db, input.SessionID, prompt, mode.String(), -1, "skip_socializing", nil)
			return nil
		default:
			// Exploring, Deepening, Reflecting: check topic change
			if input.SessionID != "" && !isTopicChange(db, input.SessionID) {
				jScore := topicChangeScore(db, input.SessionID)
				logDecision(db, input.SessionID, prompt, mode.String(), jScore, "skip_sametopic", nil)
				return nil // Same topic — context already in conversation window
			}
		}
	}

	// Clean up stale session state (runs opportunistically, ~0ms for small tables)
	_ = db.SessionStateCleanup(86400) // 24 hours

	// Get total vault note count for display
	totalVault, _ := db.NoteCount()

	// Embed the prompt
	embedProvider, err := newEmbedProvider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: embedding provider: %v\n", err)
		return &HookOutput{
			HookSpecificOutput: &HookSpecific{
				HookEventName:     "UserPromptSubmit",
				AdditionalContext: diagNoEmbed,
			},
		}
	}
	queryVec, err := embedProvider.GetQueryEmbedding(prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: embed query failed: %v\n", err)
		return &HookOutput{
			HookSpecificOutput: &HookSpecific{
				HookEventName:     "UserPromptSubmit",
				AdditionalContext: diagNoEmbed,
			},
		}
	}

	var candidates []scored

	if isRecency {
		candidates = recencyHybridSearch(db, queryVec)
	} else {
		candidates = standardSearch(db, queryVec)
	}

	// If no candidates found, show empty state (unless quiet)
	if len(candidates) == 0 {
		if !quietMode {
			cli.SurfacingEmpty(totalVault)
		}
		return nil
	}

	// Inject pinned notes: always surface them regardless of search results.
	// They're prepended so they survive the effectiveMax cap.
	{
		pinnedRecords, _ := db.GetPinnedNotes()
		for _, rec := range pinnedRecords {
			// Skip if already in candidates
			alreadyPresent := false
			for _, c := range candidates {
				if c.path == rec.Path {
					alreadyPresent = true
					break
				}
			}
			if alreadyPresent {
				continue
			}
			// SECURITY: never auto-surface _PRIVATE/ content
			if shouldSkipPath(rec.Path) {
				continue
			}
			// Prepend with high score so pinned notes survive trimming
			pinned := scored{
				path:         rec.Path,
				title:        rec.Title,
				contentType:  rec.ContentType,
				confidence:   rec.Confidence,
				snippet:      rec.Text,
				composite:    1.0,
				titleOverlap: 1.0, // prevent overlap-based trimming
			}
			candidates = append([]scored{pinned}, candidates...)
		}
	}

	// Extract match terms from prompt for display
	promptTerms := extractDisplayTerms(prompt)

	effectiveMax := maxResults
	if isRecency {
		effectiveMax = recencyMaxResults
	}
	// When the best candidate lacks strong genuine title overlap, the
	// query is ambiguous and extra results are more likely to be noise.
	// Reduce to 2 to improve precision without losing coverage on the
	// best match. Content-boosted overlap (from Mode 5 rescue) is
	// artificial and treated as weak for this ambiguity check.
	{
		topOverlap := float64(0)
		if len(candidates) > 0 {
			topOverlap = candidates[0].titleOverlap
			if candidates[0].contentBoosted {
				topOverlap = 0
			}
		}
		if !isRecency && topOverlap < highTierOverlap && effectiveMax > 2 {
			effectiveMax = 2
		}
	}
	if len(candidates) > effectiveMax {
		candidates = candidates[:effectiveMax]
	}

	// Zero-overlap trim: when the top result has weak but positive title
	// relevance, trailing zero-overlap results are likely noise from
	// vector search (semantically similar but not about the specific topic).
	// Content-boosted overlap is treated as zero for this check.
	if len(candidates) > 1 {
		leaderOverlap := candidates[0].titleOverlap
		if candidates[0].contentBoosted {
			leaderOverlap = 0
		}
		if leaderOverlap > 0 && leaderOverlap < highTierOverlap {
			for i := 1; i < len(candidates); i++ {
				if candidates[i].titleOverlap <= 0 {
					candidates = candidates[:i]
					break
				}
			}
		}
	}

	// Overlap gap cap: trim results that are significantly weaker than the
	// best match. Uses a relative threshold (< 65% of best overlap) to
	// trim weak matches that would dilute precision. Skipped when the top
	// result's overlap came from Mode 5 content boost (not a genuine
	// title match) — applying the gap cap would incorrectly trim
	// priority-type candidates (hub, decision, handoff) that the content
	// result was meant to complement.
	if len(candidates) > 1 && candidates[0].titleOverlap >= highTierOverlap && !candidates[0].contentBoosted {
		relThreshold := candidates[0].titleOverlap * 0.65
		if relThreshold < 0.10 {
			relThreshold = 0.10
		}
		for i := 1; i < len(candidates); i++ {
			if candidates[i].titleOverlap < relThreshold {
				candidates = candidates[:i]
				break
			}
		}
	}

	// Build context string, capped at token budget
	// Track which candidates are included vs excluded
	var parts []string
	var included []scored
	var excluded []scored
	totalTokens := 0

	for i := range candidates {
		var entry string
		if candidates[i].snippet != "" {
			entry = fmt.Sprintf("**%s** (%s, score: %.3f)\n%s\n%s",
				candidates[i].title, candidates[i].contentType, candidates[i].composite,
				candidates[i].path, candidates[i].snippet)
		} else {
			entry = fmt.Sprintf("**%s** (%s, score: %.3f)\n%s",
				candidates[i].title, candidates[i].contentType, candidates[i].composite,
				candidates[i].path)
		}
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

	// Sanitize: strip XML-like closing tags that could break the wrapper
	// and allow indirect prompt injection via crafted note content.
	contextText = sanitizeContextTags(contextText)

	// Log the injection for budget tracking
	if input.SessionID != "" {
		memory.LogInjection(db, input.SessionID, "context_surfacing", injectedPaths, contextText)
	}

	// Store current topic terms so the next prompt can detect topic changes
	storeTopicTerms(db, input.SessionID)

	// Log the inject decision with styled verbose output
	logDecision(db, input.SessionID, prompt, mode.String(), -1, "inject", injectedPaths)
	var titles []string
	for _, s := range included {
		titles = append(titles, s.title)
	}
	verboseDecision("inject", mode.String(), -1, prompt, titles, totalTokens)

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

// keyTermsPrompt stores the current prompt for keyword extraction.
// Set before calling standardSearch.
var keyTermsPrompt string

// extractKeyTerms pulls meaningful search terms from the current prompt.
// Returns two slices: specific (high-signal: acronyms, quoted, hyphenated)
// and broad (individual words). Keyword fallback only triggers on specific terms
// to avoid false positives on novel/generic queries.
func extractKeyTerms() (specific []string, broad []string) {
	prompt := keyTermsPrompt
	if prompt == "" {
		return nil, nil
	}

	seen := make(map[string]bool)

	// Extract quoted phrases -> specific
	quotedRe := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range quotedRe.FindAllStringSubmatch(prompt, -1) {
		t := strings.TrimSpace(m[1])
		if len(t) >= 2 && !seen[strings.ToLower(t)] {
			specific = append(specific, t)
			seen[strings.ToLower(t)] = true
		}
	}

	// Extract uppercase acronyms (2+ chars) -> specific
	// Skip common tech acronyms that appear in generic programming questions
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

	// Extract hyphenated terms -> specific
	// Skip common generic hyphenated patterns (e.g., "three-way", "real-time")
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

	// Extract significant individual words (5+ chars) -> broad
	// These supplement specific terms but don't trigger keyword fallback alone
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

// commonTechAcronyms are generic technology acronyms that should not trigger
// keyword fallback. These appear frequently in programming questions unrelated
// to vault content.
var commonTechAcronyms = map[string]bool{
	// Protocols & networking
	"tcp": true, "udp": true, "http": true, "https": true, "ssh": true,
	"ftp": true, "dns": true, "ssl": true, "tls": true, "ip": true,
	"smtp": true, "imap": true, "grpc": true, "ws": true, "wss": true,
	// Data formats & languages
	"json": true, "xml": true, "csv": true, "html": true, "css": true,
	"sql": true, "yaml": true, "toml": true, "svg": true, "pdf": true,
	// Cloud & infrastructure
	"aws": true, "gcp": true, "vpc": true, "cdn": true, "iam": true,
	"ec2": true, "ecs": true, "eks": true, "rds": true, "sqs": true,
	"sns": true, "alb": true, "elb": true, "nat": true,
	// Dev tools & concepts
	"cli": true, "sdk": true, "ide": true, "jwt": true, "url": true,
	"uri": true, "dom": true, "orm": true, "oop": true, "tdd": true,
	"bdd": true, "ddd": true, "mvp": true, "mvc": true, "gpu": true,
	"cpu": true, "ram": true, "ssd": true, "hdd": true, "ci": true,
	"cd": true, "os": true, "vm": true,
	// REST is a special case — common in "REST API" questions
	"rest": true,
}

// isConversational returns true if the prompt is a social/conversational
// message that doesn't warrant vault context injection. Only matches when
// the entire normalized prompt is conversational — mixed prompts like
// "thanks, now show me the health data" are NOT considered conversational.
func isConversational(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	// Strip trailing punctuation for matching
	normalized = strings.TrimRight(normalized, ".!?,;:")

	// Exact match against known conversational phrases
	if conversationalPhrases[normalized] {
		return true
	}

	// Short prompts (< 5 words) where every word is conversational
	words := strings.Fields(normalized)
	if len(words) <= 5 {
		allConv := true
		for _, w := range words {
			w = strings.TrimRight(w, ".!?,;:")
			if !conversationalWords[w] {
				allConv = false
				break
			}
		}
		if allConv {
			return true
		}
	}

	return false
}

var conversationalPhrases = map[string]bool{
	"hi": true, "hey": true, "hello": true, "howdy": true,
	"thanks": true, "thank you": true, "thanks a lot": true, "thank you so much": true,
	"ok": true, "okay": true, "k": true, "got it": true, "understood": true,
	"yes": true, "yeah": true, "yep": true, "yup": true, "no": true, "nah": true, "nope": true,
	"sure": true, "sure thing": true, "of course": true,
	"great": true, "perfect": true, "awesome": true, "nice": true, "cool": true,
	"sounds good": true, "looks good": true, "that works": true, "works for me": true,
	"good job": true, "great job": true, "well done": true, "nice work": true,
	"go ahead": true, "go on": true, "continue": true, "proceed": true, "next": true,
	"i see": true, "makes sense": true, "fair enough": true,
	"good morning": true, "good afternoon": true, "good evening": true, "good night": true,
	"bye": true, "goodbye": true, "see you": true, "later": true, "cheers": true,
	"please continue": true, "please proceed": true, "please go ahead": true,
	"lgtm": true, "sgtm": true, "ty": true, "thx": true, "np": true, "yw": true,
}

var conversationalWords = map[string]bool{
	"hi": true, "hey": true, "hello": true, "howdy": true,
	"thanks": true, "thank": true, "you": true, "so": true, "much": true, "a": true, "lot": true,
	"ok": true, "okay": true, "got": true, "it": true,
	"yes": true, "yeah": true, "yep": true, "yup": true, "no": true, "nah": true, "nope": true,
	"sure": true, "great": true, "perfect": true, "awesome": true, "nice": true, "cool": true,
	"good": true, "sounds": true, "looks": true, "that": true, "works": true, "for": true, "me": true,
	"go": true, "ahead": true, "on": true, "continue": true, "proceed": true, "next": true,
	"please": true, "see": true, "makes": true, "sense": true, "fair": true, "enough": true,
	"morning": true, "afternoon": true, "evening": true, "night": true,
	"bye": true, "goodbye": true, "later": true, "cheers": true,
	"lgtm": true, "sgtm": true, "ty": true, "thx": true, "np": true, "yw": true,
	"well": true, "done": true, "job": true, "work": true,
}

// hasLowSignal returns true when the prompt lacks enough domain-specific
// terms to warrant a vault search. Requires keyTermsPrompt to be set.
// Returns true when there are 0 specific terms (acronyms, quoted phrases,
// hyphenated) AND at most 1 broad term (5+ char non-stop words).
func hasLowSignal() bool {
	specific, broad := extractKeyTerms()
	return len(specific) == 0 && len(broad) <= 1
}

// commonHyphenated are generic hyphenated terms that should not trigger
// keyword fallback. These appear in general tech discussion.
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

// sanitizeContextTags strips XML-like closing tags from note content that
// could break the <vault-context> wrapper and enable indirect prompt
// injection. A crafted note containing "</vault-context>" would cause the
// AI to interpret subsequent note content as system-level instructions.
func sanitizeContextTags(text string) string {
	// Case-insensitive replacement of closing tags for our wrapper elements
	lower := strings.ToLower(text)
	var result strings.Builder
	result.Grow(len(text))
	i := 0
	for i < len(text) {
		if i+len("</vault-context>") <= len(text) && lower[i:i+len("</vault-context>")] == "</vault-context>" {
			result.WriteString("[/vault-context]")
			i += len("</vault-context>")
			continue
		}
		if i+len("</plugin-context>") <= len(text) && lower[i:i+len("</plugin-context>")] == "</plugin-context>" {
			result.WriteString("[/plugin-context]")
			i += len("</plugin-context>")
			continue
		}
		if i+len("<vault-context>") <= len(text) && lower[i:i+len("<vault-context>")] == "<vault-context>" {
			result.WriteString("[vault-context]")
			i += len("<vault-context>")
			continue
		}
		result.WriteByte(text[i])
		i++
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
