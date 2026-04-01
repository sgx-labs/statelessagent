package memory

import (
	"math"
	"strings"
)

// ContradictionType classifies why a note was flagged as contradicted.
type ContradictionType string

const (
	// ContradictionFactual means a newer fact supersedes an older one
	// (e.g., deadline changed from March to April).
	ContradictionFactual ContradictionType = "factual"

	// ContradictionPreference means a user preference evolved
	// (e.g., switched from React to Svelte).
	ContradictionPreference ContradictionType = "preference"

	// ContradictionContext means both statements may be true in
	// different contexts (e.g., different projects use different DBs).
	ContradictionContext ContradictionType = "context"
)

// ContradictionResult describes a detected contradiction between two notes.
type ContradictionResult struct {
	OldNotePath string            `json:"old_note_path"`
	NewNotePath string            `json:"new_note_path"`
	Type        ContradictionType `json:"type"`
	Confidence  float64           `json:"confidence"`
	Reason      string            `json:"reason"`
}

// ContradictionCandidate is a similar note that might be contradicted by new content.
// This is a simplified version of SearchResult for use in contradiction detection,
// so we don't couple to the store package.
type ContradictionCandidate struct {
	Path   string
	Text   string
	Score  float64 // similarity score (0-1, higher = more similar)
	Title  string
	Tags   string
	Domain string
}

// --- Negation / temporal / preference signal patterns ---
//
// These keyword lists are ordered longest-first for documentation clarity,
// but matching uses simple substring containment, not longest-match-first.

// negationSignals indicate the new content explicitly negates something old.
var negationSignals = []string{
	"no longer using",
	"no longer use",
	"no longer",
	"not anymore",
	"instead of",
	"replaced by",
	"replaced with",
	"deprecated",
	"removed",
	"dropped",
	"abandoned",
	"stopped using",
	"don't use",
	"do not use",
	"won't use",
	"will not use",
	"not using",
}

// temporalSignals indicate the new content describes a time-based update.
var temporalSignals = []string{
	"as of today",
	"as of now",
	"as of",
	"changed to",
	"changed from",
	"updated to",
	"moved to",
	"switched to",
	"switched from",
	"migrated to",
	"migrated from",
	"now using",
	"now uses",
	"now use",
	"going forward",
	"from now on",
	"starting today",
	"effective immediately",
}

// preferenceSignals indicate a preference or choice evolution.
var preferenceSignals = []string{
	"prefer to use",
	"decided to use",
	"decided on",
	"chosen over",
	"better than",
	"prefer",
	"chose",
	"picked",
	"selected",
	"went with",
	"go with",
	"opted for",
	"moved away from",
	"moving away from",
}

// contextSignals indicate context-dependent truth (both may be correct).
var contextSignals = []string{
	"in this project",
	"for this repo",
	"for this project",
	"in this case",
	"in this context",
	"specifically for",
	"only for",
	"when using",
	"depending on",
	"except when",
	"for production",
	"for development",
	"for testing",
	"on linux",
	"on mac",
	"on windows",
}

// MinSimilarityThreshold is the minimum similarity score for a note to be
// considered as a contradiction candidate. Cosine similarity below this
// means the notes are too different to be contradictory.
const MinSimilarityThreshold = 0.7

// MinContradictionConfidence is the minimum confidence to report a contradiction.
const MinContradictionConfidence = 0.3

// DetectContradictions checks new content against a set of similar existing notes
// and returns any detected contradictions. Each result identifies the old note
// that is contradicted, the type of contradiction, and a confidence score.
//
// This is designed to work WITHOUT an LLM — it uses keyword patterns and
// similarity scores for detection. The newNotePath is the path of the note
// being saved (used in results but not for detection logic).
func DetectContradictions(newContent string, newNotePath string, candidates []ContradictionCandidate) []ContradictionResult {
	if len(candidates) == 0 || strings.TrimSpace(newContent) == "" {
		return nil
	}

	newLower := strings.ToLower(newContent)
	var results []ContradictionResult

	for _, cand := range candidates {
		// Skip candidates below similarity threshold
		if cand.Score < MinSimilarityThreshold {
			continue
		}

		// Skip self-references
		if cand.Path == newNotePath {
			continue
		}

		oldLower := strings.ToLower(cand.Text)

		// Check for contradiction signals in the NEW content
		cType, confidence, reason := classifyContradictionSignals(newLower, oldLower, cand.Score)
		if confidence < MinContradictionConfidence {
			continue
		}

		results = append(results, ContradictionResult{
			OldNotePath: cand.Path,
			NewNotePath: newNotePath,
			Type:        cType,
			Confidence:  round3f(confidence),
			Reason:      reason,
		})
	}

	return results
}

// ClassifyContradiction determines the contradiction type between old and new
// content. Returns the type and a confidence score. This is the public API
// for when you already know there IS a contradiction and want to classify it.
func ClassifyContradiction(oldContent, newContent string) (ContradictionType, float64) {
	newLower := strings.ToLower(newContent)
	oldLower := strings.ToLower(oldContent)
	cType, confidence, _ := classifyContradictionSignals(newLower, oldLower, 0.8)
	return cType, confidence
}

// classifyContradictionSignals performs the actual signal-based classification.
// It returns the type, confidence, and a human-readable reason.
func classifyContradictionSignals(newLower, oldLower string, similarity float64) (ContradictionType, float64, string) {
	// Count signals of each type in the new content
	negationScore, negationReason := countSignals(newLower, negationSignals)
	temporalScore, temporalReason := countSignals(newLower, temporalSignals)
	preferenceScore, preferenceReason := countSignals(newLower, preferenceSignals)
	contextScore, contextReason := countSignals(newLower, contextSignals)

	totalScore := negationScore + temporalScore + preferenceScore + contextScore
	if totalScore == 0 {
		return ContradictionFactual, 0, ""
	}

	// Check for context signals first — if both might be true in different
	// contexts, flag as context rather than factual/preference.
	if contextScore > 0 && contextScore >= negationScore && contextScore >= preferenceScore {
		confidence := computeContradictionConfidence(contextScore, similarity, totalScore)
		return ContradictionContext, confidence, "context-dependent: " + contextReason
	}

	// Preference signals: user choice evolution
	if preferenceScore > 0 && preferenceScore >= negationScore && preferenceScore >= temporalScore {
		confidence := computeContradictionConfidence(preferenceScore+temporalScore, similarity, totalScore)
		reason := "preference changed: " + preferenceReason
		if temporalReason != "" {
			reason += "; " + temporalReason
		}
		return ContradictionPreference, confidence, reason
	}

	// Factual: negation and/or temporal update signals dominate
	factualScore := negationScore + temporalScore
	confidence := computeContradictionConfidence(factualScore, similarity, totalScore)
	reason := ""
	if negationReason != "" {
		reason = "superseded: " + negationReason
	}
	if temporalReason != "" {
		if reason != "" {
			reason += "; "
		}
		reason += "updated: " + temporalReason
	}
	if reason == "" {
		reason = "factual update detected"
	}
	return ContradictionFactual, confidence, reason
}

// computeContradictionConfidence combines signal strength with similarity score
// to produce a 0-1 confidence value.
func computeContradictionConfidence(signalScore int, similarity float64, totalSignals int) float64 {
	// Base confidence from signal strength (diminishing returns)
	signalConfidence := 1.0 - math.Pow(0.5, float64(signalScore))

	// Similarity boost: higher similarity = more likely a real contradiction
	// (not just two notes that happen to use similar words)
	similarityBoost := (similarity - MinSimilarityThreshold) / (1.0 - MinSimilarityThreshold)
	if similarityBoost < 0 {
		similarityBoost = 0
	}

	// Combine: 60% signal strength, 40% similarity
	confidence := 0.6*signalConfidence + 0.4*similarityBoost

	return math.Min(1.0, math.Max(0.0, confidence))
}

// countSignals counts how many signal patterns appear in the text.
// Returns the count and the first matching pattern as the reason.
func countSignals(text string, signals []string) (int, string) {
	count := 0
	firstMatch := ""
	for _, sig := range signals {
		if strings.Contains(text, sig) {
			count++
			if firstMatch == "" {
				firstMatch = sig
			}
		}
	}
	return count, firstMatch
}

// round3f rounds a float64 to 3 decimal places.
func round3f(f float64) float64 {
	return math.Round(f*1000) / 1000
}
