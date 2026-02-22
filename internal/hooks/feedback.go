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
func runFeedbackLoop(db *store.DB, input *HookInput) hookRunResult {
	if stopHookDebounce(db, input.SessionID, "feedback-loop") {
		return hookSkipped("cooldown active")
	}

	if input.TranscriptPath == "" || input.SessionID == "" {
		writeVerboseLog("feedback-loop: no transcript path or session ID provided\n")
		return hookSkipped("missing transcript path or session ID")
	}
	if _, err := os.Stat(input.TranscriptPath); err != nil {
		writeVerboseLog(fmt.Sprintf("feedback-loop: transcript not found: %s\n", input.TranscriptPath))
		return hookSkipped("transcript missing")
	}

	// Get assistant messages from this session
	messages := memory.GetLastNMessages(input.TranscriptPath, 200, "assistant")
	if len(messages) == 0 {
		return hookEmpty("no assistant messages")
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
		return hookEmpty("no referenced notes")
	}

	// Boost access counts for referenced notes
	records, err := db.GetUsageBySession(input.SessionID)
	if err != nil {
		return hookError("usage lookup failed")
	}

	var referencedPaths []string
	for _, rec := range records {
		if rec.WasReferenced {
			referencedPaths = append(referencedPaths, rec.InjectedPaths...)
		}
	}

	if len(referencedPaths) > 0 {
		_ = db.IncrementAccessCount(referencedPaths) // best-effort relevance signal
	}

	if !isQuietMode() {
		fmt.Fprintf(os.Stderr, "same: ✓ %d surfaced note(s) were referenced by the agent\n", referencedCount)
	}

	// Feedback is intentionally silent — no context injection back to the agent.
	return hookEmpty(fmt.Sprintf("%d surfaced note(s) referenced", referencedCount))
}
