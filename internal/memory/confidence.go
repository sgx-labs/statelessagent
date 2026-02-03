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
}

const defaultDecayDays = 60.0

// Type baselines for confidence scoring.
var typeBaselines = map[string]float64{
	"decision": 0.9,
	"hub":      0.85,
	"research": 0.7,
	"project":  0.65,
	"handoff":  0.6,
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

// ComputeConfidence computes a base confidence score (0.0-1.0) for a note.
func ComputeConfidence(contentType string, modifiedEpoch float64, accessCount int, hasReviewBy bool) float64 {
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

	return "note"
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
