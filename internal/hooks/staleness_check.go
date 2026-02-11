package hooks

import (
	"fmt"

	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// runStalenessCheck queries for stale notes and surfaces them.
func runStalenessCheck(db *store.DB, _ *HookInput) *HookOutput {
	stale := memory.FindStaleNotes(db, 5, true)
	if len(stale) == 0 {
		return nil
	}

	contextText := memory.FormatStaleNotesContext(stale)
	if contextText == "" {
		return nil
	}

	return &HookOutput{
		SystemMessage: fmt.Sprintf(
			"\n<vault-staleness>\n%s\n</vault-staleness>\n",
			contextText,
		),
	}
}
