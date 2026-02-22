package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
)

// HandoffResult holds the result of writing a handoff note.
type HandoffResult struct {
	Path      string `json:"path"`
	SessionID string `json:"session_id"`
	Machine   string `json:"machine"`
	Written   string `json:"written"`
}

// handoffData holds all extracted session data for handoff generation.
type handoffData struct {
	Topics       []string
	Decisions    []string
	NotesCreated []string
	FilesChanged []string
	NextSteps    []string
	ToolUsage    string
	SessionStats string
	SessionID    string
	Machine      string
}

// AutoHandoffFromTranscript generates a handoff note from a transcript file.
func AutoHandoffFromTranscript(transcriptPath string, sessionID string) *HandoffResult {
	inputs := GetSessionSummaryInputs(transcriptPath)

	msgCount, _ := inputs["message_count"].(int)
	if msgCount < 3 {
		return nil
	}

	userMsgs, _ := inputs["user_messages"].([]string)
	assistantMsgs, _ := inputs["assistant_messages"].([]string)
	toolCalls, _ := inputs["tool_calls"].([]ToolCall)
	filesChanged, _ := inputs["files_changed"].([]string)

	data := handoffData{
		SessionID: sessionID,
		Machine:   getMachineName(),
	}

	// --- Topics: up to 8 user messages, word-boundary truncation, deduplicated ---
	seen := make(map[string]bool)
	for _, msg := range userMsgs {
		if len(data.Topics) >= 8 {
			break
		}
		summary := truncateAtWordBoundary(msg, 150)
		if summary == "" {
			continue
		}
		// Deduplicate by first 50 chars
		key := summary
		if len(key) > 50 {
			key = key[:50]
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		data.Topics = append(data.Topics, summary)
	}

	// --- Decisions: extract titles from save_decision tool calls ---
	data.Decisions = extractDecisionTitles(toolCalls)

	// --- Notes created: extract paths from save_note tool calls ---
	data.NotesCreated = extractSavedNotePaths(toolCalls)

	// --- Files changed: filter artifacts ---
	data.FilesChanged = filterMeaningfulFiles(filesChanged)
	// Cap at 20 files, note how many were dropped
	if len(data.FilesChanged) > 20 {
		remaining := len(data.FilesChanged) - 20
		data.FilesChanged = data.FilesChanged[:20]
		data.FilesChanged = append(data.FilesChanged, fmt.Sprintf("...and %d more", remaining))
	}

	// --- Tool usage: group by name, sort by frequency, top 10 ---
	toolCounts := make(map[string]int)
	for _, tc := range toolCalls {
		name := tc.Tool
		// Simplify MCP tool names: mcp__same__search_notes → same:search_notes
		if strings.HasPrefix(name, "mcp__same__") {
			name = "same:" + strings.TrimPrefix(name, "mcp__same__")
		}
		toolCounts[name]++
	}
	if len(toolCounts) > 0 {
		type toolEntry struct {
			name  string
			count int
		}
		var entries []toolEntry
		for name, count := range toolCounts {
			entries = append(entries, toolEntry{name, count})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].count > entries[j].count
		})
		if len(entries) > 10 {
			entries = entries[:10]
		}
		var parts []string
		for _, e := range entries {
			parts = append(parts, fmt.Sprintf("%s (%d)", e.name, e.count))
		}
		data.ToolUsage = strings.Join(parts, ", ")
	}

	// --- Next steps: extract from last assistant messages ---
	data.NextSteps = extractNextSteps(assistantMsgs)

	// --- Session stats ---
	data.SessionStats = fmt.Sprintf("%d user, %d assistant messages · %d tool calls · %d files",
		len(userMsgs), len(assistantMsgs), len(toolCalls), len(data.FilesChanged))

	return writeHandoffFromData(&data)
}

// writeHandoffFromData generates and writes the handoff file.
func writeHandoffFromData(data *handoffData) *HandoffResult {
	if data.SessionID == "" {
		data.SessionID = generateSessionID()
	}
	if data.Machine == "" {
		data.Machine = getMachineName()
	}

	content := generateRichHandoff(data)

	// Use date + session prefix so the same session overwrites its handoff
	// instead of creating a new file every time Stop fires.
	sessionShort := data.SessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	filename := time.Now().Format("2006-01-02") + "-" + sessionShort + "-handoff.md"
	relativePath := filepath.Join(config.HandoffDirectory(), filename)

	// SECURITY: Validate the resolved path stays inside the vault boundary.
	absPath, ok := config.SafeVaultSubpath(relativePath)
	if !ok {
		fmt.Fprintf(os.Stderr, "same: handoff path is outside your notes folder — skipping\n")
		return nil
	}
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "same: cannot create handoff directory %q: %v\n", dir, err)
		return nil
	}

	if err := os.WriteFile(absPath, []byte(content), 0o600); err != nil {
		return nil
	}

	return &HandoffResult{
		Path:      relativePath,
		SessionID: data.SessionID,
		Machine:   data.Machine,
		Written:   absPath,
	}
}

// generateRichHandoff produces the markdown content for a rich handoff note.
func generateRichHandoff(data *handoffData) string {
	now := time.Now()
	timestamp := now.UTC().Format(time.RFC3339)

	var b strings.Builder

	// YAML frontmatter
	fmt.Fprintf(&b, "---\ntitle: Session Handoff %s\n", now.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "content_type: handoff\n")
	fmt.Fprintf(&b, "session_id: %s\n", data.SessionID)
	fmt.Fprintf(&b, "machine: %s\n", data.Machine)
	fmt.Fprintf(&b, "created: %s\n", timestamp)
	fmt.Fprintf(&b, "tags:\n  - handoff\n  - auto-generated\n---\n\n")

	// Title
	fmt.Fprintf(&b, "# Session Handoff — %s\n\n", now.Format("2006-01-02"))

	// Topics
	if len(data.Topics) > 0 {
		b.WriteString("## What we worked on\n")
		for _, t := range data.Topics {
			fmt.Fprintf(&b, "- %s\n", t)
		}
		b.WriteString("\n")
	}

	// Decisions (only if found)
	if len(data.Decisions) > 0 {
		b.WriteString("## Decisions made\n")
		for _, d := range data.Decisions {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}

	// Notes created (only if found)
	if len(data.NotesCreated) > 0 {
		b.WriteString("## Notes created/updated\n")
		for _, n := range data.NotesCreated {
			fmt.Fprintf(&b, "- `%s`\n", n)
		}
		b.WriteString("\n")
	}

	// Files changed (only if found)
	if len(data.FilesChanged) > 0 {
		b.WriteString("## Files changed\n")
		for _, f := range data.FilesChanged {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	// Next steps (only if found)
	if len(data.NextSteps) > 0 {
		b.WriteString("## Next steps\n")
		for _, s := range data.NextSteps {
			fmt.Fprintf(&b, "%s\n", s)
		}
		b.WriteString("\n")
	}

	// Session stats + tool usage
	if data.SessionStats != "" || data.ToolUsage != "" {
		b.WriteString("## Session\n")
		if data.SessionStats != "" {
			b.WriteString(data.SessionStats)
			b.WriteString("\n")
		}
		if data.ToolUsage != "" {
			fmt.Fprintf(&b, "\nTop tools: %s\n", data.ToolUsage)
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n*Auto-generated by SAME (Stateless Agent Memory Engine)*\n")

	return b.String()
}

// extractNextSteps scans the last few assistant messages for forward-looking
// lines — things like "next:", "TODO:", "remaining:", bullet points mentioning
// future work. Returns up to 5 items.
func extractNextSteps(assistantMsgs []string) []string {
	if len(assistantMsgs) == 0 {
		return nil
	}

	// Check last 3 assistant messages (most likely to contain wrap-up)
	start := len(assistantMsgs) - 3
	if start < 0 {
		start = 0
	}

	markers := []string{
		"next step", "next:", "todo:", "todo ", "remaining:",
		"still need", "left to do", "not yet", "pending:",
		"should be", "ready to", "needs to", "want to",
		"follow up", "follow-up",
	}

	var steps []string
	seen := make(map[string]bool)

	for _, msg := range assistantMsgs[start:] {
		lines := strings.Split(msg, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Only consider bullet points or numbered items
			if !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ") &&
				!(len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.') {
				continue
			}
			lower := strings.ToLower(trimmed)
			hasMarker := false
			for _, m := range markers {
				if strings.Contains(lower, m) {
					hasMarker = true
					break
				}
			}
			if !hasMarker {
				continue
			}
			// Clean up and deduplicate
			step := truncateAtWordBoundary(trimmed, 150)
			key := step
			if len(key) > 50 {
				key = key[:50]
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			steps = append(steps, step)
			if len(steps) >= 5 {
				return steps
			}
		}
	}

	return steps
}

// truncateAtWordBoundary truncates a string at a word boundary, up to maxChars.
// Returns the first line only (splits on newline), then truncates to word boundary.
func truncateAtWordBoundary(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	// Take first line only
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[:nl]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= maxChars {
		return s
	}

	truncated := s[:maxChars]

	// Prefer sentence boundaries when available.
	sentenceIdx := strings.LastIndexAny(truncated, ".!?")
	if sentenceIdx >= maxChars/2 {
		return strings.TrimSpace(truncated[:sentenceIdx+1])
	}

	// Otherwise use a word boundary.
	lastSpace := strings.LastIndexAny(truncated, " \t")
	if lastSpace > 0 {
		return strings.TrimSpace(truncated[:lastSpace])
	}

	// Fallback for long uninterrupted tokens (for example, URLs).
	return strings.TrimSpace(truncated)
}

func extractDecisionTitles(toolCalls []ToolCall) []string {
	var decisions []string
	seen := make(map[string]bool)
	for _, tc := range toolCalls {
		if !isSaveDecisionTool(tc.Tool) || tc.Input == nil {
			continue
		}
		title, _ := tc.Input["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		title = truncateAtWordBoundary(title, 180)
		key := strings.ToLower(title)
		if seen[key] {
			continue
		}
		seen[key] = true
		decisions = append(decisions, title)
	}
	return decisions
}

func extractSavedNotePaths(toolCalls []ToolCall) []string {
	var notes []string
	seen := make(map[string]bool)
	for _, tc := range toolCalls {
		if !isSaveNoteTool(tc.Tool) || tc.Input == nil {
			continue
		}
		path, _ := tc.Input["path"].(string)
		path = normalizeNotePath(path)
		if path == "" || !isSafeHandoffNotePath(path) {
			continue
		}
		key := strings.ToLower(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		notes = append(notes, path)
	}
	return notes
}

func filterMeaningfulFiles(filesChanged []string) []string {
	var files []string
	seen := make(map[string]bool)
	for _, f := range filesChanged {
		normalized := normalizeNotePath(f)
		if normalized == "" || !isMeaningfulFilePath(normalized) {
			continue
		}
		key := strings.ToLower(normalized)
		if seen[key] {
			continue
		}
		seen[key] = true
		files = append(files, normalized)
	}
	return files
}

func isSaveDecisionTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mcp__same__save_decision", "same:save_decision", "save_decision":
		return true
	default:
		return false
	}
}

func isSaveNoteTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mcp__same__save_note", "same:save_note", "save_note":
		return true
	default:
		return false
	}
}

func normalizeNotePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}
	return path
}

func isSafeHandoffNotePath(path string) bool {
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) {
		return false
	}
	if len(path) >= 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' {
		return false
	}

	upper := strings.ToUpper(path)
	if strings.HasPrefix(upper, "_PRIVATE/") || upper == "_PRIVATE" {
		return false
	}

	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		// Match MCP safety posture: reject writes to dot-directories/files.
		if strings.HasPrefix(part, ".") {
			return false
		}
	}

	_, ok := config.SafeVaultSubpath(path)
	return ok
}

func isMeaningfulFilePath(path string) bool {
	lower := strings.ToLower(path)
	if lower == "/dev/null" || lower == "dev/null" || lower == "nul" {
		return false
	}
	if strings.HasPrefix(lower, "tmp/") || strings.HasPrefix(lower, "temp/") || strings.HasPrefix(lower, "/tmp/") {
		return false
	}
	if strings.HasPrefix(lower, ".same/") || strings.HasPrefix(lower, ".git/") {
		return false
	}

	artifactSuffixes := []string{
		".tmp", ".temp", ".swp", ".swo", ".ds_store", ".log", ".orig", ".bak",
	}
	for _, suffix := range artifactSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	if strings.HasSuffix(path, "~") {
		return false
	}
	return true
}

// --- Legacy API (kept for MCP create_handoff compatibility) ---

// GenerateHandoffNote generates markdown content for a handoff note.
// Used by MCP create_handoff handler. New auto-handoffs use generateRichHandoff.
func GenerateHandoffNote(
	accomplishments []string,
	decisions []string,
	currentState string,
	nextSession string,
	filesChanged []string,
	sessionID string,
	machine string,
) string {
	now := time.Now()
	timestamp := now.UTC().Format(time.RFC3339)
	if machine == "" {
		machine = getMachineName()
	}
	if sessionID == "" {
		sessionID = generateSessionID()
	}

	accomplishmentsMD := "- (none recorded)"
	if len(accomplishments) > 0 {
		lines := make([]string, len(accomplishments))
		for i, a := range accomplishments {
			lines[i] = "- " + a
		}
		accomplishmentsMD = strings.Join(lines, "\n")
	}

	decisionsMD := "- (none recorded)"
	if len(decisions) > 0 {
		lines := make([]string, len(decisions))
		for i, d := range decisions {
			lines[i] = "- " + d
		}
		decisionsMD = strings.Join(lines, "\n")
	}

	filesMD := "- (none)"
	if len(filesChanged) > 0 {
		lines := make([]string, len(filesChanged))
		for i, f := range filesChanged {
			lines[i] = "- `" + f + "`"
		}
		filesMD = strings.Join(lines, "\n")
	}

	if currentState == "" {
		currentState = "(not recorded)"
	}
	if nextSession == "" {
		nextSession = "(no specific next steps noted)"
	}

	return fmt.Sprintf(`---
title: Session Handoff %s
content_type: handoff
session_id: %s
machine: %s
created: %s
tags:
  - handoff
  - auto-generated
---

# Session Handoff

## Accomplishments
%s

## Decisions Made
%s

## Current State
%s

## Next Session
%s

## Files Changed
%s

---
*Auto-generated by SAME (Stateless Agent Memory Engine)*
`, now.Format("2006-01-02 15:04"), sessionID, machine, timestamp,
		accomplishmentsMD, decisionsMD, currentState, nextSession, filesMD)
}

// WriteHandoff generates and writes a handoff note to the vault.
// Legacy API — used only by tests and external callers. Auto-handoffs use writeHandoffFromData.
func WriteHandoff(
	accomplishments []string,
	decisions []string,
	currentState string,
	nextSession string,
	filesChanged []string,
	sessionID string,
) *HandoffResult {
	if sessionID == "" {
		sessionID = generateSessionID()
	}
	machine := getMachineName()

	content := GenerateHandoffNote(
		accomplishments, decisions, currentState, nextSession,
		filesChanged, sessionID, machine,
	)

	sessionShort := sessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	filename := time.Now().Format("2006-01-02") + "-" + sessionShort + "-handoff.md"
	relativePath := filepath.Join(config.HandoffDirectory(), filename)

	absPath, ok := config.SafeVaultSubpath(relativePath)
	if !ok {
		fmt.Fprintf(os.Stderr, "same: handoff path is outside your notes folder — skipping\n")
		return nil
	}
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "same: cannot create handoff directory %q: %v\n", dir, err)
		return nil
	}

	if err := os.WriteFile(absPath, []byte(content), 0o600); err != nil {
		return nil
	}

	return &HandoffResult{
		Path:      relativePath,
		SessionID: sessionID,
		Machine:   machine,
		Written:   absPath,
	}
}

func generateSessionID() string {
	ts := time.Now().Format("20060102-150405")
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return ts + "-00000000"
	}
	return ts + "-" + hex.EncodeToString(b)
}

func getMachineName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	// SECURITY: hash the hostname to avoid leaking PII into handoff notes.
	h := sha256.Sum256([]byte(name))
	return "machine-" + hex.EncodeToString(h[:4])
}
