package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// sessionsIndex represents the top-level structure of Claude Code's
// sessions-index.json file.
type sessionsIndex struct {
	Version int            `json:"version"`
	Entries []sessionEntry `json:"entries"`
}

// sessionEntry represents a single session in the sessions index.
type sessionEntry struct {
	SessionID    string `json:"sessionId"`
	FullPath     string `json:"fullPath"`
	FileMtime    int64  `json:"fileMtime"`
	FirstPrompt  string `json:"firstPrompt"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"messageCount"`
	Created      string `json:"created"`
	Modified     string `json:"modified"`
	GitBranch    string `json:"gitBranch"`
	ProjectPath  string `json:"projectPath"`
	IsSidechain  bool   `json:"isSidechain"`
}

const (
	continuityMaxChars      = 800
	firstPromptMaxChars     = 150
	currentSessionThreshold = 60 * time.Second
	// sessionsIndexMaxBytes is the maximum size we'll read for sessions-index.json.
	// Claude Code indexes are typically <100KB; 5MB guards against anomalies.
	sessionsIndexMaxBytes = 5 * 1024 * 1024
)

// findPreviousSessionContext reads Claude Code's session history and returns
// a formatted summary of the most recent previous session for the current
// project. Returns "" if no previous session is found or on any error.
func findPreviousSessionContext(currentSessionID string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	hash := claudeProjectHash(cwd)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	indexPath := filepath.Join(homeDir, ".claude", "projects", hash, "sessions-index.json")

	// Check file size before reading to avoid unbounded memory allocation.
	fi, err := os.Stat(indexPath)
	if err != nil {
		return ""
	}
	if fi.Size() > sessionsIndexMaxBytes {
		fmt.Fprintf(os.Stderr, "same: sessions-index.json too large (%d bytes), skipping\n", fi.Size())
		return ""
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}

	var idx sessionsIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return ""
	}

	if len(idx.Entries) == 0 {
		return ""
	}

	// Sort entries by modified time descending (most recent first).
	sort.Slice(idx.Entries, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, idx.Entries[i].Modified)
		tj, _ := time.Parse(time.RFC3339, idx.Entries[j].Modified)
		return ti.After(tj)
	})

	// Find the most recent entry that is NOT the current session.
	var prev *sessionEntry
	now := time.Now()

	for i := range idx.Entries {
		e := &idx.Entries[i]

		// If we have a current session ID, skip entries that match it.
		if currentSessionID != "" && e.SessionID == currentSessionID {
			continue
		}

		// If no current session ID provided, skip the most recently modified
		// entry if it was modified within the last 60 seconds (likely the
		// current session).
		if currentSessionID == "" {
			modTime, err := time.Parse(time.RFC3339, e.Modified)
			if err == nil && now.Sub(modTime) < currentSessionThreshold {
				continue
			}
		}

		prev = e
		break
	}

	if prev == nil {
		return ""
	}

	// Format the modified time for display.
	whenStr := prev.Modified
	if modTime, err := time.Parse(time.RFC3339, prev.Modified); err == nil {
		whenStr = modTime.Local().Format("Jan 2, 3:04pm")
	}

	// Truncate firstPrompt if needed.
	firstPrompt := prev.FirstPrompt
	if len(firstPrompt) > firstPromptMaxChars {
		firstPrompt = firstPrompt[:firstPromptMaxChars-3] + "..."
	}

	result := fmt.Sprintf(
		"## Previous Session\n**Summary:** %s\n**When:** %s\n**Messages:** %d\n**First prompt:** %s",
		prev.Summary,
		whenStr,
		prev.MessageCount,
		firstPrompt,
	)

	// Enforce total budget.
	if len(result) > continuityMaxChars {
		result = result[:continuityMaxChars]
	}

	return result
}

// claudeProjectHash converts a directory path to Claude Code's project hash
// format. All path separators are replaced with dashes, and the result starts
// with a leading dash.
func claudeProjectHash(cwd string) string {
	result := strings.ReplaceAll(cwd, "/", "-")

	if runtime.GOOS == "windows" {
		result = strings.ReplaceAll(result, `\`, "-")
		result = strings.ReplaceAll(result, ":", "-")
	}

	if !strings.HasPrefix(result, "-") {
		result = "-" + result
	}

	return result
}
