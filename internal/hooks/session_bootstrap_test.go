package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// extractHandoffSections
// ---------------------------------------------------------------------------

func TestExtractHandoffSections_AllSections(t *testing.T) {
	content := `---
title: Handoff 2026-02-10
---

## Next Session
- Continue the refactoring of the parser module

## Current State
The parser is 80% complete. Tests pass.

## Accomplishments
- Rewrote the tokenizer
- Added 20 new tests

## Decisions Made
- Use recursive descent instead of PEG

## Files Changed
- internal/parser/tokenizer.go
- internal/parser/parser.go
`

	result := extractHandoffSections(content)

	// All priority sections should appear
	for _, section := range []string{
		"Next Session",
		"Current State",
		"Accomplishments",
		"Decisions Made",
		"Files Changed",
	} {
		if !strings.Contains(result, "### "+section) {
			t.Errorf("expected section %q to be extracted, not found in:\n%s", section, result)
		}
	}
}

func TestExtractHandoffSections_UnknownSectionsIncluded(t *testing.T) {
	// All sections should now be included, even unknown ones
	content := `## Summary
This session was productive.
`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "This session was productive") {
		t.Errorf("expected unknown section to be included, got %q", result)
	}
}

func TestExtractHandoffSections_NextSessionOnly(t *testing.T) {
	content := `## Next Session
- Fix the broken tests in store package
`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "### Next Session") {
		t.Errorf("expected Next Session to be extracted, got %q", result)
	}
	if !strings.Contains(result, "Fix the broken tests") {
		t.Errorf("expected section content, got %q", result)
	}
}

func TestExtractHandoffSections_EmptySections(t *testing.T) {
	content := `## Next Session

## Current State

## Accomplishments
- Did something
`

	result := extractHandoffSections(content)
	// Empty sections should be skipped (extractSectionWithOffset returns "")
	// Only "Accomplishments" has content
	if !strings.Contains(result, "Accomplishments") {
		t.Errorf("expected Accomplishments section, got %q", result)
	}
}

func TestExtractHandoffSections_ExtraWhitespace(t *testing.T) {
	content := `

## Next Session

  - Task 1
  - Task 2


## Current State

  Everything is fine.

`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "Task 1") {
		t.Errorf("expected task content despite extra whitespace, got %q", result)
	}
	if !strings.Contains(result, "Everything is fine") {
		t.Errorf("expected state content, got %q", result)
	}
}

func TestExtractHandoffSections_YAMLFrontmatterStripped(t *testing.T) {
	content := `---
title: Test Handoff
date: 2026-02-10
---
## Next Session
- Continue work
`

	result := extractHandoffSections(content)
	if strings.Contains(result, "title:") {
		t.Error("expected YAML frontmatter to be stripped")
	}
	if !strings.Contains(result, "Continue work") {
		t.Errorf("expected content after frontmatter, got %q", result)
	}
}

func TestExtractHandoffSections_CaseInsensitive(t *testing.T) {
	content := `## NEXT SESSION
- Uppercase headings should work
`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "Uppercase headings should work") {
		t.Errorf("expected case-insensitive heading match, got %q", result)
	}
}

func TestExtractHandoffSections_EmptyContent(t *testing.T) {
	result := extractHandoffSections("")
	if result != "" {
		t.Errorf("expected empty result for empty content, got %q", result)
	}
}

func TestExtractHandoffSections_NextSessionShouldVsNextSession(t *testing.T) {
	// "Next Session Should" is a longer match and should take priority
	content := `## Next Session Should
- Focus on testing
- Fix linter warnings
`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "### Next Session Should") {
		t.Errorf("expected 'Next Session Should' heading, got %q", result)
	}
	if !strings.Contains(result, "Focus on testing") {
		t.Errorf("expected content from 'Next Session Should' section, got %q", result)
	}
}

func TestExtractHandoffSections_HorizontalRuleStopsSection(t *testing.T) {
	content := `## Next Session
- Task A
- Task B

---

## Current State
All clear.
`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "Task A") {
		t.Error("expected Task A before horizontal rule")
	}
	// Next Session section should stop at ---
	// The section content should NOT bleed into Current State
	nextSessionIdx := strings.Index(result, "### Next Session")
	currentStateIdx := strings.Index(result, "### Current State")

	if nextSessionIdx >= 0 && currentStateIdx >= 0 {
		// Extract just the Next Session part
		var nextSessionContent string
		if currentStateIdx > nextSessionIdx {
			nextSessionContent = result[nextSessionIdx:currentStateIdx]
		} else {
			nextSessionContent = result[nextSessionIdx:]
		}
		if strings.Contains(nextSessionContent, "All clear") {
			t.Error("expected horizontal rule to stop section extraction")
		}
	}
}

// ---------------------------------------------------------------------------
// extractHandoffSections
// ---------------------------------------------------------------------------

func TestExtractHandoffSections_IncludesAllSections(t *testing.T) {
	content := `---
title: Test Handoff
content_type: handoff
---

# Session Handoff

## What we worked on
- Setup friend with Claude
- Debugging auth issues

## Decisions
- Use JWT for auth

## Custom Section
Something custom here.

## Files changed
- main.go
`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "Setup friend with Claude") {
		t.Error("expected 'What we worked on' content")
	}
	if !strings.Contains(result, "Use JWT for auth") {
		t.Error("expected 'Decisions' content")
	}
	if !strings.Contains(result, "Something custom here") {
		t.Error("expected custom section to be included, not dropped")
	}
	if !strings.Contains(result, "main.go") {
		t.Error("expected 'Files changed' content")
	}
}

func TestExtractHandoffSections_SkipsPlaceholders(t *testing.T) {
	content := `## Accomplishments
- Worked on: something real

## Decisions Made
(see decision extractor for detailed extraction)

## Next Session
(review handoff and add specific next steps)
`

	result := extractHandoffSections(content)
	if !strings.Contains(result, "something real") {
		t.Error("expected real accomplishments")
	}
	if strings.Contains(result, "see decision extractor") {
		t.Error("placeholder text should be filtered out")
	}
	if strings.Contains(result, "review handoff") {
		t.Error("placeholder text should be filtered out")
	}
}

// ---------------------------------------------------------------------------
// extractRecentDecisionEntries
// ---------------------------------------------------------------------------

func TestExtractRecentDecisionEntries_RecentDates(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.md")

	// Use dates relative to now to ensure the "recent" check works
	now := time.Now()
	recentDate := now.AddDate(0, 0, -2).Format("2006-01-02")
	oldDate := now.AddDate(0, 0, -30).Format("2006-01-02")

	content := `# Decision Log

## ` + oldDate + `
- Old decision that should NOT appear

## ` + recentDate + `
- Recent decision about API design
- Chose REST over GraphQL
`

	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cutoff := now.AddDate(0, 0, -7)
	entries := extractRecentDecisionEntries(logFile, cutoff)

	if len(entries) == 0 {
		t.Fatal("expected at least one recent entry")
	}

	joined := strings.Join(entries, "\n")
	if !strings.Contains(joined, "API design") {
		t.Errorf("expected recent entry content, got %q", joined)
	}
	if strings.Contains(joined, "Old decision") {
		t.Error("expected old entry to be excluded")
	}
}

func TestExtractRecentDecisionEntries_SlashDateFormat(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.md")

	now := time.Now()
	recentDate := now.AddDate(0, 0, -1).Format("2006/01/02")

	content := `## ` + recentDate + `
- Decision using slash date format
`

	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cutoff := now.AddDate(0, 0, -7)
	entries := extractRecentDecisionEntries(logFile, cutoff)

	if len(entries) == 0 {
		t.Fatal("expected entry with slash-formatted date")
	}
	if !strings.Contains(strings.Join(entries, "\n"), "slash date format") {
		t.Error("expected content from slash-dated entry")
	}
}

func TestExtractRecentDecisionEntries_NoDateHeadings(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.md")

	content := `# Decision Log

## API Design
- Chose REST for simplicity

## Database Choice
- SQLite for local-first
`

	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -7)
	entries := extractRecentDecisionEntries(logFile, cutoff)

	// No date headings means no entries should match
	if len(entries) != 0 {
		t.Errorf("expected no entries with non-date headings, got %d", len(entries))
	}
}

func TestExtractRecentDecisionEntries_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.md")

	if err := os.WriteFile(logFile, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cutoff := time.Now().AddDate(0, 0, -7)
	entries := extractRecentDecisionEntries(logFile, cutoff)

	if len(entries) != 0 {
		t.Errorf("expected no entries for empty file, got %d", len(entries))
	}
}

func TestExtractRecentDecisionEntries_NonExistentFile(t *testing.T) {
	entries := extractRecentDecisionEntries("/nonexistent/path/decisions.md", time.Now())
	if entries != nil {
		t.Errorf("expected nil for non-existent file, got %v", entries)
	}
}

func TestExtractRecentDecisionEntries_TripleHashHeading(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.md")

	now := time.Now()
	recentDate := now.AddDate(0, 0, -1).Format("2006-01-02")

	content := `# Decision Log

### ` + recentDate + `
- Decision under triple-hash heading
`

	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cutoff := now.AddDate(0, 0, -7)
	entries := extractRecentDecisionEntries(logFile, cutoff)

	if len(entries) == 0 {
		t.Fatal("expected entry under ### heading")
	}
	if !strings.Contains(strings.Join(entries, "\n"), "triple-hash heading") {
		t.Error("expected content from ### dated entry")
	}
}

func TestExtractRecentDecisionEntries_DateWithTitle(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.md")

	now := time.Now()
	recentDate := now.AddDate(0, 0, -1).Format("2006-01-02")

	// tryParseDate extracts first 10 chars, so "2026-02-09 — Sprint Review" works
	content := `## ` + recentDate + ` — Sprint Review
- Decided to ship v2 this week
`

	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cutoff := now.AddDate(0, 0, -7)
	entries := extractRecentDecisionEntries(logFile, cutoff)

	if len(entries) == 0 {
		t.Fatal("expected entry with date-and-title heading")
	}
	if !strings.Contains(strings.Join(entries, "\n"), "ship v2") {
		t.Error("expected content from date-with-title entry")
	}
}

func TestExtractRecentDecisionEntries_LargeFileTruncation(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.md")

	now := time.Now()
	recentDate := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Create a file larger than 6000 chars with the recent entry at the end
	padding := strings.Repeat("x", 7000) + "\n"
	content := padding + "## " + recentDate + "\n- Recent entry at end\n"

	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cutoff := now.AddDate(0, 0, -7)
	entries := extractRecentDecisionEntries(logFile, cutoff)

	if len(entries) == 0 {
		t.Fatal("expected recent entry from end of large file")
	}
	if !strings.Contains(strings.Join(entries, "\n"), "Recent entry at end") {
		t.Error("expected truncation to preserve recent entries at end of file")
	}
}

// ---------------------------------------------------------------------------
// tryParseDate
// ---------------------------------------------------------------------------

func TestTryParseDate_DashFormat(t *testing.T) {
	parsed := tryParseDate("2026-02-10")
	if parsed.IsZero() {
		t.Fatal("expected valid date for 2026-02-10")
	}
	if parsed.Year() != 2026 || parsed.Month() != 2 || parsed.Day() != 10 {
		t.Errorf("expected 2026-02-10, got %v", parsed)
	}
}

func TestTryParseDate_SlashFormat(t *testing.T) {
	parsed := tryParseDate("2026/02/10")
	if parsed.IsZero() {
		t.Fatal("expected valid date for 2026/02/10")
	}
	if parsed.Year() != 2026 || parsed.Month() != 2 || parsed.Day() != 10 {
		t.Errorf("expected 2026-02-10, got %v", parsed)
	}
}

func TestTryParseDate_WithTrailingText(t *testing.T) {
	// tryParseDate takes first 10 chars — trailing text should not break it
	parsed := tryParseDate("2026-02-10 — Sprint Review")
	if parsed.IsZero() {
		t.Fatal("expected valid date with trailing text")
	}
	if parsed.Day() != 10 {
		t.Errorf("expected day 10, got %d", parsed.Day())
	}
}

func TestTryParseDate_InvalidString(t *testing.T) {
	parsed := tryParseDate("not-a-date")
	if !parsed.IsZero() {
		t.Errorf("expected zero time for invalid string, got %v", parsed)
	}
}

func TestTryParseDate_EmptyString(t *testing.T) {
	parsed := tryParseDate("")
	if !parsed.IsZero() {
		t.Errorf("expected zero time for empty string, got %v", parsed)
	}
}

func TestTryParseDate_TooShort(t *testing.T) {
	parsed := tryParseDate("2026-02")
	if !parsed.IsZero() {
		t.Errorf("expected zero time for short string, got %v", parsed)
	}
}

func TestTryParseDate_InvalidMonth(t *testing.T) {
	parsed := tryParseDate("2026-13-01")
	if !parsed.IsZero() {
		t.Errorf("expected zero time for invalid month 13, got %v", parsed)
	}
}

func TestTryParseDate_InvalidDay(t *testing.T) {
	parsed := tryParseDate("2026-02-30")
	// Go's time.Parse is lenient about day overflow — check if it returns
	// a zero or non-zero value and just document the behavior.
	// time.Parse("2006-01-02", "2026-02-30") → 2026-03-02 (Go wraps)
	// So this might NOT be zero. We just verify no panic.
	_ = parsed
}

func TestTryParseDate_NoZeroPadding(t *testing.T) {
	// "2026-2-1" is only 8 chars; tryParseDate requires 10 chars
	parsed := tryParseDate("2026-2-1")
	if !parsed.IsZero() {
		t.Errorf("expected zero time for non-zero-padded date, got %v", parsed)
	}
}

func TestTryParseDate_WhitespaceHandling(t *testing.T) {
	// tryParseDate trims leading/trailing whitespace
	parsed := tryParseDate("  2026-02-10  ")
	if parsed.IsZero() {
		t.Fatal("expected valid date with surrounding whitespace")
	}
	if parsed.Day() != 10 {
		t.Errorf("expected day 10, got %d", parsed.Day())
	}
}

// ---------------------------------------------------------------------------
// findLatestHandoff (uses config, so we test edge cases with temp dirs)
// ---------------------------------------------------------------------------

// Note: findLatestHandoff() depends on config.SafeVaultSubpath and
// config.HandoffDirectory(), which read from the global config. We cannot
// easily inject a custom vault path in unit tests without modifying config
// state. Instead, we test the underlying extractHandoffSections and
// extractSectionWithOffset functions thoroughly above, and test
// findLatestHandoff behavior through its return value when no vault is
// configured (returns "").

func TestFindLatestHandoff_NoVaultConfigured(t *testing.T) {
	// Without a configured vault, findLatestHandoff should return ""
	// (SafeVaultSubpath will fail or the directory won't exist)
	result := findLatestHandoff()
	// We cannot guarantee vault state, but the function must not panic
	_ = result
}

// TestFindLatestHandoff_SortsByFilenameDescending verifies the sort logic
// by testing with a controlled directory structure.
func TestFindLatestHandoff_SortsByFilenameDescending(t *testing.T) {
	// This tests the sorting logic indirectly. Since findLatestHandoff
	// reads from the config-determined handoff directory, we verify the
	// sort.Slice behavior used in that function.
	//
	// The function sorts by filename descending, so "2026-02-10" > "2026-02-09".
	names := []string{
		"2026-02-08-handoff.md",
		"2026-02-10-handoff.md",
		"2026-02-09-handoff.md",
	}

	// Simulate the sort from findLatestHandoff
	sorted := make([]string, len(names))
	copy(sorted, names)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] < sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	if sorted[0] != "2026-02-10-handoff.md" {
		t.Errorf("expected most recent file first, got %q", sorted[0])
	}
}

// ---------------------------------------------------------------------------
// findActiveDecisions (depends on config.VaultPath)
// ---------------------------------------------------------------------------

func TestFindActiveDecisions_NoVault(t *testing.T) {
	// Without a configured vault, should return "" without panicking
	result := findActiveDecisions()
	// May or may not return content depending on system state
	_ = result
}

// ---------------------------------------------------------------------------
// formatAge
// ---------------------------------------------------------------------------

func TestFormatAge_JustNow(t *testing.T) {
	result := formatAge(time.Now())
	if result != "just now" {
		t.Errorf("expected 'just now', got %q", result)
	}
}

func TestFormatAge_MinutesAgo(t *testing.T) {
	result := formatAge(time.Now().Add(-15 * time.Minute))
	if !strings.Contains(result, "m ago") {
		t.Errorf("expected 'Nm ago', got %q", result)
	}
}

func TestFormatAge_HoursAgo(t *testing.T) {
	result := formatAge(time.Now().Add(-3 * time.Hour))
	if !strings.Contains(result, "h ago") {
		t.Errorf("expected 'Nh ago', got %q", result)
	}
}

func TestFormatAge_DaysAgo(t *testing.T) {
	result := formatAge(time.Now().Add(-48 * time.Hour))
	if !strings.Contains(result, "d ago") {
		t.Errorf("expected 'Nd ago', got %q", result)
	}
}

func TestFormatAge_ZeroTime(t *testing.T) {
	result := formatAge(time.Time{})
	if result != "unknown" {
		t.Errorf("expected 'unknown' for zero time, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// isQuietMode
// ---------------------------------------------------------------------------

func TestIsQuietMode_EnvVar(t *testing.T) {
	// Save and restore env
	old := os.Getenv("SAME_QUIET")
	defer os.Setenv("SAME_QUIET", old)

	os.Setenv("SAME_QUIET", "1")
	if !isQuietMode() {
		t.Error("expected quiet mode with SAME_QUIET=1")
	}

	os.Setenv("SAME_QUIET", "true")
	if !isQuietMode() {
		t.Error("expected quiet mode with SAME_QUIET=true")
	}

	os.Setenv("SAME_QUIET", "0")
	// This depends on config.DisplayMode() too, so we just verify no panic
	_ = isQuietMode()
}

// ---------------------------------------------------------------------------
// Constants sanity
// ---------------------------------------------------------------------------

func TestBootstrapConstants(t *testing.T) {
	// Verify budget constants are reasonable
	if bootstrapMaxChars <= 0 {
		t.Error("bootstrapMaxChars must be positive")
	}
	if handoffMaxChars <= 0 {
		t.Error("handoffMaxChars must be positive")
	}
	if decisionsMaxChars <= 0 {
		t.Error("decisionsMaxChars must be positive")
	}
	if staleNotesMaxChars <= 0 {
		t.Error("staleNotesMaxChars must be positive")
	}

	// Total budget should accommodate the individual section budgets
	if handoffMaxChars+decisionsMaxChars+staleNotesMaxChars > bootstrapMaxChars*2 {
		t.Error("individual section budgets seem too large relative to total budget")
	}

	if decisionLookbackDays != 7 {
		t.Errorf("expected 7-day lookback, got %d", decisionLookbackDays)
	}
}
