package hooks

import (
	"fmt"
	"os"

	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// runHandoffGenerator generates a handoff note from the transcript.
func runHandoffGenerator(_ *store.DB, input *HookInput) *HookOutput {
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
