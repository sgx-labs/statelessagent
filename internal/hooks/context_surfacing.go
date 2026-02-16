package hooks

import (
	"fmt"
	"os"
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
	// maxPerNoteTokens caps any single note's contribution to the token budget.
	// Prevents a large note from consuming the entire budget and crowding out
	// other relevant results. At 400 tokens (~1600 chars), even a 10K-char
	// note will leave room for 1-2 more results within the 800 token budget.
	maxPerNoteTokens = 400
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

	// Embed the prompt — keyword fallback if provider unavailable
	var candidates []scored
	embedProvider, err := newEmbedProvider()
	if err != nil {
		// No embedding provider — fall through to keyword search
		fmt.Fprintf(os.Stderr, "same: no embedding provider, using keyword search\n")
		writeVerboseLog(fmt.Sprintf("Embed provider error: %v — keyword fallback\n", err))
		candidates = keywordFallbackSearch(db)
	} else {
		// Check for embedding model/dimension mismatch before searching
		if mismatchErr := db.CheckEmbeddingMeta(embedProvider.Name(), embedProvider.Model(), embedProvider.Dimensions()); mismatchErr != nil {
			fmt.Fprintf(os.Stderr, "same: embedding model changed — run 'same reindex --force' to rebuild\n")
			return &HookOutput{
				HookSpecificOutput: &HookSpecific{
					HookEventName: "UserPromptSubmit",
					AdditionalContext: fmt.Sprintf(`<same-diagnostic>
%s
Suggested actions for the user:
- Run "same reindex --force" to rebuild the index with the current embedding model
</same-diagnostic>`, mismatchErr),
				},
			}
		}

		queryVec, embedErr := embedProvider.GetQueryEmbedding(prompt)
		if embedErr != nil {
			// Classify the error for better debugging
			errMsg := embedErr.Error()
			switch {
			case strings.Contains(errMsg, "connection_refused"):
				fmt.Fprintf(os.Stderr, "same: Ollama not running, falling back to keyword search\n")
			case strings.Contains(errMsg, "permission_denied"):
				fmt.Fprintf(os.Stderr, "same: Ollama blocked by sandbox policy, falling back to keyword search\n")
			case strings.Contains(errMsg, "timeout"):
				fmt.Fprintf(os.Stderr, "same: Ollama timeout (model loading?), falling back to keyword search\n")
			default:
				fmt.Fprintf(os.Stderr, "same: embedding failed, falling back to keyword search\n")
			}
			writeVerboseLog(fmt.Sprintf("Embedding failed: %v — keyword fallback\n", embedErr))
			candidates = keywordFallbackSearch(db)
		} else if isRecency {
			candidates = recencyHybridSearch(db, queryVec)
		} else {
			candidates = standardSearch(db, queryVec)
		}
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

	// Build context string, capped at token budget.
	// Continue past oversized candidates — a large note that doesn't fit
	// shouldn't prevent smaller, high-relevance notes behind it from being included.
	var parts []string
	var included []scored
	var excluded []scored
	totalTokens := 0

	for i := range candidates {
		// Cap per-note tokens to prevent a single large note from starving
		// the budget. If the snippet alone exceeds the per-note limit,
		// truncate it so other results get a fair share of the budget.
		snippet := candidates[i].snippet
		if snippet != "" {
			maxSnipChars := maxPerNoteTokens * 4 // ~4 chars per token
			if len(snippet) > maxSnipChars {
				snippet = smartTruncate(snippet, maxSnipChars)
			}
		}

		var entry string
		if snippet != "" {
			entry = fmt.Sprintf("**%s** (%s, score: %.3f)\n%s\n%s",
				candidates[i].title, candidates[i].contentType, candidates[i].composite,
				candidates[i].path, snippet)
		} else {
			entry = fmt.Sprintf("**%s** (%s, score: %.3f)\n%s",
				candidates[i].title, candidates[i].contentType, candidates[i].composite,
				candidates[i].path)
		}
		entryTokens := memory.EstimateTokens(entry)

		// Find which prompt terms appear in this note's title/snippet
		candidates[i].matchTerms = findMatchingTerms(promptTerms, candidates[i].title, candidates[i].snippet)

		if totalTokens+entryTokens > maxTokenBudget {
			// Skip this note but keep scanning — smaller notes may still fit
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
