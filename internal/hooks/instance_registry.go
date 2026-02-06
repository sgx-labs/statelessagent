package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
)

// instanceInfo represents a registered Claude Code session instance.
type instanceInfo struct {
	SessionID string `json:"sessionId"`
	Machine   string `json:"machine"`
	Started   string `json:"started"`
	Updated   string `json:"updated"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
}

// instancesDir returns the path to the instances directory (.same/instances/).
func instancesDir() string {
	return filepath.Join(filepath.Dir(config.DataDir()), "instances")
}

// sanitizeSessionID strips dangerous characters from a session ID before
// using it as a filename. Rejects path separators, "..", and null bytes to
// prevent path traversal. Returns "" if the ID is empty or entirely unsafe.
func sanitizeSessionID(id string) string {
	if id == "" {
		return ""
	}
	// Remove null bytes, path separators, and control characters.
	var b strings.Builder
	for _, r := range id {
		if r == 0 || r == '/' || r == '\\' || r == ':' || r < 0x20 {
			continue
		}
		b.WriteRune(r)
	}
	safe := b.String()
	// Reject ".." sequences that survived stripping.
	safe = strings.ReplaceAll(safe, "..", "")
	if safe == "" || safe == "." {
		return ""
	}
	// Cap length to prevent overly long filenames.
	if len(safe) > 255 {
		safe = safe[:255]
	}
	return safe
}

// registerInstance creates a JSON file for the current session so other
// instances can discover it. Fails silently on any error.
func registerInstance(sessionID string, initialContext string) {
	safeID := sanitizeSessionID(sessionID)
	if safeID == "" {
		return
	}

	dir := instancesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "same: instance registry: mkdir: %v\n", err)
		return
	}

	hostname := config.MachineName()

	summary := initialContext
	if len(summary) > 200 {
		summary = summary[:200]
	}

	now := time.Now().UTC().Format(time.RFC3339)

	info := instanceInfo{
		SessionID: sessionID,
		Machine:   hostname,
		Started:   now,
		Updated:   now,
		Summary:   summary,
		Status:    "active",
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: instance registry: marshal: %v\n", err)
		return
	}

	path := filepath.Join(dir, safeID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "same: instance registry: write: %v\n", err)
	}
}

// findActiveInstances reads all instance JSON files and returns a formatted
// string describing other active sessions. Skips the current session and any
// instances whose updated timestamp is older than 12 hours.
// Returns "" if no active peers are found. Output is capped at 500 chars.
func findActiveInstances(currentSessionID string) string {
	dir := instancesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	cutoff := time.Now().Add(-12 * time.Hour)
	var lines []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}

		var info instanceInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}

		// Skip current session
		if info.SessionID == currentSessionID {
			continue
		}

		// Skip stale instances (updated more than 12 hours ago)
		updated, err := time.Parse(time.RFC3339, info.Updated)
		if err != nil {
			continue
		}
		if updated.Before(cutoff) {
			continue
		}

		started, err := time.Parse(time.RFC3339, info.Started)
		if err != nil {
			continue
		}

		line := fmt.Sprintf("- %s (started %s): %s", info.Machine, relativeTime(started), info.Summary)
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return ""
	}

	result := "## Active Instances\n" + strings.Join(lines, "\n")
	if len(result) > 500 {
		result = result[:500]
	}
	return result
}

// cleanStaleInstances removes instance JSON files whose updated timestamp
// is older than 24 hours. Does not remove the current session's file.
// Fails silently on any error.
func cleanStaleInstances(currentSessionID string) {
	dir := instancesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-24 * time.Hour)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var info instanceInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}

		// Never delete the current session
		if info.SessionID == currentSessionID {
			continue
		}

		updated, err := time.Parse(time.RFC3339, info.Updated)
		if err != nil {
			continue
		}

		if updated.Before(cutoff) {
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "same: instance registry: clean: %v\n", err)
			}
		}
	}
}

// relativeTime formats a timestamp as a short relative duration string
// (e.g. "5m ago", "3h ago", "2d ago").
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
