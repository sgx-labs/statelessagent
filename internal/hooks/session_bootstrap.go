package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// Token budget caps (approximate: 1 token ≈ 4 chars).
const (
	bootstrapMaxChars    = 8000 // ~2000 tokens total
	handoffMaxChars      = 4000 // ~1000 tokens — highest priority
	decisionsMaxChars    = 3000 // ~750 tokens
	staleNotesMaxChars   = 1000 // ~250 tokens
	decisionLookbackDays = 7
)

// runSessionBootstrap surfaces handoff, decisions, and stale notes at session start.
func runSessionBootstrap(db *store.DB, input *HookInput) *HookOutput {
	sessionID := ""
	if input != nil {
		sessionID = input.SessionID
	}
	cleanStaleInstances(sessionID)
	registerInstance(sessionID, "")

	// Check for graduation tips (shown to stderr)
	if tip := CheckGraduation(db); tip != "" {
		fmt.Fprint(os.Stderr, tip)
	}

	var sections []string

	// Priority 0: Unified recovery (replaces separate session index + handoff lookup)
	// Uses a priority cascade: handoff → instance → session index
	recovered := RecoverPreviousSession(db, sessionID)
	if recovered != nil {
		if ctx := FormatRecoveryContext(recovered); ctx != "" {
			sections = append(sections, ctx)
		}
	}

	// Priority 0b: Active instances (other Claude Code sessions)
	if instances := findActiveInstances(sessionID); instances != "" {
		sections = append(sections, instances)
	}

	// Priority 2: Active decisions (last 7 days)
	if decisions := findActiveDecisions(); decisions != "" {
		sections = append(sections, decisions)
	}

	// Priority 3: Stale notes (reuse existing logic)
	if stale := findStaleNotesSection(db); stale != "" {
		sections = append(sections, stale)
	}

	if len(sections) == 0 {
		return nil
	}

	context := strings.Join(sections, "\n\n")

	// Enforce total budget
	if len(context) > bootstrapMaxChars {
		context = context[:bootstrapMaxChars]
	}

	return &HookOutput{
		HookSpecificOutput: &HookSpecific{
			HookEventName: "SessionStart",
			AdditionalContext: fmt.Sprintf(
				"\n<session-bootstrap>\n%s\n</session-bootstrap>\n",
				context,
			),
		},
	}
}

// findLatestHandoff walks the handoff directory and returns the most recent
// handoff note's key sections (Next Session, Current State, etc.).
func findLatestHandoff() string {
	// SECURITY: Validate handoff directory stays inside vault boundary.
	handoffDir, ok := config.SafeVaultSubpath(config.HandoffDirectory())
	if !ok {
		return ""
	}
	entries, err := os.ReadDir(handoffDir)
	if err != nil || len(entries) == 0 {
		return ""
	}

	// Collect markdown files only
	var mdFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			mdFiles = append(mdFiles, e)
		}
	}
	if len(mdFiles) == 0 {
		return ""
	}

	// Sort by filename descending (filenames are date-prefixed)
	sort.Slice(mdFiles, func(i, j int) bool {
		return mdFiles[i].Name() > mdFiles[j].Name()
	})

	// Read the most recent file
	latestPath := filepath.Join(handoffDir, mdFiles[0].Name())
	data, err := os.ReadFile(latestPath)
	if err != nil {
		return ""
	}

	content := string(data)
	if len(content) > handoffMaxChars*2 {
		content = content[:handoffMaxChars*2]
	}

	// Extract key sections: Next Session, Current State, Files Changed,
	// Accomplishments, Decisions Made, What Was Done
	extracted := extractHandoffSections(content)
	if extracted == "" {
		// Fallback: use the raw content (truncated)
		extracted = content
	}

	if len(extracted) > handoffMaxChars {
		extracted = extracted[:handoffMaxChars]
	}

	return "## Last Session\n" + extracted
}

// extractHandoffSections pulls the most useful sections from a handoff note.
func extractHandoffSections(content string) string {
	// Strip YAML frontmatter
	if strings.HasPrefix(content, "---") {
		if idx := strings.Index(content[3:], "---"); idx >= 0 {
			content = strings.TrimSpace(content[idx+6:])
		}
	}

	// Define sections in priority order. Use longest-match-first groups
	// to avoid "Next Session" also matching when "Next Session Should" exists.
	prioritySections := []string{
		"Next Session Should",
		"Next Session",
		"Current State",
		"What Was Done",
		"Accomplishments",
		"Decisions Made",
		"Files Changed",
	}

	var parts []string
	matched := make(map[int]bool) // track byte offsets already extracted

	for _, section := range prioritySections {
		idx, text := extractSectionWithOffset(content, section)
		if text == "" || matched[idx] {
			continue
		}
		matched[idx] = true
		parts = append(parts, "### "+section+"\n"+text)
	}

	return strings.Join(parts, "\n\n")
}

// extractSectionWithOffset extracts the content under a ## heading and returns
// its byte offset in the source content (used for deduplication).
func extractSectionWithOffset(content, heading string) (int, string) {
	// Look for ## heading (case-insensitive)
	lower := strings.ToLower(content)
	target := strings.ToLower("## " + heading)

	idx := strings.Index(lower, target)
	if idx < 0 {
		return -1, ""
	}

	// Skip the heading line itself
	start := idx + len("## "+heading)
	rest := content[start:]

	// Find the next ## heading
	nextHeading := strings.Index(rest, "\n## ")
	if nextHeading >= 0 {
		rest = rest[:nextHeading]
	}

	// Also stop at --- (horizontal rule / frontmatter end)
	if hrIdx := strings.Index(rest, "\n---"); hrIdx >= 0 {
		rest = rest[:hrIdx]
	}

	return idx, strings.TrimSpace(rest)
}

// findActiveDecisions reads decision log files and extracts entries from
// the last 7 days. Checks both the root DecisionLog and project directories.
func findActiveDecisions() string {
	vaultPath := config.VaultPath()
	var candidates []string

	// Check root decision log (validate path stays in vault)
	rootLog, ok := config.SafeVaultSubpath(config.DecisionLogPath())
	if ok {
		if _, err := os.Stat(rootLog); err == nil {
			candidates = append(candidates, rootLog)
		}
	}

	// Walk entire vault for decision files, respecting SkipDirs
	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if config.SkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(info.Name())
		if strings.Contains(name, "decision") && strings.HasSuffix(name, ".md") {
			// Skip the root decision log (already added above)
			if path != rootLog {
				candidates = append(candidates, path)
			}
		}
		return nil
	})

	if len(candidates) == 0 {
		return ""
	}

	// Limit to 3 files
	if len(candidates) > 3 {
		candidates = candidates[:3]
	}

	cutoff := time.Now().AddDate(0, 0, -decisionLookbackDays)
	var recentEntries []string
	seen := make(map[string]bool)

	for _, path := range candidates {
		entries := extractRecentDecisionEntries(path, cutoff)
		for _, e := range entries {
			// Deduplicate by first 100 chars of content
			key := e
			if len(key) > 100 {
				key = key[:100]
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			recentEntries = append(recentEntries, e)
		}
	}

	if len(recentEntries) == 0 {
		return ""
	}

	result := "## Active Decisions (last 7 days)\n" + strings.Join(recentEntries, "\n")
	if len(result) > decisionsMaxChars {
		result = result[:decisionsMaxChars]
	}
	return result
}

// extractRecentDecisionEntries reads a decision log file and returns entries
// from after the cutoff date. Reads the last 1500 chars of the file to
// focus on recent entries.
func extractRecentDecisionEntries(path string, cutoff time.Time) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	content := string(data)

	// Focus on recent entries: read last portion of file
	if len(content) > 6000 {
		content = content[len(content)-6000:]
	}

	lines := strings.Split(content, "\n")
	var entries []string
	var currentEntry []string
	var currentDate time.Time
	inEntry := false

	for _, line := range lines {
		// Detect date headers: "## 2026-02-02" or "### 2026-02-02"
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			// Flush previous entry if it's recent
			if inEntry && !currentDate.IsZero() && currentDate.After(cutoff) {
				entries = append(entries, strings.Join(currentEntry, "\n"))
			}

			// Try to parse date from this heading
			heading := strings.TrimLeft(trimmed, "# ")
			parsed := tryParseDate(heading)
			if !parsed.IsZero() {
				currentDate = parsed
				currentEntry = []string{line}
				inEntry = true
				continue
			}

			// Not a date heading — could be a decision title under a date
			if inEntry {
				currentEntry = append(currentEntry, line)
			}
			continue
		}

		if inEntry {
			currentEntry = append(currentEntry, line)
		}
	}

	// Flush last entry
	if inEntry && !currentDate.IsZero() && currentDate.After(cutoff) {
		entries = append(entries, strings.Join(currentEntry, "\n"))
	}

	return entries
}

// tryParseDate attempts to parse a date from a heading string.
// Handles formats like "2026-02-02", "2026-02-02 — Title", etc.
func tryParseDate(s string) time.Time {
	s = strings.TrimSpace(s)

	// Try the first 10 characters as a date
	if len(s) >= 10 {
		dateStr := s[:10]
		for _, layout := range []string{"2006-01-02", "2006/01/02"} {
			if t, err := time.Parse(layout, dateStr); err == nil {
				return t
			}
		}
	}

	return time.Time{}
}

// findStaleNotesSection reuses the existing staleness check logic.
func findStaleNotesSection(db *store.DB) string {
	stale := memory.FindStaleNotes(db, 5, true)
	if len(stale) == 0 {
		return ""
	}

	contextText := memory.FormatStaleNotesContext(stale)
	if contextText == "" {
		return ""
	}

	result := "## Stale Notes\n" + contextText
	if len(result) > staleNotesMaxChars {
		result = result[:staleNotesMaxChars]
	}
	return result
}
