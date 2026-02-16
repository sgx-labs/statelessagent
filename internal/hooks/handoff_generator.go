package hooks

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// stopHookCooldown is the minimum seconds between successive runs of each
// Stop hook within the same session. Claude Code fires the Stop event on
// every assistant turn, not just session end — without debouncing, hooks
// would re-parse the transcript and create duplicate artifacts every turn.
const stopHookCooldown = 300 // 5 minutes

// stopHookDebounce checks whether a Stop hook ran recently for this session.
// Returns true if the hook should be skipped (still within cooldown).
// On first run or after cooldown expires, returns false and records the timestamp.
func stopHookDebounce(db *store.DB, sessionID, hookName string) bool {
	if sessionID == "" {
		return false // no session tracking possible, let it run
	}
	key := "stop_cooldown_" + hookName
	if last, ok := db.SessionStateGet(sessionID, key); ok {
		if ts, err := strconv.ParseInt(last, 10, 64); err == nil {
			if time.Now().Unix()-ts < stopHookCooldown {
				writeVerboseLog(fmt.Sprintf("%s: skipped (cooldown, last ran %ds ago)\n",
					hookName, time.Now().Unix()-ts))
				return true
			}
		}
	}
	// Record this run
	_ = db.SessionStateSet(sessionID, key, strconv.FormatInt(time.Now().Unix(), 10))
	return false
}

// runHandoffGenerator generates a handoff note from the transcript.
func runHandoffGenerator(db *store.DB, input *HookInput) *HookOutput {
	if stopHookDebounce(db, input.SessionID, "handoff-generator") {
		return nil
	}

	transcriptPath := input.TranscriptPath
	if transcriptPath == "" {
		writeVerboseLog("handoff-generator: no transcript path provided\n")
		return nil
	}
	if _, err := os.Stat(transcriptPath); err != nil {
		writeVerboseLog(fmt.Sprintf("handoff-generator: transcript not found: %s\n", transcriptPath))
		return nil
	}

	result := memory.AutoHandoffFromTranscript(transcriptPath, input.SessionID)
	if result == nil {
		return nil
	}

	// Only show the message on first handoff creation for this session.
	// Subsequent overwrites update the file silently.
	key := "handoff_created"
	if _, alreadyCreated := db.SessionStateGet(input.SessionID, key); alreadyCreated {
		writeVerboseLog(fmt.Sprintf("handoff-generator: updated %s (silent)\n", result.Path))
		return nil
	}
	_ = db.SessionStateSet(input.SessionID, key, result.Path)

	if !isQuietMode() {
		fmt.Fprintf(os.Stderr, "same: ✓ handoff saved → %s\n", result.Path)
	}

	return &HookOutput{
		SystemMessage: fmt.Sprintf(
			"\n<vault-handoff>\nSession handoff written to: %s\nSession ID: %s\n</vault-handoff>\n",
			result.Path, result.SessionID,
		),
	}
}
