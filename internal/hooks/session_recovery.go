package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// RecoverySource indicates where recovered context came from.
type RecoverySource int

const (
	RecoveryNone         RecoverySource = iota // No previous session found
	RecoverySessionIndex                       // IDE session index only (crash recovery)
	RecoveryInstance                           // Instance registry file
	RecoveryHandoff                            // Stop hook fired, full handoff written
)

// RecoveredSession holds context recovered from the previous session.
type RecoveredSession struct {
	Source       RecoverySource
	SessionID    string
	Summary      string
	FirstPrompt  string
	MessageCount int
	GitBranch    string
	EndedAt      time.Time
	HandoffText  string  // Only populated for RecoveryHandoff
	Completeness float64 // 0.0 = nothing, 0.3 = session index, 0.4 = instance, 1.0 = handoff
}

// RecoverPreviousSession attempts to recover context from the previous session
// using a priority cascade. Each source is checked in order — the first one
// that returns data wins. This ensures same-machine recovery even after crashes.
//
// Cascade:
//   1. Handoff note (Stop fired) → Completeness 1.0
//   2. Instance file with summary → Completeness 0.4
//   3. sessions-index.json (IDE-persisted) → Completeness 0.3
//   4. Nothing found → nil
func RecoverPreviousSession(db *store.DB, currentSessionID string) *RecoveredSession {
	// Source 1: Try handoff (richest source - Stop hook fired successfully)
	if rs := recoverFromHandoff(); rs != nil {
		// Record recovery outcome
		if db != nil {
			db.RecordRecovery(currentSessionID, rs.SessionID, "handoff", rs.Completeness)
		}
		return rs
	}

	// Source 2: Try instance files (first prompt was captured)
	if rs := recoverFromInstance(currentSessionID); rs != nil {
		if db != nil {
			db.RecordRecovery(currentSessionID, rs.SessionID, "instance", rs.Completeness)
		}
		return rs
	}

	// Source 3: Try sessions-index.json (IDE always persists this)
	if rs := recoverFromSessionIndex(currentSessionID); rs != nil {
		if db != nil {
			db.RecordRecovery(currentSessionID, rs.SessionID, "session_index", rs.Completeness)
		}
		return rs
	}

	// Nothing found
	if db != nil {
		db.RecordRecovery(currentSessionID, "", "none", 0.0)
	}
	return nil
}

// recoverFromHandoff checks for a handoff note from the previous session.
// This is the richest recovery source — it means Stop fired successfully.
func recoverFromHandoff() *RecoveredSession {
	handoffDir, ok := config.SafeVaultSubpath(config.HandoffDirectory())
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(handoffDir)
	if err != nil || len(entries) == 0 {
		return nil
	}

	// Collect markdown files
	var mdFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			mdFiles = append(mdFiles, e)
		}
	}
	if len(mdFiles) == 0 {
		return nil
	}

	// Sort by filename descending (date-prefixed)
	sort.Slice(mdFiles, func(i, j int) bool {
		return mdFiles[i].Name() > mdFiles[j].Name()
	})

	latest := mdFiles[0]
	latestPath := filepath.Join(handoffDir, latest.Name())

	// Check freshness — only use handoffs within configured max age
	info, err := latest.Info()
	if err != nil {
		return nil
	}
	maxAge := time.Duration(config.HandoffMaxAge()) * time.Hour
	if time.Since(info.ModTime()) > maxAge {
		return nil
	}

	data, err := os.ReadFile(latestPath)
	if err != nil {
		return nil
	}

	content := string(data)
	// Cap content size
	if len(content) > handoffMaxChars*2 {
		content = content[:handoffMaxChars*2]
	}

	extracted := extractHandoffSections(content)
	if extracted == "" {
		extracted = content
	}
	if len(extracted) > handoffMaxChars {
		extracted = extracted[:handoffMaxChars]
	}

	return &RecoveredSession{
		Source:       RecoveryHandoff,
		Summary:      "Handoff from previous session",
		HandoffText:  extracted,
		EndedAt:      info.ModTime(),
		Completeness: 1.0,
	}
}

// recoverFromInstance checks instance registry files for the most recent
// completed or active session (not the current one).
func recoverFromInstance(currentSessionID string) *RecoveredSession {
	dir := instancesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type candidate struct {
		info    instanceInfo
		updated time.Time
	}
	var candidates []candidate

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

		// Must have a summary to be useful
		if info.Summary == "" {
			continue
		}

		updated, err := time.Parse(time.RFC3339, info.Updated)
		if err != nil {
			continue
		}

		// Only consider instances within configured max age
		maxAge := time.Duration(config.HandoffMaxAge()) * time.Hour
		if time.Since(updated) > maxAge {
			continue
		}

		candidates = append(candidates, candidate{info: info, updated: updated})
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort by updated time descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].updated.After(candidates[j].updated)
	})

	best := candidates[0]
	return &RecoveredSession{
		Source:       RecoveryInstance,
		SessionID:    best.info.SessionID,
		Summary:      best.info.Summary,
		EndedAt:      best.updated,
		Completeness: 0.4,
	}
}

// recoverFromSessionIndex reads Claude Code's sessions-index.json to find
// the most recent previous session. This is the lowest-fidelity source but
// is always available because the IDE persists it regardless of exit method.
func recoverFromSessionIndex(currentSessionID string) *RecoveredSession {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	hash := claudeProjectHash(cwd)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	indexPath := filepath.Join(homeDir, ".claude", "projects", hash, "sessions-index.json")

	fi, err := os.Stat(indexPath)
	if err != nil {
		return nil
	}
	if fi.Size() > sessionsIndexMaxBytes {
		return nil
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil
	}

	var idx sessionsIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil
	}

	if len(idx.Entries) == 0 {
		return nil
	}

	// Sort by modified time descending
	sort.Slice(idx.Entries, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, idx.Entries[i].Modified)
		tj, _ := time.Parse(time.RFC3339, idx.Entries[j].Modified)
		return ti.After(tj)
	})

	// Find the most recent entry that is NOT the current session
	now := time.Now()
	for i := range idx.Entries {
		e := &idx.Entries[i]

		if currentSessionID != "" && e.SessionID == currentSessionID {
			continue
		}

		if currentSessionID == "" {
			modTime, err := time.Parse(time.RFC3339, e.Modified)
			if err == nil && now.Sub(modTime) < currentSessionThreshold {
				continue
			}
		}

		endedAt, _ := time.Parse(time.RFC3339, e.Modified)

		return &RecoveredSession{
			Source:       RecoverySessionIndex,
			SessionID:    e.SessionID,
			Summary:      e.Summary,
			FirstPrompt:  e.FirstPrompt,
			MessageCount: e.MessageCount,
			GitBranch:    e.GitBranch,
			EndedAt:      endedAt,
			Completeness: 0.3,
		}
	}

	return nil
}

// FormatRecoveryContext converts a RecoveredSession into the compact orientation
// block injected at SessionStart.
//
// High completeness (handoff): inject the handoff text with pointer to file
// Low completeness (crash): inject the summary + first prompt inline
func FormatRecoveryContext(rs *RecoveredSession) string {
	if rs == nil || rs.Source == RecoveryNone {
		return ""
	}

	var b strings.Builder

	switch rs.Source {
	case RecoveryHandoff:
		b.WriteString("## Previous Session (full handoff)\n")
		b.WriteString(rs.HandoffText)

	case RecoveryInstance:
		b.WriteString("## Previous Session (recovered from instance)\n")
		b.WriteString(fmt.Sprintf("**Summary:** %s\n", rs.Summary))
		if !rs.EndedAt.IsZero() {
			b.WriteString(fmt.Sprintf("**Last active:** %s\n", rs.EndedAt.Local().Format("Jan 2, 3:04pm")))
		}
		b.WriteString("\n_Note: Session ended without a full handoff. Context may be incomplete._\n")

	case RecoverySessionIndex:
		b.WriteString("## Previous Session (recovered from session index)\n")
		if rs.Summary != "" {
			b.WriteString(fmt.Sprintf("**Summary:** %s\n", rs.Summary))
		}
		if rs.FirstPrompt != "" {
			prompt := rs.FirstPrompt
			if len(prompt) > 150 {
				prompt = prompt[:147] + "..."
			}
			b.WriteString(fmt.Sprintf("**First prompt:** %s\n", prompt))
		}
		if rs.MessageCount > 0 {
			b.WriteString(fmt.Sprintf("**Messages:** %d\n", rs.MessageCount))
		}
		if rs.GitBranch != "" {
			b.WriteString(fmt.Sprintf("**Branch:** %s\n", rs.GitBranch))
		}
		if !rs.EndedAt.IsZero() {
			b.WriteString(fmt.Sprintf("**When:** %s\n", rs.EndedAt.Local().Format("Jan 2, 3:04pm")))
		}
		b.WriteString("\n_Note: Session ended without a handoff (terminal closed?). This is minimal recovery context._\n")
	}

	result := b.String()

	// Enforce budget — recovery context should be compact
	const recoveryMaxChars = 4000
	if len(result) > recoveryMaxChars {
		result = result[:recoveryMaxChars]
	}

	return result
}
