package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- HooksInstalled tests ---

func TestHooksInstalled_NoFile(t *testing.T) {
	dir := t.TempDir()
	result := HooksInstalled(dir)

	for name, installed := range result {
		if installed {
			t.Errorf("expected %s to be false with no settings file", name)
		}
	}
}

func TestHooksInstalled_EmptySettings(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{}`), 0o644)

	result := HooksInstalled(dir)
	for name, installed := range result {
		if installed {
			t.Errorf("expected %s to be false with empty settings", name)
		}
	}
}

func TestHooksInstalled_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{invalid`), 0o644)

	result := HooksInstalled(dir)
	for name, installed := range result {
		if installed {
			t.Errorf("expected %s to be false with invalid JSON", name)
		}
	}
}

func TestHooksInstalled_WithSAMEHooks(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	settings := map[string]interface{}{
		"hooks": map[string][]hookEntry{
			"UserPromptSubmit": {
				{Matcher: "", Hooks: []hookAction{{Type: "command", Command: "same hook context-surfacing"}}},
			},
			"Stop": {
				{Matcher: "", Hooks: []hookAction{
					{Type: "command", Command: "same hook decision-extractor"},
					{Type: "command", Command: "same hook handoff-generator"},
					{Type: "command", Command: "same hook feedback-loop"},
				}},
			},
			"SessionStart": {
				{Matcher: "", Hooks: []hookAction{
					{Type: "command", Command: "same version --check"},
					{Type: "command", Command: "same hook staleness-check"},
				}},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)

	result := HooksInstalled(dir)

	expected := map[string]bool{
		"context-surfacing":  true,
		"decision-extractor": true,
		"handoff-generator":  true,
		"feedback-loop":      true,
		"staleness-check":    true,
	}

	for name, want := range expected {
		if got := result[name]; got != want {
			t.Errorf("HooksInstalled[%s] = %v, want %v", name, got, want)
		}
	}
}

func TestHooksInstalled_PartialHooks(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	settings := map[string]interface{}{
		"hooks": map[string][]hookEntry{
			"UserPromptSubmit": {
				{Matcher: "", Hooks: []hookAction{{Type: "command", Command: "same hook context-surfacing"}}},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)

	result := HooksInstalled(dir)
	if !result["context-surfacing"] {
		t.Error("expected context-surfacing to be true")
	}
	if result["decision-extractor"] {
		t.Error("expected decision-extractor to be false")
	}
}

// --- SetupHooks tests ---

func TestSetupHooks_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	err := SetupHooks(dir)
	if err != nil {
		t.Fatalf("SetupHooks: %v", err)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	if _, ok := settings["hooks"]; !ok {
		t.Fatal("expected hooks key in settings")
	}

	var hooks map[string][]hookEntry
	json.Unmarshal(settings["hooks"], &hooks)

	if _, ok := hooks["UserPromptSubmit"]; !ok {
		t.Error("expected UserPromptSubmit hooks")
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Error("expected Stop hooks")
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("expected SessionStart hooks")
	}
}

func TestSetupHooks_PreservesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	existing := map[string]interface{}{
		"customSetting": "preserved",
		"hooks": map[string][]hookEntry{
			"UserPromptSubmit": {
				{Matcher: "", Hooks: []hookAction{{Type: "command", Command: "other-tool run"}}},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)

	err := SetupHooks(dir)
	if err != nil {
		t.Fatalf("SetupHooks: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var result map[string]json.RawMessage
	json.Unmarshal(data, &result)

	// Custom setting should be preserved
	if string(result["customSetting"]) != `"preserved"` {
		t.Errorf("customSetting not preserved, got %s", string(result["customSetting"]))
	}

	// Non-SAME hooks should still be present
	var hooks map[string][]hookEntry
	json.Unmarshal(result["hooks"], &hooks)

	found := false
	for _, entry := range hooks["UserPromptSubmit"] {
		for _, h := range entry.Hooks {
			if h.Command == "other-tool run" {
				found = true
			}
		}
	}
	if !found {
		t.Error("non-SAME hook 'other-tool run' was not preserved")
	}
}

func TestSetupHooks_RejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{bad json`), 0o644)

	err := SetupHooks(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSetupHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()

	// Run twice
	if err := SetupHooks(dir); err != nil {
		t.Fatalf("first SetupHooks: %v", err)
	}
	if err := SetupHooks(dir); err != nil {
		t.Fatalf("second SetupHooks: %v", err)
	}

	// Check that hooks aren't duplicated
	data, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	var settings map[string]json.RawMessage
	json.Unmarshal(data, &settings)

	var hooks map[string][]hookEntry
	json.Unmarshal(settings["hooks"], &hooks)

	// UserPromptSubmit should have exactly 1 entry with 1 hook
	entries := hooks["UserPromptSubmit"]
	count := 0
	for _, e := range entries {
		for _, h := range e.Hooks {
			if isSAMEHook(h.Command) {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("expected 1 SAME hook in UserPromptSubmit after double setup, got %d", count)
	}
}

// --- RemoveHooks tests ---

func TestRemoveHooks_RemovesSAMEHooks(t *testing.T) {
	dir := t.TempDir()

	// First install hooks
	if err := SetupHooks(dir); err != nil {
		t.Fatalf("SetupHooks: %v", err)
	}

	// Then remove
	if err := RemoveHooks(dir); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}

	// Verify all hooks are gone
	result := HooksInstalled(dir)
	for name, installed := range result {
		if installed {
			t.Errorf("expected %s to be removed", name)
		}
	}
}

func TestRemoveHooks_PreservesNonSAMEHooks(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	settings := map[string]interface{}{
		"hooks": map[string][]hookEntry{
			"UserPromptSubmit": {
				{Matcher: "", Hooks: []hookAction{
					{Type: "command", Command: "other-tool run"},
					{Type: "command", Command: "same hook context-surfacing"},
				}},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)

	if err := RemoveHooks(dir); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var result map[string]json.RawMessage
	json.Unmarshal(data, &result)

	var hooks map[string][]hookEntry
	json.Unmarshal(result["hooks"], &hooks)

	found := false
	for _, entry := range hooks["UserPromptSubmit"] {
		for _, h := range entry.Hooks {
			if h.Command == "other-tool run" {
				found = true
			}
		}
	}
	if !found {
		t.Error("non-SAME hook 'other-tool run' was removed")
	}
}

func TestRemoveHooks_NoFile(t *testing.T) {
	dir := t.TempDir()
	err := RemoveHooks(dir)
	if err == nil {
		t.Fatal("expected error when settings file doesn't exist")
	}
}

func TestRemoveHooks_NoHooksKey(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"other": true}`), 0o644)

	err := RemoveHooks(dir)
	if err != nil {
		t.Fatalf("RemoveHooks with no hooks key: %v", err)
	}
}

// --- MCPInstalled tests ---

func TestMCPInstalled_NoFile(t *testing.T) {
	dir := t.TempDir()
	if MCPInstalled(dir) {
		t.Error("expected false with no .mcp.json")
	}
}

func TestMCPInstalled_EmptyConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0o644)
	if MCPInstalled(dir) {
		t.Error("expected false with empty mcpServers")
	}
}

func TestMCPInstalled_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{broken`), 0o644)
	if MCPInstalled(dir) {
		t.Error("expected false with invalid JSON")
	}
}

func TestMCPInstalled_SAMERegistered(t *testing.T) {
	dir := t.TempDir()
	cfg := mcpConfig{
		Servers: map[string]mcpServer{
			"same": {Command: "same", Args: []string{"mcp"}},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644)

	if !MCPInstalled(dir) {
		t.Error("expected true when SAME is registered")
	}
}

func TestMCPInstalled_OtherServersOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := mcpConfig{
		Servers: map[string]mcpServer{
			"other-tool": {Command: "other-tool", Args: []string{"serve"}},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644)

	if MCPInstalled(dir) {
		t.Error("expected false when only other servers are registered")
	}
}

// --- MCPUsesPortablePath tests ---

func TestMCPUsesPortablePath_NoFile(t *testing.T) {
	dir := t.TempDir()
	portable, exists := MCPUsesPortablePath(dir)
	if exists {
		t.Error("expected exists=false when no .mcp.json")
	}
	if portable {
		t.Error("expected portable=false when no .mcp.json")
	}
}

func TestMCPUsesPortablePath_Portable(t *testing.T) {
	dir := t.TempDir()
	cfg := mcpConfig{
		Servers: map[string]mcpServer{
			"same": {Command: "same", Args: []string{"mcp"}},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644)

	portable, exists := MCPUsesPortablePath(dir)
	if !exists {
		t.Error("expected exists=true")
	}
	if !portable {
		t.Error("expected portable=true for bare 'same' command")
	}
}

func TestMCPUsesPortablePath_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	cfg := mcpConfig{
		Servers: map[string]mcpServer{
			"same": {Command: "/usr/local/bin/same", Args: []string{"mcp"}},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644)

	portable, exists := MCPUsesPortablePath(dir)
	if !exists {
		t.Error("expected exists=true")
	}
	if portable {
		t.Error("expected portable=false for absolute path")
	}
}

// --- HooksUsePortablePath tests ---

func TestHooksUsePortablePath_NoFile(t *testing.T) {
	dir := t.TempDir()
	portable, exists := HooksUsePortablePath(dir)
	if exists {
		t.Error("expected exists=false when no settings.json")
	}
	if portable {
		t.Error("expected portable=false when no settings.json")
	}
}

func TestHooksUsePortablePath_Portable(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	settings := map[string]interface{}{
		"hooks": map[string][]hookEntry{
			"UserPromptSubmit": {
				{Matcher: "", Hooks: []hookAction{{Type: "command", Command: "same hook context-surfacing"}}},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)

	portable, exists := HooksUsePortablePath(dir)
	if !exists {
		t.Error("expected exists=true")
	}
	if !portable {
		t.Error("expected portable=true for bare 'same' command")
	}
}

func TestHooksUsePortablePath_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	settings := map[string]interface{}{
		"hooks": map[string][]hookEntry{
			"UserPromptSubmit": {
				{Matcher: "", Hooks: []hookAction{{Type: "command", Command: "/usr/local/bin/same hook context-surfacing"}}},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)

	portable, exists := HooksUsePortablePath(dir)
	if !exists {
		t.Error("expected exists=true")
	}
	if portable {
		t.Error("expected portable=false for absolute path")
	}
}

// --- SetupMCP tests ---

func TestSetupMCP_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	err := SetupMCP(dir)
	if err != nil {
		t.Fatalf("SetupMCP: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}

	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}

	server, ok := cfg.Servers["same"]
	if !ok {
		t.Fatal("expected 'same' server in .mcp.json")
	}
	if len(server.Args) != 1 || server.Args[0] != "mcp" {
		t.Errorf("expected args [mcp], got %v", server.Args)
	}
	if server.Env["VAULT_PATH"] != dir {
		t.Errorf("expected VAULT_PATH=%s, got %s", dir, server.Env["VAULT_PATH"])
	}
}

func TestSetupMCP_PreservesExistingServers(t *testing.T) {
	dir := t.TempDir()
	existing := mcpConfig{
		Servers: map[string]mcpServer{
			"other-tool": {Command: "other-tool", Args: []string{"serve"}},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644)

	if err := SetupMCP(dir); err != nil {
		t.Fatalf("SetupMCP: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(dir, ".mcp.json"))
	var cfg mcpConfig
	json.Unmarshal(data, &cfg)

	if _, ok := cfg.Servers["other-tool"]; !ok {
		t.Error("existing server 'other-tool' was not preserved")
	}
	if _, ok := cfg.Servers["same"]; !ok {
		t.Error("'same' server was not added")
	}
}

func TestSetupMCP_RejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{bad json`), 0o644)

	err := SetupMCP(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- RemoveMCP tests ---

func TestRemoveMCP_RemovesSAME(t *testing.T) {
	dir := t.TempDir()

	// Setup first
	if err := SetupMCP(dir); err != nil {
		t.Fatalf("SetupMCP: %v", err)
	}

	// Remove
	if err := RemoveMCP(dir); err != nil {
		t.Fatalf("RemoveMCP: %v", err)
	}

	if MCPInstalled(dir) {
		t.Error("expected SAME to be removed from .mcp.json")
	}
}

func TestRemoveMCP_PreservesOtherServers(t *testing.T) {
	dir := t.TempDir()
	cfg := mcpConfig{
		Servers: map[string]mcpServer{
			"same":       {Command: "same", Args: []string{"mcp"}},
			"other-tool": {Command: "other-tool", Args: []string{"serve"}},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644)

	if err := RemoveMCP(dir); err != nil {
		t.Fatalf("RemoveMCP: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(dir, ".mcp.json"))
	var result mcpConfig
	json.Unmarshal(data, &result)

	if _, ok := result.Servers["same"]; ok {
		t.Error("'same' server should have been removed")
	}
	if _, ok := result.Servers["other-tool"]; !ok {
		t.Error("'other-tool' server should have been preserved")
	}
}

func TestRemoveMCP_NotRegistered(t *testing.T) {
	dir := t.TempDir()
	cfg := mcpConfig{
		Servers: map[string]mcpServer{
			"other-tool": {Command: "other-tool", Args: []string{"serve"}},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644)

	err := RemoveMCP(dir)
	if err != nil {
		t.Fatalf("RemoveMCP when not registered: %v", err)
	}
}

func TestRemoveMCP_NoFile(t *testing.T) {
	dir := t.TempDir()
	err := RemoveMCP(dir)
	if err == nil {
		t.Fatal("expected error when .mcp.json doesn't exist")
	}
}

// --- buildHooks tests ---

func TestBuildHooks_SubstitutesBinaryPath(t *testing.T) {
	hooks := buildHooks("/usr/local/bin/same")

	for event, entries := range hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				if h.Command == "" {
					t.Errorf("empty command in event %s", event)
				}
				if h.Command[0] == '%' {
					t.Errorf("unsubstituted format string in event %s: %s", event, h.Command)
				}
			}
		}
	}

	// Check specific command
	entries := hooks["UserPromptSubmit"]
	if len(entries) == 0 {
		t.Fatal("expected UserPromptSubmit entries")
	}
	if entries[0].Hooks[0].Command != "/usr/local/bin/same hook context-surfacing" {
		t.Errorf("unexpected command: %s", entries[0].Hooks[0].Command)
	}
}

// --- filterNonSAMEHooks tests ---

func TestFilterNonSAMEHooks_RemovesSAME(t *testing.T) {
	entries := []hookEntry{
		{Matcher: "", Hooks: []hookAction{
			{Type: "command", Command: "same hook context-surfacing"},
		}},
		{Matcher: "", Hooks: []hookAction{
			{Type: "command", Command: "other-tool run"},
		}},
	}

	filtered := filterNonSAMEHooks(entries, "same")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 entry after filter, got %d", len(filtered))
	}
	if filtered[0].Hooks[0].Command != "other-tool run" {
		t.Errorf("expected other-tool hook, got %s", filtered[0].Hooks[0].Command)
	}
}

func TestFilterNonSAMEHooks_EmptyInput(t *testing.T) {
	filtered := filterNonSAMEHooks(nil, "same")
	if len(filtered) != 0 {
		t.Errorf("expected empty result, got %d entries", len(filtered))
	}
}

// --- isSAMEHook tests ---

func TestIsSAMEHook(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"same hook context-surfacing", true},
		{"same hook decision-extractor", true},
		{"/usr/local/bin/same hook feedback-loop", true},
		{"same version --check", true},
		{"other-tool run", false},
		{"", false},
		{"samehook", false},
		// Windows quoted paths
		{`"C:\Users\User Name\AppData\Local\Programs\SAME\same.exe" hook context-surfacing`, true},
		{`"C:\Users\Jane Doe\AppData\Local\Programs\SAME\same.exe" hook staleness-check`, true},
		{`"C:\Users\User\same.exe" version --check`, true},
		// Case insensitive
		{"SAME hook context-surfacing", true},
		{"Same Hook Decision-Extractor", true},
	}

	for _, tt := range tests {
		if got := isSAMEHook(tt.command); got != tt.want {
			t.Errorf("isSAMEHook(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

// --- containsSAMEHook tests ---

func TestContainsSAMEHook(t *testing.T) {
	tests := []struct {
		command  string
		hookName string
		want     bool
	}{
		{"same hook context-surfacing", "context-surfacing", true},
		{"/usr/local/bin/same hook context-surfacing", "context-surfacing", true},
		{`"C:\Users\User Name\same.exe" hook context-surfacing`, "context-surfacing", true},
		{"same version --check", "version --check", true},
		{"other-tool run", "context-surfacing", false},
		{"", "hook", false},
		// Case insensitive
		{"SAME hook Context-Surfacing", "context-surfacing", true},
	}

	for _, tt := range tests {
		if got := containsSAMEHook(tt.command, tt.hookName); got != tt.want {
			t.Errorf("containsSAMEHook(%q, %q) = %v, want %v", tt.command, tt.hookName, got, tt.want)
		}
	}
}

// --- isCloudSyncedPath tests ---

func TestIsCloudSyncedPath(t *testing.T) {
	tests := []struct {
		path     string
		synced   bool
		provider string
	}{
		{"/home/user/Dropbox/notes", true, "Dropbox"},
		{"/home/user/OneDrive/docs", true, "OneDrive"},
		{"/home/user/Google Drive/notes", true, "Google Drive"},
		{"/home/user/notes", false, ""},
		{"/tmp/test", false, ""},
	}

	for _, tt := range tests {
		synced, provider := isCloudSyncedPath(tt.path)
		if synced != tt.synced {
			t.Errorf("isCloudSyncedPath(%q) synced = %v, want %v", tt.path, synced, tt.synced)
		}
		if provider != tt.provider {
			t.Errorf("isCloudSyncedPath(%q) provider = %q, want %q", tt.path, provider, tt.provider)
		}
	}
}

// --- createSeedStructure tests ---

func TestCreateSeedStructure_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	createSeedStructure(dir)

	for _, name := range []string{"sessions", "_PRIVATE"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("expected %s to be created: %v", name, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", name)
		}
	}
}

func TestCreateSeedStructure_SkipsExisting(t *testing.T) {
	dir := t.TempDir()

	// Pre-create sessions directory with a file inside
	sessDir := filepath.Join(dir, "sessions")
	os.MkdirAll(sessDir, 0o755)
	os.WriteFile(filepath.Join(sessDir, "test.md"), []byte("existing"), 0o644)

	createSeedStructure(dir)

	// Verify existing content is preserved
	data, err := os.ReadFile(filepath.Join(sessDir, "test.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Error("existing file was overwritten")
	}
}

// --- copyWelcomeNotes tests ---

func TestCopyWelcomeNotes_CopiesFiles(t *testing.T) {
	dir := t.TempDir()
	copyWelcomeNotes(dir)

	destDir := filepath.Join(dir, "welcome")
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatalf("read welcome dir: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("expected welcome notes to be copied")
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(destDir, e.Name()))
		if err != nil {
			t.Errorf("read %s: %v", e.Name(), err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("welcome note %s is empty", e.Name())
		}
	}
}

func TestCopyWelcomeNotes_SkipsIfExists(t *testing.T) {
	dir := t.TempDir()

	// Pre-create welcome directory with a custom file
	destDir := filepath.Join(dir, "welcome")
	os.MkdirAll(destDir, 0o755)
	os.WriteFile(filepath.Join(destDir, "custom.md"), []byte("custom"), 0o644)

	copyWelcomeNotes(dir)

	// Verify custom file is preserved and no new files were added
	data, err := os.ReadFile(filepath.Join(destDir, "custom.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom" {
		t.Error("custom file was overwritten")
	}
}

func TestCopyWelcomeNotes_SkipsIfLegacyExists(t *testing.T) {
	dir := t.TempDir()

	// Pre-create legacy .same/welcome/ directory
	legacyDir := filepath.Join(dir, ".same", "welcome")
	os.MkdirAll(legacyDir, 0o755)
	os.WriteFile(filepath.Join(legacyDir, "old.md"), []byte("legacy"), 0o644)

	copyWelcomeNotes(dir)

	// Verify new welcome/ was NOT created
	if _, err := os.Stat(filepath.Join(dir, "welcome")); err == nil {
		t.Error("welcome/ should not be created when legacy .same/welcome/ exists")
	}
}

func TestHooksInstalled_WindowsQuotedPath(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	settings := map[string]interface{}{
		"hooks": map[string][]hookEntry{
			"UserPromptSubmit": {
				{Matcher: "", Hooks: []hookAction{{Type: "command", Command: `"C:\Users\Jane Doe\AppData\Local\Programs\SAME\same.exe" hook context-surfacing`}}},
			},
			"SessionStart": {
				{Matcher: "", Hooks: []hookAction{
					{Type: "command", Command: `"C:\Users\Jane Doe\AppData\Local\Programs\SAME\same.exe" version --check`},
					{Type: "command", Command: `"C:\Users\Jane Doe\AppData\Local\Programs\SAME\same.exe" hook staleness-check`},
				}},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)

	result := HooksInstalled(dir)

	if !result["context-surfacing"] {
		t.Error("expected context-surfacing to be detected with Windows quoted path")
	}
	if !result["staleness-check"] {
		t.Error("expected staleness-check to be detected with Windows quoted path")
	}
}

// --- detectProjectDocs tests ---

func TestDetectProjectDocs_FindsRootFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some doc files
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# README"), 0o644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Rules"), 0o644)

	found := detectProjectDocs(dir)

	has := make(map[string]bool)
	for _, f := range found {
		has[f] = true
	}

	if !has["README.md"] {
		t.Error("expected README.md to be found")
	}
	if !has["CLAUDE.md"] {
		t.Error("expected CLAUDE.md to be found")
	}
}

func TestDetectProjectDocs_FindsDocDirs(t *testing.T) {
	dir := t.TempDir()

	// Create docs/ directory with files
	docsDir := filepath.Join(dir, "docs")
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "guide.md"), []byte("# Guide"), 0o644)
	os.WriteFile(filepath.Join(docsDir, "setup.md"), []byte("# Setup"), 0o644)
	os.WriteFile(filepath.Join(docsDir, "image.png"), []byte("not-md"), 0o644)

	found := detectProjectDocs(dir)

	has := make(map[string]bool)
	for _, f := range found {
		has[f] = true
	}

	if !has[filepath.Join("docs", "guide.md")] {
		t.Error("expected docs/guide.md to be found")
	}
	if !has[filepath.Join("docs", "setup.md")] {
		t.Error("expected docs/setup.md to be found")
	}
	if has[filepath.Join("docs", "image.png")] {
		t.Error("non-md file should not be found")
	}
}

func TestDetectProjectDocs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	found := detectProjectDocs(dir)
	if len(found) != 0 {
		t.Errorf("expected no docs in empty dir, got %v", found)
	}
}

// --- handleGitignore tests ---

func TestHandleGitignore_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	handleGitignore(dir, true) // autoAccept=true to skip prompt

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, ".same/data/") {
		t.Error("expected .same/data/ in .gitignore")
	}
	if !strings.Contains(content, "_PRIVATE/") {
		t.Error("expected _PRIVATE/ in .gitignore")
	}
}

func TestHandleGitignore_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644)

	handleGitignore(dir, true)

	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)

	if !strings.Contains(content, "node_modules/") {
		t.Error("existing content should be preserved")
	}
	if !strings.Contains(content, ".same/data/") {
		t.Error("SAME rules should be appended")
	}
}

func TestHandleGitignore_SkipsIfAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	original := "node_modules/\n.same/data/\n"
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(original), 0o644)

	handleGitignore(dir, true)

	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(data) != original {
		t.Error("gitignore should not be modified when SAME rules already present")
	}
}
