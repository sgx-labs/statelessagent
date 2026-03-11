package hooks

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// runStalenessCheck queries for stale notes and surfaces them,
// including both review-by staleness and source file divergence.
func runStalenessCheck(db *store.DB, _ *HookInput) hookRunResult {
	stale := memory.FindStaleNotes(db, 5, true)
	contextText := memory.FormatStaleNotesContext(stale)

	// Check source divergence
	vaultPath := config.VaultPath()
	divergenceContext := buildDivergenceContext(db, vaultPath)

	// Combine both staleness signals
	var systemMsg string
	if contextText != "" {
		systemMsg += fmt.Sprintf("\n<vault-staleness>\n%s\n</vault-staleness>\n", contextText)
	}
	if divergenceContext != "" {
		systemMsg += fmt.Sprintf("\n<vault-source-divergence>\n%s\n</vault-source-divergence>\n", divergenceContext)
	}

	if systemMsg == "" {
		return hookEmpty("no stale notes")
	}

	totalNotes := len(stale)
	if divergenceContext != "" {
		// Count the diverged notes included in context (up to 3)
		totalNotes += strings.Count(divergenceContext, "\n- ")
	}

	out := &HookOutput{
		SystemMessage: systemMsg,
	}
	return hookInjected(out, totalNotes, memory.EstimateTokens(systemMsg), nil, "")
}

// buildDivergenceContext checks for source file divergence and returns
// formatted context text, limited to 3 most relevant results.
func buildDivergenceContext(db *store.DB, vaultPath string) string {
	diverged, err := db.CheckSourceDivergence(vaultPath)
	if err != nil || len(diverged) == 0 {
		return ""
	}

	// Deduplicate by note path, keep first diverged source per note
	seen := make(map[string]bool)
	var unique []store.DivergenceResult
	for _, d := range diverged {
		if seen[d.NotePath] {
			continue
		}
		seen[d.NotePath] = true
		unique = append(unique, d)
		if len(unique) >= 3 {
			break
		}
	}

	lines := []string{"The following notes may be outdated because their source files changed:"}
	for _, d := range unique {
		sourceBase := filepath.Base(d.SourcePath)
		capturedAt := time.Unix(d.CapturedAt, 0)
		ago := time.Since(capturedAt)
		agoStr := formatDivergenceAge(ago)
		lines = append(lines, fmt.Sprintf("- %s (source: %s changed %s)", d.NotePath, sourceBase, agoStr))
	}

	return strings.Join(lines, "\n")
}

// formatDivergenceAge formats a duration as a relative time string for divergence context.
func formatDivergenceAge(d time.Duration) string {
	hours := int(d.Hours())
	if hours < 1 {
		return "just now"
	}
	if hours < 24 {
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := hours / 24
	if days == 1 {
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", days)
}
