// Package memory implements the Stateless Agent Memory Engine.
package memory

import (
	"math"
	"strings"
	"time"
)

// Recency decay rates by content type (days until half-life).
// nil means permanent — never decays.
var decayRates = map[string]*float64{
	"decision": nil,
	"hub":      nil,
	"research": ptr(90),
	"project":  ptr(90),
	"note":     ptr(60),
	"handoff":  ptr(30),
	"progress": ptr(30),
	"kaizen":   ptr(30),
}

const defaultDecayDays = 60.0

// Type baselines for confidence scoring.
var typeBaselines = map[string]float64{
	"decision": 0.9,
	"hub":      0.85,
	"research": 0.7,
	"project":  0.65,
	"handoff":  0.6,
	"kaizen":   0.5,
	"progress": 0.5,
	"note":     0.5,
}

// ComputeRecencyScore computes a 0.0-1.0 score based on content type decay rate.
// Permanent types always return 1.0. Others decay exponentially.
func ComputeRecencyScore(modifiedEpoch float64, contentType string) float64 {
	decayDaysPtr, ok := decayRates[contentType]
	if !ok {
		decayDaysPtr = ptr(defaultDecayDays)
	}
	if decayDaysPtr == nil {
		return 1.0 // permanent
	}

	ageDays := (float64(time.Now().Unix()) - modifiedEpoch) / 86400.0
	if ageDays <= 0 {
		return 1.0
	}

	// Exponential decay: score = 0.5^(age / half_life)
	return math.Pow(0.5, ageDays / *decayDaysPtr)
}

// Trust multipliers applied to confidence scores based on provenance state.
// validated/unknown = neutral (1.0), stale = 25% penalty, contradicted = 60% penalty.
var trustMultipliers = map[string]float64{
	"validated":    1.0,
	"unknown":      1.0,
	"stale":        0.75,
	"contradicted": 0.4,
}

// TrustMultiplier returns the confidence multiplier for a given trust state.
func TrustMultiplier(trustState string) float64 {
	if m, ok := trustMultipliers[trustState]; ok {
		return m
	}
	return 1.0
}

// ComputeConfidence computes a base confidence score (0.0-1.0) for a note.
// trustState adjusts the score based on provenance: stale/contradicted notes
// are penalized, validated/unknown are neutral.
func ComputeConfidence(contentType string, modifiedEpoch float64, accessCount int, hasReviewBy bool, trustState string) float64 {
	baseline, ok := typeBaselines[contentType]
	if !ok {
		baseline = 0.5
	}

	recency := ComputeRecencyScore(modifiedEpoch, contentType)

	// Access boost: log2(access_count + 1) / 10, capped at 0.15
	accessBoost := math.Min(0.15, math.Log2(float64(accessCount)+1)/10)

	// Review-by maintenance boost
	reviewBoost := 0.0
	if hasReviewBy {
		reviewBoost = 0.05
	}

	confidence := 0.5*baseline + 0.35*recency + accessBoost + reviewBoost
	confidence *= TrustMultiplier(trustState)
	return round3(math.Min(1.0, math.Max(0.0, confidence)))
}

// CompositeScore computes a composite ranking score for search results.
func CompositeScore(semanticScore, modifiedEpoch, confidence float64, contentType string,
	relevanceWeight, recencyWeight, confidenceWeight float64) float64 {

	recency := ComputeRecencyScore(modifiedEpoch, contentType)
	score := relevanceWeight*semanticScore + recencyWeight*recency + confidenceWeight*confidence
	return round3(math.Min(1.0, math.Max(0.0, score)))
}

// InferContentType infers content_type from path patterns and metadata.
func InferContentType(path string, explicitType string, tags []string) string {
	// Explicit type wins
	if explicitType != "" {
		lower := strings.ToLower(strings.TrimSpace(explicitType))
		if _, ok := decayRates[lower]; ok {
			return lower
		}
	}

	// Path-based inference
	pathLower := strings.ToLower(path)
	if strings.Contains(pathLower, "handoff") || strings.Contains(pathLower, "session") {
		return "handoff"
	}
	if strings.Contains(pathLower, "decision") {
		return "decision"
	}
	if strings.Contains(pathLower, "research") {
		return "research"
	}
	if strings.Contains(pathLower, "project") {
		return "project"
	}
	if strings.Contains(pathLower, "hub") || strings.Contains(pathLower, "moc") || strings.Contains(pathLower, "index") {
		return "hub"
	}
	if strings.HasPrefix(pathLower, "kaizen/") || strings.Contains(pathLower, "/kaizen/") {
		return "kaizen"
	}

	// Tag-based inference
	tagSet := make(map[string]bool)
	for _, t := range tags {
		tagSet[strings.ToLower(t)] = true
	}
	if tagSet["decision"] {
		return "decision"
	}
	if tagSet["research"] {
		return "research"
	}
	if tagSet["handoff"] {
		return "handoff"
	}
	if tagSet["kaizen"] {
		return "kaizen"
	}

	return "note"
}

// queryTypeBoostKeywords maps query keywords to content_type boost multipliers.
// When a search query contains one of these keywords, results of the matching
// content_type receive a subtle score multiplier (1.2-1.3x) — enough to break
// ties, not enough to override strong semantic matches.
var queryTypeBoostKeywords = map[string]map[string]float64{
	// Handoff-related queries
	"session":      {"handoff": 1.3},
	"handoff":      {"handoff": 1.3},
	"hand-off":     {"handoff": 1.3},
	"last session": {"handoff": 1.3},
	"working on":   {"handoff": 1.3},
	"left off":     {"handoff": 1.3},
	"leave off":    {"handoff": 1.3},
	"pick up":      {"handoff": 1.3},
	// Decision-related queries
	"decided":    {"decision": 1.3},
	"decide":     {"decision": 1.3},
	"decision":   {"decision": 1.3},
	"chose":      {"decision": 1.3},
	"choose":     {"decision": 1.3},
	"why did we": {"decision": 1.3},
	"choice":     {"decision": 1.3},
	// Meeting-related queries
	"meeting":   {"meeting": 1.2},
	"discussed": {"meeting": 1.2},
	"sprint":    {"meeting": 1.2},
}

// staleQueryKeywords are keywords that signal the user is asking about stale
// content. When matched, the stale trust penalty is suppressed (multiplier of
// 1.0 effectively neutralizes TrustMultiplier for stale results).
var staleQueryKeywords = []string{
	"stale", "outdated", "old", "deprecated",
}

// InferQueryTypeBoost analyzes a search query and returns content_type score
// multipliers. The returned map contains content_type -> multiplier pairs.
// A special key "_suppress_stale_penalty" with value 1.0 signals that stale
// results should not be penalized for this query.
func InferQueryTypeBoost(query string) map[string]float64 {
	lower := strings.ToLower(query)
	boosts := make(map[string]float64)

	// Check type-boost keywords (longest match first via iteration order is fine;
	// overlapping boosts are max'd)
	for keyword, typeBoosts := range queryTypeBoostKeywords {
		if strings.Contains(lower, keyword) {
			for ct, mult := range typeBoosts {
				if existing, ok := boosts[ct]; !ok || mult > existing {
					boosts[ct] = mult
				}
			}
		}
	}

	// Check stale-intent keywords
	for _, kw := range staleQueryKeywords {
		if strings.Contains(lower, kw) {
			boosts["_suppress_stale_penalty"] = 1.0
			break
		}
	}

	return boosts
}

// recencyKeywords signal the user wants time-based results.
var recencyKeywords = []string{
	"recent", "recently", "lately", "today", "yesterday",
	"this week", "last week", "this month", "last month",
	"last session", "previous session", "earlier today",
	"worked on", "changed", "modified",
	"updated", "latest", "newest", "last time",
	"last night", "left off", "up to speed", "catch me up",
	"where were we", "bring me up", "what happened",
	"handoff", "hand off", "hand-off",
}

// HasRecencyIntent returns true if the query contains time-related keywords.
func HasRecencyIntent(query string) bool {
	lower := strings.ToLower(query)
	for _, kw := range recencyKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func ptr(f float64) *float64 {
	return &f
}

func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}
