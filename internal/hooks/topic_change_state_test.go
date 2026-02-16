package hooks

import (
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestTopicChangeLifecycle(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	origPrompt := keyTermsPrompt
	t.Cleanup(func() { keyTermsPrompt = origPrompt })

	sessionID := "s-123"

	keyTermsPrompt = "vector search ranking strategy"
	if !isTopicChange(db, sessionID) {
		t.Fatal("expected first prompt in session to be treated as topic change")
	}
	if got := topicChangeScore(db, sessionID); got != -1 {
		t.Fatalf("expected no score before topic state exists, got %f", got)
	}

	storeTopicTerms(db, sessionID)

	keyTermsPrompt = "vector ranking strategy improvements"
	if isTopicChange(db, sessionID) {
		t.Fatal("expected similar follow-up prompt to be treated as same topic")
	}
	score := topicChangeScore(db, sessionID)
	if score <= topicSimilarityThreshold {
		t.Fatalf("expected score above threshold for same-topic follow-up, got %f", score)
	}

	keyTermsPrompt = "oauth callback redirect bug"
	if !isTopicChange(db, sessionID) {
		t.Fatal("expected unrelated prompt to be treated as topic change")
	}
	score = topicChangeScore(db, sessionID)
	if score >= topicSimilarityThreshold {
		t.Fatalf("expected score below threshold for new topic, got %f", score)
	}
}

func TestTopicChange_CorruptSessionStateTreatsAsNewTopic(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	origPrompt := keyTermsPrompt
	t.Cleanup(func() { keyTermsPrompt = origPrompt })

	sessionID := "s-corrupt"
	_ = db.SessionStateSet(sessionID, sessionStateKeyTopicTerms, "{bad-json")

	keyTermsPrompt = "database migration error"
	if !isTopicChange(db, sessionID) {
		t.Fatal("expected corrupt stored terms to force topic refresh")
	}
	if got := topicChangeScore(db, sessionID); got != -1 {
		t.Fatalf("expected -1 score with corrupt stored terms, got %f", got)
	}
}
