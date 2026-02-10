package hooks

import (
	"fmt"
	"os"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// runDecisionExtractor reads the transcript, extracts decisions, and appends to the log.
func runDecisionExtractor(_ *store.DB, input *HookInput) *HookOutput {
	transcriptPath := input.TranscriptPath
	if transcriptPath == "" {
		return nil
	}
	if _, err := os.Stat(transcriptPath); err != nil {
		return nil
	}

	// Get last 200 messages (long sessions can easily exceed 50)
	messages := memory.GetLastNMessages(transcriptPath, 200, "")
	if len(messages) == 0 {
		return nil
	}

	// Extract decisions
	decisions := memory.ExtractDecisionsFromMessages(messages)
	if len(decisions) == 0 {
		return nil
	}

	// Append to decision log (validate path stays in vault)
	logPath, ok := config.SafeVaultSubpath(config.DecisionLogPath())
	if !ok {
		fmt.Fprintf(os.Stderr, "same: decision log path is outside your notes folder — skipping\n")
		return nil
	}
	count := memory.AppendToDecisionLog(decisions, logPath, "")

	if count > 0 {
		return &HookOutput{
			HookSpecificOutput: &HookSpecific{
				HookEventName: "Stop",
				AdditionalContext: fmt.Sprintf(
					"\n<vault-decisions>\nExtracted %d decision(s) from this session.\nAppended to: %s\nTagged as auto-extracted for human review.\n</vault-decisions>\n",
					count, config.DecisionLogPath(),
				),
			},
		}
	}

	return nil
}
