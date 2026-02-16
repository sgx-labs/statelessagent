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
	pinnedMaxChars       = 2000 // ~500 tokens — always included
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

	quiet := isQuietMode()
	var sections []string

	// Priority 0: Unified recovery (replaces separate session index + handoff lookup)
	// Uses a priority cascade: handoff → instance → session index
	recovered := RecoverPreviousSession(db, sessionID)
	if recovered != nil {
		if ctx := FormatRecoveryContext(recovered); ctx != "" {
			sections = append(sections, ctx)
			if !quiet {
				source := "session index"
				switch recovered.Source {
				case RecoveryHandoff:
					source = "handoff"
				case RecoveryInstance:
					source = "instance"
				}
				fmt.Fprintf(os.Stderr, "same: ← previous session loaded (%s, %s)\n", source, formatAge(recovered.EndedAt))
			}
		}
	}

	// Priority 0b: Active instances (other Claude Code sessions)
	if instances := findActiveInstances(sessionID); instances != "" {
		sections = append(sections, instances)
	}

	// Priority 1: Pinned notes (always included — user's most important context)
	if pinned := findPinnedNotesSection(db); pinned != "" {
		sections = append(sections, pinned)
		if !quiet {
			n := strings.Count(pinned, "\n- ") + 1
			fmt.Fprintf(os.Stderr, "same: 📌 %d pinned note(s) loaded\n", n)
		}
	}

	// Priority 2: Active decisions (last 7 days)
	if decisions := findActiveDecisions(); decisions != "" {
		sections = append(sections, decisions)
		if !quiet {
			n := strings.Count(decisions, "\n## ") + strings.Count(decisions, "\n### ")
			if n == 0 {
				n = 1
			}
			fmt.Fprintf(os.Stderr, "same: ↑ %d active decision(s) loaded\n", n)
		}
	}

	// Priority 3: Stale notes (reuse existing logic)
	if stale := findStaleNotesSection(db); stale != "" {
		sections = append(sections, stale)
		if !quiet {
			n := strings.Count(stale, "\n- ")
			if n == 0 {
				n = 1
			}
			fmt.Fprintf(os.Stderr, "same: ⚠ %d stale note(s) need review\n", n)
		}
	}

	if len(sections) == 0 {
		return nil
	}

	context := strings.Join(sections, "\n\n")

	// SECURITY: Sanitize XML tags that could break the session-bootstrap wrapper
	// or enable stored prompt injection via crafted handoff/decision content.
	context = sanitizeContextTags(context)

	// Enforce total budget
	if len(context) > bootstrapMaxChars {
		context = context[:bootstrapMaxChars]
	}

	return &HookOutput{
		SystemMessage: fmt.Sprintf(
			"\n<session-bootstrap>\n%s\n</session-bootstrap>\n",
			context,
		),
	}
}

// isQuietMode returns true if the user has set display mode to quiet.
func isQuietMode() bool {
	if os.Getenv("SAME_QUIET") == "1" || os.Getenv("SAME_QUIET") == "true" {
		return true
	}
	return config.DisplayMode() == "quiet"
}

// formatAge returns a human-readable age string like "2h ago" or "3d ago".
func formatAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
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

// extractHandoffSections pulls sections from a handoff note.
// All ## sections are included — known sections are ordered first for priority,
// then any remaining sections are appended in their original order.
func extractHandoffSections(content string) string {
	// Strip YAML frontmatter
	if strings.HasPrefix(content, "---") {
		if idx := strings.Index(content[3:], "---"); idx >= 0 {
			content = strings.TrimSpace(content[idx+6:])
		}
	}

	// Strip the top-level # heading (e.g. "# Session Handoff")
	if strings.HasPrefix(content, "# ") {
		if nl := strings.Index(content, "\n"); nl >= 0 {
			content = strings.TrimSpace(content[nl+1:])
		}
	}

	// Parse all ## sections with their byte offsets
	type section struct {
		heading string
		text    string
		offset  int
	}
	var allSections []section
	seen := make(map[int]bool)

	// Scan for all ## headings
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		heading := strings.TrimSpace(line[3:])
		if heading == "" {
			continue
		}

		// Collect body until next ## or --- or end
		var body []string
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") || strings.HasPrefix(lines[j], "---") {
				break
			}
			body = append(body, lines[j])
		}
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text == "" || text == "(none)" || text == "(none recorded)" || text == "(not recorded)" {
			continue
		}
		// Skip placeholder sections
		if strings.HasPrefix(text, "(see ") || strings.HasPrefix(text, "(review ") {
			continue
		}
		allSections = append(allSections, section{heading: heading, text: text, offset: i})
	}

	if len(allSections) == 0 {
		return ""
	}

	// Known sections in priority order (matched case-insensitively).
	// Includes both current and legacy section names for backward compatibility.
	priorityOrder := []string{
		"what we worked on",
		"accomplishments",
		"what was done",
		"decisions",
		"decisions made",
		"notes created",
		"notes created or updated",
		"next steps",
		"current state",
		"session",
		"files changed",
		"tool usage",
		"pending",
		"blockers",
		"next session should",
		"next session",
	}

	// Build priority map for ordering
	priorityMap := make(map[string]int)
	for i, name := range priorityOrder {
		priorityMap[name] = i
	}

	// Sort: known sections by priority, unknown sections after (in original order)
	sorted := make([]section, len(allSections))
	copy(sorted, allSections)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, oki := priorityMap[strings.ToLower(sorted[i].heading)]
		pj, okj := priorityMap[strings.ToLower(sorted[j].heading)]
		if oki && okj {
			return pi < pj
		}
		if oki {
			return true // known before unknown
		}
		if okj {
			return false // unknown after known
		}
		return sorted[i].offset < sorted[j].offset // preserve order for unknowns
	})

	var parts []string
	for _, s := range sorted {
		if seen[s.offset] {
			continue
		}
		seen[s.offset] = true
		parts = append(parts, "### "+s.heading+"\n"+s.text)
	}

	return strings.Join(parts, "\n\n")
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

// findPinnedNotesSection returns pinned notes formatted for session bootstrap.
// Pinned notes are the user's most important context — always included.
func findPinnedNotesSection(db *store.DB) string {
	pinned, err := db.GetPinnedNotes()
	if err != nil || len(pinned) == 0 {
		return ""
	}

	var parts []string
	totalChars := 0
	for _, rec := range pinned {
		text := rec.Text
		// Cap each note to keep total budget manageable
		if len(text) > 500 {
			text = text[:500] + "..."
		}
		entry := fmt.Sprintf("### %s\n%s", rec.Title, text)
		if totalChars+len(entry) > pinnedMaxChars {
			break
		}
		parts = append(parts, entry)
		totalChars += len(entry)
	}

	if len(parts) == 0 {
		return ""
	}

	return "## Pinned Notes\n" + strings.Join(parts, "\n\n")
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
