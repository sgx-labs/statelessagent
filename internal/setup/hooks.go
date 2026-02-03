package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sgx-labs/statelessagent/internal/cli"
)

// hookDefinitions are the SAME hooks to install in .claude/settings.json.
var hookDefinitions = map[string][]hookEntry{
	"UserPromptSubmit": {
		{Matcher: "", Hooks: []hookAction{{Type: "command", Command: "%s hook context-surfacing"}}},
	},
	"Stop": {
		{Matcher: "", Hooks: []hookAction{
			{Type: "command", Command: "%s hook decision-extractor"},
			{Type: "command", Command: "%s hook handoff-generator"},
			{Type: "command", Command: "%s hook feedback-loop"},
		}},
	},
	"SessionStart": {
		{Matcher: "", Hooks: []hookAction{
			{Type: "command", Command: "%s version --check"},
			{Type: "command", Command: "%s hook staleness-check"},
		}},
	},
}

type hookEntry struct {
	Matcher string       `json:"matcher"`
	Hooks   []hookAction `json:"hooks"`
}

type hookAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type claudeSettings struct {
	Hooks map[string][]hookEntry `json:"hooks"`
	// Preserve other fields
	Extra map[string]json.RawMessage `json:"-"`
}

// SetupHooks installs SAME hooks into .claude/settings.json.
func SetupHooks(vaultPath string) error {
	settingsPath := filepath.Join(vaultPath, ".claude", "settings.json")
	binaryPath := detectBinaryPath()

	// Load existing settings or create new
	existing := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(settingsPath); err == nil {
		json.Unmarshal(data, &existing)
	}

	// Parse existing hooks
	var existingHooks map[string][]hookEntry
	if raw, ok := existing["hooks"]; ok {
		json.Unmarshal(raw, &existingHooks)
	}
	if existingHooks == nil {
		existingHooks = make(map[string][]hookEntry)
	}

	// Build SAME hooks with the binary path
	sameHooks := buildHooks(binaryPath)

	// Merge: remove old SAME hooks, add new ones
	count := 0
	for event, entries := range sameHooks {
		merged := filterNonSAMEHooks(existingHooks[event], binaryPath)
		merged = append(merged, entries...)
		existingHooks[event] = merged
		for _, e := range entries {
			count += len(e.Hooks)
		}
	}

	// Write back
	hooksJSON, _ := json.Marshal(existingHooks)
	existing["hooks"] = json.RawMessage(hooksJSON)

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create .claude directory: %w", err)
	}

	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	fmt.Printf("  → .claude/settings.json (%d hooks)\n", count)
	return nil
}

// RemoveHooks removes SAME hooks from .claude/settings.json.
func RemoveHooks(vaultPath string) error {
	settingsPath := filepath.Join(vaultPath, ".claude", "settings.json")
	binaryPath := detectBinaryPath()

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	var existing map[string]json.RawMessage
	if err := json.Unmarshal(data, &existing); err != nil {
		return fmt.Errorf("parse settings: %w", err)
	}

	var existingHooks map[string][]hookEntry
	if raw, ok := existing["hooks"]; ok {
		json.Unmarshal(raw, &existingHooks)
	}
	if existingHooks == nil {
		fmt.Println("  No hooks found.")
		return nil
	}

	removed := 0
	for event, entries := range existingHooks {
		filtered := filterNonSAMEHooks(entries, binaryPath)
		removed += len(entries) - len(filtered)
		if len(filtered) == 0 {
			delete(existingHooks, event)
		} else {
			existingHooks[event] = filtered
		}
	}

	hooksJSON, _ := json.Marshal(existingHooks)
	existing["hooks"] = json.RawMessage(hooksJSON)

	data, _ = json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	fmt.Printf("  Removed %d SAME hook entries.\n", removed)
	return nil
}

// HooksInstalled checks if SAME hooks are installed and returns their status.
func HooksInstalled(vaultPath string) map[string]bool {
	settingsPath := filepath.Join(vaultPath, ".claude", "settings.json")
	result := map[string]bool{
		"context-surfacing":  false,
		"decision-extractor": false,
		"handoff-generator":  false,
		"feedback-loop":      false,
		"staleness-check":    false,
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return result
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return result
	}

	var hooks map[string][]hookEntry
	raw, ok := settings["hooks"]
	if !ok {
		return result
	}
	if err := json.Unmarshal(raw, &hooks); err != nil {
		return result
	}

	for _, entries := range hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				for name := range result {
					if containsSAMEHook(h.Command, name) {
						result[name] = true
					}
				}
			}
		}
	}

	return result
}

func buildHooks(binaryPath string) map[string][]hookEntry {
	result := make(map[string][]hookEntry)
	for event, entries := range hookDefinitions {
		var built []hookEntry
		for _, e := range entries {
			var actions []hookAction
			for _, a := range e.Hooks {
				actions = append(actions, hookAction{
					Type:    a.Type,
					Command: fmt.Sprintf(a.Command, binaryPath),
				})
			}
			built = append(built, hookEntry{
				Matcher: e.Matcher,
				Hooks:   actions,
			})
		}
		result[event] = built
	}
	return result
}

func filterNonSAMEHooks(entries []hookEntry, binaryPath string) []hookEntry {
	var filtered []hookEntry
	for _, entry := range entries {
		var nonSAME []hookAction
		for _, h := range entry.Hooks {
			if !isSAMEHook(h.Command) {
				nonSAME = append(nonSAME, h)
			}
		}
		if len(nonSAME) > 0 {
			entry.Hooks = nonSAME
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func isSAMEHook(command string) bool {
	return containsSAMEHook(command, "hook") ||
		containsSAMEHook(command, "version --check")
}

func containsSAMEHook(command, hookName string) bool {
	return contains(command, "same "+hookName) || contains(command, "same hook "+hookName)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func detectBinaryPath() string {
	// Check if 'same' is in PATH
	if p, err := exec.LookPath("same"); err == nil {
		return p
	}

	// Check common install locations
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "same"),
		filepath.Join(home, "go", "bin", "same"),
		"/usr/local/bin/same",
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Fall back to just "same" and hope it's in PATH at runtime
	return "same"
}

// setupHooksInteractive prompts and sets up hooks.
func setupHooksInteractive(vaultPath string, autoAccept bool) {
	if autoAccept || confirm("  Set up Claude Code hooks?", true) {
		if err := SetupHooks(vaultPath); err != nil {
			fmt.Printf("  %s!%s Could not set up hooks: %v\n",
				cli.Yellow, cli.Reset, err)
		}
	} else {
		fmt.Println("  Skipped. Run 'same setup hooks' later.")
	}
}
