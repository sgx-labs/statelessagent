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

// preCompactCooldown is a shorter cooldown for PreCompact checkpoint handoffs.
// PreCompact fires less frequently than Stop (only before context compaction),
// so a shorter cooldown lets it capture checkpoints without excessive runs.
const preCompactCooldown = 120 // 2 minutes

// stopHookDebounce checks whether a Stop hook ran recently for this session.
// Returns true if the hook should be skipped (still within cooldown).
// On first run or after cooldown expires, returns false and records the timestamp.
func stopHookDebounce(db *store.DB, sessionID, hookName string) bool {
	return hookDebounce(db, sessionID, hookName, "stop_cooldown_", int64(stopHookCooldown))
}

// preCompactDebounce checks whether a PreCompact hook ran recently for this session.
// Uses a shorter cooldown than Stop since PreCompact fires less frequently.
func preCompactDebounce(db *store.DB, sessionID, hookName string) bool {
	return hookDebounce(db, sessionID, hookName, "precompact_cooldown_", int64(preCompactCooldown))
}

// hookDebounce is the shared debounce logic for Stop and PreCompact hooks.
func hookDebounce(db *store.DB, sessionID, hookName, keyPrefix string, cooldownSec int64) bool {
	if sessionID == "" {
		return false // no session tracking possible, let it run
	}
	key := keyPrefix + hookName
	if last, ok := db.SessionStateGet(sessionID, key); ok {
		if ts, err := strconv.ParseInt(last, 10, 64); err == nil {
			if time.Now().Unix()-ts < cooldownSec {
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

// isCheckpointMode returns true if the hook was triggered by PreCompact
// (a checkpoint before context compaction) rather than Stop (session end).
func isCheckpointMode(input *HookInput) bool {
	return input.HookEventName == "PreCompact"
}

// runHandoffGenerator generates a handoff note from the transcript.
func runHandoffGenerator(db *store.DB, input *HookInput) hookRunResult {
	checkpoint := isCheckpointMode(input)
	if checkpoint {
		if preCompactDebounce(db, input.SessionID, "handoff-generator") {
			return hookSkipped("cooldown active")
		}
	} else {
		if stopHookDebounce(db, input.SessionID, "handoff-generator") {
			return hookSkipped("cooldown active")
		}
	}

	transcriptPath := input.TranscriptPath
	if transcriptPath == "" {
		writeVerboseLog("handoff-generator: no transcript path provided\n")
		return hookSkipped("no transcript path")
	}
	if _, err := os.Stat(transcriptPath); err != nil {
		writeVerboseLog(fmt.Sprintf("handoff-generator: transcript not found: %s\n", transcriptPath))
		return hookSkipped("transcript missing")
	}

	result := memory.AutoHandoffFromTranscript(transcriptPath, input.SessionID)
	if result == nil {
		return hookEmpty("insufficient transcript data")
	}

	// Only show the message on first handoff creation for this session.
	// Subsequent overwrites update the file silently.
	key := "handoff_created"
	if _, alreadyCreated := db.SessionStateGet(input.SessionID, key); alreadyCreated {
		writeVerboseLog(fmt.Sprintf("handoff-generator: updated %s (silent)\n", result.Path))
		return hookEmpty("handoff updated")
	}
	_ = db.SessionStateSet(input.SessionID, key, result.Path)

	handoffKind := "handoff"
	if checkpoint {
		handoffKind = "checkpoint handoff"
	}

	if !isQuietMode() {
		fmt.Fprintf(os.Stderr, "same: ✓ %s saved → %s\n", handoffKind, result.Path)
	}

	out := &HookOutput{
		SystemMessage: fmt.Sprintf(
			"\n<vault-handoff>\nSession %s written to: %s\nSession ID: %s\n</vault-handoff>\n",
			handoffKind, result.Path, result.SessionID,
		),
	}
	return hookInjected(out, 1, 0, []string{result.Path}, handoffKind+" saved")
}
