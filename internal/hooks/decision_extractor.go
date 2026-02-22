package hooks

import (
	"fmt"
	"os"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// runDecisionExtractor reads the transcript, extracts decisions, and appends to the log.
func runDecisionExtractor(db *store.DB, input *HookInput) hookRunResult {
	if stopHookDebounce(db, input.SessionID, "decision-extractor") {
		return hookSkipped("cooldown active")
	}

	transcriptPath := input.TranscriptPath
	if transcriptPath == "" {
		writeVerboseLog("decision-extractor: no transcript path provided\n")
		return hookSkipped("no transcript path")
	}
	if _, err := os.Stat(transcriptPath); err != nil {
		writeVerboseLog(fmt.Sprintf("decision-extractor: transcript not found: %s\n", transcriptPath))
		return hookSkipped("transcript missing")
	}

	// Get last 200 messages (long sessions can easily exceed 50)
	messages := memory.GetLastNMessages(transcriptPath, 200, "")
	if len(messages) == 0 {
		return hookEmpty("no transcript messages")
	}

	// Extract decisions
	decisions := memory.ExtractDecisionsFromMessages(messages)
	if len(decisions) == 0 {
		return hookSkipped("no new decisions")
	}

	// Append to decision log (validate path stays in vault)
	logPath, ok := config.SafeVaultSubpath(config.DecisionLogPath())
	if !ok {
		fmt.Fprintf(os.Stderr, "same: decision log path is outside your vault — check SAME_DECISION_LOG setting\n")
		return hookError("invalid decision log path")
	}
	count := memory.AppendToDecisionLog(decisions, logPath, "")

	if count > 0 {
		if !isQuietMode() {
			fmt.Fprintf(os.Stderr, "same: ✓ extracted %d decision(s) → %s\n", count, config.DecisionLogPath())
		}
		out := &HookOutput{
			SystemMessage: fmt.Sprintf(
				"\n<vault-decisions>\nExtracted %d decision(s) from this session.\nAppended to: %s\nTagged as auto-extracted for human review.\n</vault-decisions>\n",
				count, config.DecisionLogPath(),
			),
		}
		return hookInjected(out, 0, 0, nil, fmt.Sprintf("%d decision(s) extracted", count))
	}

	return hookEmpty("no decisions appended")
}
