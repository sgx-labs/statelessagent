package hooks

import (
	"encoding/json"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/store"
)

const (
	// topicSimilarityThreshold: above this Jaccard score, prompts are
	// considered same-topic. Tuned conservatively — better to occasionally
	// re-inject than to miss a real topic change.
	topicSimilarityThreshold = 0.35

	// sessionStateKeyTopicTerms is the key for storing the last
	// injected topic's extracted terms in session_state.
	sessionStateKeyTopicTerms = "last_topic_terms"
)

// isTopicChange compares the current prompt's terms against the last
// injected topic's terms. Returns true if the topic has changed enough
// to warrant new context injection.
//
// On first prompt of a session (no stored terms), always returns true.
// Requires keyTermsPrompt to be set before calling.
func isTopicChange(db *store.DB, sessionID string) bool {
	if sessionID == "" {
		return true // No session tracking, always inject
	}

	currentTerms := collectTopicTerms()
	if len(currentTerms) == 0 {
		return false // No terms = low signal, already handled by hasLowSignal
	}

	// Read last topic terms from session state
	stored, ok := db.SessionStateGet(sessionID, sessionStateKeyTopicTerms)
	if !ok || stored == "" {
		return true // First prompt with terms in this session
	}

	var lastTerms []string
	if err := json.Unmarshal([]byte(stored), &lastTerms); err != nil {
		return true // Corrupt data, treat as new topic
	}

	if len(lastTerms) == 0 {
		return true
	}

	similarity := jaccardSimilarity(currentTerms, lastTerms)
	return similarity <= topicSimilarityThreshold
}

// topicChangeScore returns the Jaccard similarity score for logging.
// Returns -1 if no comparison was possible (first prompt, no stored terms).
func topicChangeScore(db *store.DB, sessionID string) float64 {
	if sessionID == "" {
		return -1
	}
	currentTerms := collectTopicTerms()
	if len(currentTerms) == 0 {
		return -1
	}
	stored, ok := db.SessionStateGet(sessionID, sessionStateKeyTopicTerms)
	if !ok || stored == "" {
		return -1
	}
	var lastTerms []string
	if err := json.Unmarshal([]byte(stored), &lastTerms); err != nil {
		return -1
	}
	if len(lastTerms) == 0 {
		return -1
	}
	return jaccardSimilarity(currentTerms, lastTerms)
}

// storeTopicTerms saves the current prompt's terms as the session's
// active topic. Called after a successful context injection.
func storeTopicTerms(db *store.DB, sessionID string) {
	if sessionID == "" {
		return
	}

	terms := collectTopicTerms()
	data, err := json.Marshal(terms)
	if err != nil {
		return
	}
	_ = db.SessionStateSet(sessionID, sessionStateKeyTopicTerms, string(data))
}

// collectTopicTerms gathers all meaningful terms from the current prompt.
// Combines specific + broad from extractKeyTerms for maximum signal.
func collectTopicTerms() []string {
	specific, broad := extractKeyTerms()
	seen := make(map[string]bool)
	var terms []string

	for _, t := range specific {
		lower := strings.ToLower(t)
		if !seen[lower] {
			terms = append(terms, lower)
			seen[lower] = true
		}
	}
	for _, t := range broad {
		lower := strings.ToLower(t)
		if !seen[lower] {
			terms = append(terms, lower)
			seen[lower] = true
		}
	}
	return terms
}

// jaccardSimilarity computes |A ∩ B| / |A ∪ B| for two string slices.
func jaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0 // Both empty = identical
	}

	setA := make(map[string]bool, len(a))
	for _, s := range a {
		setA[s] = true
	}

	setB := make(map[string]bool, len(b))
	for _, s := range b {
		setB[s] = true
	}

	intersection := 0
	for s := range setA {
		if setB[s] {
			intersection++
		}
	}

	union := len(setA)
	for s := range setB {
		if !setA[s] {
			union++
		}
	}

	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}
