package hooks

import (
	"fmt"

	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// runStalenessCheck queries for stale notes and surfaces them.
func runStalenessCheck(db *store.DB, _ *HookInput) hookRunResult {
	stale := memory.FindStaleNotes(db, 5, true)
	if len(stale) == 0 {
		return hookEmpty("no stale notes")
	}

	contextText := memory.FormatStaleNotesContext(stale)
	if contextText == "" {
		return hookEmpty("no stale note context")
	}

	out := &HookOutput{
		SystemMessage: fmt.Sprintf(
			"\n<vault-staleness>\n%s\n</vault-staleness>\n",
			contextText,
		),
	}
	return hookInjected(out, len(stale), memory.EstimateTokens(contextText), nil, "")
}
