package hooks

import (
	"fmt"
	"os"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// runFeedbackLoop reads the session transcript, checks which surfaced notes
// were actually referenced by the agent, and updates access counts accordingly.
// This closes the learning loop: notes that get used rise in confidence,
// notes that are surfaced but ignored gradually decay.
func runFeedbackLoop(db *store.DB, input *HookInput) *HookOutput {
	if input.TranscriptPath == "" || input.SessionID == "" {
		return nil
	}
	if _, err := os.Stat(input.TranscriptPath); err != nil {
		return nil
	}

	// Get assistant messages from this session
	messages := memory.GetLastNMessages(input.TranscriptPath, 200, "assistant")
	if len(messages) == 0 {
		return nil
	}

	// Concatenate assistant output for reference detection
	var assistantText strings.Builder
	for _, m := range messages {
		assistantText.WriteString(m.Content)
		assistantText.WriteString("\n")
	}

	// Check which surfaced notes were actually referenced
	referencedCount := memory.DetectReferences(db, input.SessionID, assistantText.String())
	if referencedCount == 0 {
		return nil
	}

	// Boost access counts for referenced notes
	records, err := db.GetUsageBySession(input.SessionID)
	if err != nil {
		return nil
	}

	var referencedPaths []string
	for _, rec := range records {
		if rec.WasReferenced {
			referencedPaths = append(referencedPaths, rec.InjectedPaths...)
		}
	}

	if len(referencedPaths) > 0 {
		db.IncrementAccessCount(referencedPaths)
	}

	if !isQuietMode() {
		fmt.Fprintf(os.Stderr, "same: ✓ %d surfaced note(s) were referenced by the agent\n", referencedCount)
	}

	return nil // feedback is silent — no context injection back to the agent
}
