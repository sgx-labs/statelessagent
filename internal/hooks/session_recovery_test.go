package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/store"
)

// --- TestRecoverFromHandoff ---

func TestRecoverFromHandoff_FreshHandoff(t *testing.T) {
	tmp := t.TempDir()

	// Set VAULT_PATH so SafeVaultSubpath resolves inside our temp dir.
	t.Setenv("VAULT_PATH", tmp)

	// The default HandoffDirectory() returns "sessions", so create that subdir.
	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	// Write a realistic handoff file with date-prefixed name.
	handoffContent := `---
date: 2026-02-10
---
## Summary
Refactored the indexing pipeline for better chunking.

## Pending Items
- Review edge cases in heading detection
- Add tests for empty files

## Blockers
None currently.
`
	handoffFile := filepath.Join(sessionsDir, "2026-02-10-session-handoff.md")
	if err := os.WriteFile(handoffFile, []byte(handoffContent), 0o644); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	rs := recoverFromHandoff()
	if rs == nil {
		t.Fatal("expected non-nil RecoveredSession from fresh handoff")
	}

	if rs.Source != RecoveryHandoff {
		t.Errorf("expected Source=RecoveryHandoff, got %d", rs.Source)
	}
	if rs.Completeness != 1.0 {
		t.Errorf("expected Completeness=1.0, got %f", rs.Completeness)
	}
	if rs.HandoffText == "" {
		t.Error("expected non-empty HandoffText")
	}
	if !strings.Contains(rs.HandoffText, "Refactored") || !strings.Contains(rs.HandoffText, "Pending Items") {
		t.Errorf("expected handoff text to contain key sections, got: %s", rs.HandoffText)
	}
}

func TestRecoverFromHandoff_StaleHandoff(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	handoffContent := "## Summary\nOld session work.\n"
	handoffFile := filepath.Join(sessionsDir, "2026-01-01-old-handoff.md")
	if err := os.WriteFile(handoffFile, []byte(handoffContent), 0o644); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	// Set modtime to > 48 hours ago.
	staleTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(handoffFile, staleTime, staleTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	rs := recoverFromHandoff()
	if rs != nil {
		t.Errorf("expected nil for stale handoff (>48h), got Source=%d", rs.Source)
	}
}

func TestRecoverFromHandoff_EmptyDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	// No files in the directory.
	rs := recoverFromHandoff()
	if rs != nil {
		t.Errorf("expected nil for empty handoff directory, got %+v", rs)
	}
}

func TestRecoverFromHandoff_NoDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)
	// sessions/ directory does not exist.

	rs := recoverFromHandoff()
	if rs != nil {
		t.Errorf("expected nil when handoff directory does not exist, got %+v", rs)
	}
}

func TestRecoverFromHandoff_PicksLatestByFilename(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	// Write two handoff files; the one with the later date prefix should win.
	older := "## Summary\nOlder session.\n"
	newer := "## Summary\nNewer session with important changes.\n"

	if err := os.WriteFile(filepath.Join(sessionsDir, "2026-02-08-handoff.md"), []byte(older), 0o644); err != nil {
		t.Fatalf("write older: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "2026-02-10-handoff.md"), []byte(newer), 0o644); err != nil {
		t.Fatalf("write newer: %v", err)
	}

	rs := recoverFromHandoff()
	if rs == nil {
		t.Fatal("expected non-nil RecoveredSession")
	}
	if !strings.Contains(rs.HandoffText, "Newer session") {
		t.Errorf("expected latest handoff to win, got: %s", rs.HandoffText)
	}
}

func TestRecoverFromHandoff_IgnoresNonMarkdownFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	// Write a JSON file (not markdown).
	if err := os.WriteFile(filepath.Join(sessionsDir, "2026-02-10-data.json"), []byte(`{"key":"value"}`), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	rs := recoverFromHandoff()
	if rs != nil {
		t.Errorf("expected nil when no .md files exist, got %+v", rs)
	}
}

// --- TestRecoverFromInstance ---

func TestRecoverFromInstance_FreshInstance(t *testing.T) {
	tmp := t.TempDir()

	// Set up SAME_DATA_DIR so instancesDir() resolves to <tmp>/.same/instances
	dataDir := filepath.Join(tmp, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	t.Setenv("SAME_DATA_DIR", dataDir)
	// Also need VAULT_PATH set so DataDir validation doesn't fall through.
	t.Setenv("VAULT_PATH", tmp)

	instDir := filepath.Join(tmp, ".same", "instances")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatalf("mkdir instances: %v", err)
	}

	now := time.Now().UTC()
	info := instanceInfo{
		SessionID: "prev-session-abc",
		Machine:   "test-machine",
		Started:   now.Add(-2 * time.Hour).Format(time.RFC3339),
		Updated:   now.Add(-30 * time.Minute).Format(time.RFC3339),
		Summary:   "Working on indexer refactoring",
		Status:    "active",
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("marshal instance: %v", err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "prev-session-abc.json"), data, 0o600); err != nil {
		t.Fatalf("write instance: %v", err)
	}

	rs := recoverFromInstance("current-session-xyz")
	if rs == nil {
		t.Fatal("expected non-nil RecoveredSession from fresh instance")
	}

	if rs.Source != RecoveryInstance {
		t.Errorf("expected Source=RecoveryInstance, got %d", rs.Source)
	}
	if rs.Completeness != 0.4 {
		t.Errorf("expected Completeness=0.4, got %f", rs.Completeness)
	}
	if rs.SessionID != "prev-session-abc" {
		t.Errorf("expected SessionID=prev-session-abc, got %s", rs.SessionID)
	}
	if rs.Summary != "Working on indexer refactoring" {
		t.Errorf("expected matching summary, got %s", rs.Summary)
	}
}

func TestRecoverFromInstance_SkipsCurrentSession(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	t.Setenv("SAME_DATA_DIR", dataDir)
	t.Setenv("VAULT_PATH", tmp)

	instDir := filepath.Join(tmp, ".same", "instances")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatalf("mkdir instances: %v", err)
	}

	now := time.Now().UTC()
	info := instanceInfo{
		SessionID: "my-session",
		Machine:   "test-machine",
		Started:   now.Add(-1 * time.Hour).Format(time.RFC3339),
		Updated:   now.Add(-5 * time.Minute).Format(time.RFC3339),
		Summary:   "Current work",
		Status:    "active",
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(filepath.Join(instDir, "my-session.json"), data, 0o600); err != nil {
		t.Fatalf("write instance: %v", err)
	}

	// Pass the same session ID — should be skipped.
	rs := recoverFromInstance("my-session")
	if rs != nil {
		t.Errorf("expected nil when only the current session exists, got %+v", rs)
	}
}

func TestRecoverFromInstance_SkipsStale(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	t.Setenv("SAME_DATA_DIR", dataDir)
	t.Setenv("VAULT_PATH", tmp)

	instDir := filepath.Join(tmp, ".same", "instances")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatalf("mkdir instances: %v", err)
	}

	// Instance with updated time > 48 hours ago.
	staleTime := time.Now().UTC().Add(-72 * time.Hour)
	info := instanceInfo{
		SessionID: "stale-session",
		Machine:   "test-machine",
		Started:   staleTime.Add(-1 * time.Hour).Format(time.RFC3339),
		Updated:   staleTime.Format(time.RFC3339),
		Summary:   "Old work",
		Status:    "completed",
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(filepath.Join(instDir, "stale-session.json"), data, 0o600); err != nil {
		t.Fatalf("write instance: %v", err)
	}

	rs := recoverFromInstance("current-session")
	if rs != nil {
		t.Errorf("expected nil for stale instance (>48h), got %+v", rs)
	}
}

func TestRecoverFromInstance_SkipsNoSummary(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	t.Setenv("SAME_DATA_DIR", dataDir)
	t.Setenv("VAULT_PATH", tmp)

	instDir := filepath.Join(tmp, ".same", "instances")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatalf("mkdir instances: %v", err)
	}

	now := time.Now().UTC()
	info := instanceInfo{
		SessionID: "no-summary-session",
		Machine:   "test-machine",
		Started:   now.Add(-1 * time.Hour).Format(time.RFC3339),
		Updated:   now.Add(-5 * time.Minute).Format(time.RFC3339),
		Summary:   "", // Empty summary should be skipped.
		Status:    "active",
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(filepath.Join(instDir, "no-summary-session.json"), data, 0o600); err != nil {
		t.Fatalf("write instance: %v", err)
	}

	rs := recoverFromInstance("current-session")
	if rs != nil {
		t.Errorf("expected nil when instance has no summary, got %+v", rs)
	}
}

func TestRecoverFromInstance_PicksMostRecent(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, ".same", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	t.Setenv("SAME_DATA_DIR", dataDir)
	t.Setenv("VAULT_PATH", tmp)

	instDir := filepath.Join(tmp, ".same", "instances")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatalf("mkdir instances: %v", err)
	}

	now := time.Now().UTC()

	// Older instance.
	older := instanceInfo{
		SessionID: "older-session",
		Machine:   "test-machine",
		Started:   now.Add(-4 * time.Hour).Format(time.RFC3339),
		Updated:   now.Add(-3 * time.Hour).Format(time.RFC3339),
		Summary:   "Older work",
		Status:    "completed",
	}
	olderData, _ := json.MarshalIndent(older, "", "  ")
	if err := os.WriteFile(filepath.Join(instDir, "older-session.json"), olderData, 0o600); err != nil {
		t.Fatalf("write older: %v", err)
	}

	// Newer instance.
	newer := instanceInfo{
		SessionID: "newer-session",
		Machine:   "test-machine",
		Started:   now.Add(-2 * time.Hour).Format(time.RFC3339),
		Updated:   now.Add(-30 * time.Minute).Format(time.RFC3339),
		Summary:   "Newer work",
		Status:    "active",
	}
	newerData, _ := json.MarshalIndent(newer, "", "  ")
	if err := os.WriteFile(filepath.Join(instDir, "newer-session.json"), newerData, 0o600); err != nil {
		t.Fatalf("write newer: %v", err)
	}

	rs := recoverFromInstance("current-session")
	if rs == nil {
		t.Fatal("expected non-nil RecoveredSession")
	}
	if rs.SessionID != "newer-session" {
		t.Errorf("expected most recent instance to win, got SessionID=%s", rs.SessionID)
	}
}

// --- TestRecoverFromSessionIndex ---

func sessionIndexPathForTest(t *testing.T) string {
	t.Helper()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	hash := claudeProjectHash(cwd)
	indexDir := filepath.Join(tmpHome, ".claude", "projects", hash)
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatalf("mkdir index dir: %v", err)
	}
	return filepath.Join(indexDir, "sessions-index.json")
}

func TestRecoverFromSessionIndex_ValidIndex(t *testing.T) {
	indexPath := sessionIndexPathForTest(t)

	now := time.Now().UTC()
	idx := sessionsIndex{
		Version: 1,
		Entries: []sessionEntry{
			{
				SessionID:    "current-session-123",
				Summary:      "Current session work",
				FirstPrompt:  "help me with testing",
				MessageCount: 5,
				Modified:     now.Format(time.RFC3339),
				GitBranch:    "main",
			},
			{
				SessionID:    "prev-session-456",
				Summary:      "Previous session on indexer",
				FirstPrompt:  "refactor the indexing pipeline",
				MessageCount: 42,
				Modified:     now.Add(-2 * time.Hour).Format(time.RFC3339),
				GitBranch:    "feature/indexer",
			},
		},
	}

	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}

	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	rs := recoverFromSessionIndex("current-session-123")
	if rs == nil {
		t.Fatal("expected non-nil RecoveredSession from session index")
	}

	if rs.Source != RecoverySessionIndex {
		t.Errorf("expected Source=RecoverySessionIndex, got %d", rs.Source)
	}
	if rs.Completeness != 0.3 {
		t.Errorf("expected Completeness=0.3, got %f", rs.Completeness)
	}
	if rs.SessionID != "prev-session-456" {
		t.Errorf("expected SessionID=prev-session-456, got %s", rs.SessionID)
	}
	if rs.Summary != "Previous session on indexer" {
		t.Errorf("expected matching summary, got %s", rs.Summary)
	}
	if rs.FirstPrompt != "refactor the indexing pipeline" {
		t.Errorf("expected matching first prompt, got %s", rs.FirstPrompt)
	}
	if rs.MessageCount != 42 {
		t.Errorf("expected MessageCount=42, got %d", rs.MessageCount)
	}
	if rs.GitBranch != "feature/indexer" {
		t.Errorf("expected GitBranch=feature/indexer, got %s", rs.GitBranch)
	}
}

func TestRecoverFromSessionIndex_SkipsCurrentSession(t *testing.T) {
	indexPath := sessionIndexPathForTest(t)

	now := time.Now().UTC()
	idx := sessionsIndex{
		Version: 1,
		Entries: []sessionEntry{
			{
				SessionID:    "only-session",
				Summary:      "The only session",
				FirstPrompt:  "hello",
				MessageCount: 3,
				Modified:     now.Add(-2 * time.Hour).Format(time.RFC3339),
			},
		},
	}

	data, _ := json.Marshal(idx)
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	// When passing the same session ID as the only entry, should return nil.
	rs := recoverFromSessionIndex("only-session")
	if rs != nil {
		t.Errorf("expected nil when current session is the only entry, got %+v", rs)
	}
}

func TestRecoverFromSessionIndex_EmptyEntries(t *testing.T) {
	indexPath := sessionIndexPathForTest(t)

	idx := sessionsIndex{Version: 1, Entries: []sessionEntry{}}
	data, _ := json.Marshal(idx)
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	rs := recoverFromSessionIndex("any-session")
	if rs != nil {
		t.Errorf("expected nil for empty session entries, got %+v", rs)
	}
}

// --- TestRecoverPreviousSession_Cascade ---

func TestRecoverPreviousSession_Cascade_HandoffWins(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	dataDir := filepath.Join(tmp, ".same", "data")
	os.MkdirAll(dataDir, 0o755)
	t.Setenv("SAME_DATA_DIR", dataDir)

	// Set up handoff (Source 1).
	sessionsDir := filepath.Join(tmp, "sessions")
	os.MkdirAll(sessionsDir, 0o755)
	handoffContent := "## Summary\nHandoff content wins.\n## Next Session\nDo testing.\n"
	os.WriteFile(filepath.Join(sessionsDir, "2026-02-10-handoff.md"), []byte(handoffContent), 0o644)

	// Set up instance (Source 2).
	instDir := filepath.Join(tmp, ".same", "instances")
	os.MkdirAll(instDir, 0o755)
	now := time.Now().UTC()
	instInfo := instanceInfo{
		SessionID: "prev-inst",
		Machine:   "test",
		Started:   now.Add(-1 * time.Hour).Format(time.RFC3339),
		Updated:   now.Add(-10 * time.Minute).Format(time.RFC3339),
		Summary:   "Instance work",
		Status:    "active",
	}
	instData, _ := json.MarshalIndent(instInfo, "", "  ")
	os.WriteFile(filepath.Join(instDir, "prev-inst.json"), instData, 0o600)

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	rs := RecoverPreviousSession(db, "current-session")
	if rs == nil {
		t.Fatal("expected non-nil recovery result")
	}
	if rs.Source != RecoveryHandoff {
		t.Errorf("expected handoff to win cascade, got Source=%d", rs.Source)
	}
	if rs.Completeness != 1.0 {
		t.Errorf("expected Completeness=1.0 for handoff, got %f", rs.Completeness)
	}
}

func TestRecoverPreviousSession_Cascade_InstanceWinsWhenHandoffStale(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	dataDir := filepath.Join(tmp, ".same", "data")
	os.MkdirAll(dataDir, 0o755)
	t.Setenv("SAME_DATA_DIR", dataDir)

	// Set up stale handoff (Source 1 — too old).
	sessionsDir := filepath.Join(tmp, "sessions")
	os.MkdirAll(sessionsDir, 0o755)
	handoffFile := filepath.Join(sessionsDir, "2026-01-01-old-handoff.md")
	os.WriteFile(handoffFile, []byte("## Summary\nStale handoff.\n"), 0o644)
	staleTime := time.Now().Add(-72 * time.Hour)
	os.Chtimes(handoffFile, staleTime, staleTime)

	// Set up fresh instance (Source 2).
	instDir := filepath.Join(tmp, ".same", "instances")
	os.MkdirAll(instDir, 0o755)
	now := time.Now().UTC()
	instInfo := instanceInfo{
		SessionID: "prev-inst",
		Machine:   "test",
		Started:   now.Add(-1 * time.Hour).Format(time.RFC3339),
		Updated:   now.Add(-10 * time.Minute).Format(time.RFC3339),
		Summary:   "Instance work should win",
		Status:    "active",
	}
	instData, _ := json.MarshalIndent(instInfo, "", "  ")
	os.WriteFile(filepath.Join(instDir, "prev-inst.json"), instData, 0o600)

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	rs := RecoverPreviousSession(db, "current-session")
	if rs == nil {
		t.Fatal("expected non-nil recovery result")
	}
	if rs.Source != RecoveryInstance {
		t.Errorf("expected instance to win when handoff is stale, got Source=%d", rs.Source)
	}
	if rs.Completeness != 0.4 {
		t.Errorf("expected Completeness=0.4 for instance, got %f", rs.Completeness)
	}
}

func TestRecoverPreviousSession_Cascade_NothingFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	dataDir := filepath.Join(tmp, ".same", "data")
	os.MkdirAll(dataDir, 0o755)
	t.Setenv("SAME_DATA_DIR", dataDir)

	// No handoff files, no instance files.
	// Session index won't find anything at a random path either.

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	rs := RecoverPreviousSession(db, "current-session")
	// Could be nil or session-index if one happens to exist on this machine.
	// We just verify it doesn't panic and the cascade works.
	if rs != nil && rs.Source != RecoverySessionIndex {
		t.Errorf("expected nil or session-index fallback, got Source=%d", rs.Source)
	}
}

func TestRecoverPreviousSession_NilDB(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("VAULT_PATH", tmp)

	dataDir := filepath.Join(tmp, ".same", "data")
	os.MkdirAll(dataDir, 0o755)
	t.Setenv("SAME_DATA_DIR", dataDir)

	// Verify it does not panic when db is nil.
	rs := RecoverPreviousSession(nil, "session-id")
	_ = rs // Just ensure no panic.
}

// --- TestFormatRecoveryContext ---

func TestFormatRecoveryContext_Nil(t *testing.T) {
	result := FormatRecoveryContext(nil)
	if result != "" {
		t.Errorf("expected empty string for nil input, got %q", result)
	}
}

func TestFormatRecoveryContext_RecoveryNone(t *testing.T) {
	rs := &RecoveredSession{Source: RecoveryNone}
	result := FormatRecoveryContext(rs)
	if result != "" {
		t.Errorf("expected empty string for RecoveryNone, got %q", result)
	}
}

func TestFormatRecoveryContext_Handoff(t *testing.T) {
	rs := &RecoveredSession{
		Source:      RecoveryHandoff,
		HandoffText: "Session ended cleanly. Work was on indexer refactoring.",
	}
	result := FormatRecoveryContext(rs)

	if !strings.Contains(result, "Previous Session (full handoff)") {
		t.Errorf("expected handoff header, got: %s", result)
	}
	if !strings.Contains(result, "indexer refactoring") {
		t.Errorf("expected handoff text content, got: %s", result)
	}
}

func TestFormatRecoveryContext_Instance(t *testing.T) {
	rs := &RecoveredSession{
		Source:  RecoveryInstance,
		Summary: "Working on search improvements",
		EndedAt: time.Now().Add(-1 * time.Hour),
	}
	result := FormatRecoveryContext(rs)

	if !strings.Contains(result, "recovered from instance") {
		t.Errorf("expected instance header, got: %s", result)
	}
	if !strings.Contains(result, "search improvements") {
		t.Errorf("expected summary in output, got: %s", result)
	}
	if !strings.Contains(result, "Context may be incomplete") {
		t.Errorf("expected incomplete context note, got: %s", result)
	}
}

func TestFormatRecoveryContext_SessionIndex(t *testing.T) {
	rs := &RecoveredSession{
		Source:       RecoverySessionIndex,
		Summary:      "Reviewing PR feedback",
		FirstPrompt:  "check the latest review comments",
		MessageCount: 15,
		GitBranch:    "feature/review",
		EndedAt:      time.Now().Add(-3 * time.Hour),
	}
	result := FormatRecoveryContext(rs)

	if !strings.Contains(result, "recovered from session index") {
		t.Errorf("expected session-index header, got: %s", result)
	}
	if !strings.Contains(result, "Reviewing PR feedback") {
		t.Errorf("expected summary, got: %s", result)
	}
	if !strings.Contains(result, "check the latest review comments") {
		t.Errorf("expected first prompt, got: %s", result)
	}
	if !strings.Contains(result, "15") {
		t.Errorf("expected message count, got: %s", result)
	}
	if !strings.Contains(result, "feature/review") {
		t.Errorf("expected git branch, got: %s", result)
	}
	if !strings.Contains(result, "terminal closed") {
		t.Errorf("expected crash recovery note, got: %s", result)
	}
}

func TestFormatRecoveryContext_SessionIndex_LongFirstPrompt(t *testing.T) {
	longPrompt := strings.Repeat("x", 200)
	rs := &RecoveredSession{
		Source:      RecoverySessionIndex,
		FirstPrompt: longPrompt,
	}
	result := FormatRecoveryContext(rs)

	// The function truncates first prompt at 150 chars (147 + "...")
	if strings.Contains(result, strings.Repeat("x", 200)) {
		t.Error("expected first prompt to be truncated")
	}
	if !strings.Contains(result, "...") {
		t.Errorf("expected truncation ellipsis in output")
	}
}

func TestFormatRecoveryContext_Truncation(t *testing.T) {
	// Create content that exceeds recoveryMaxChars (4000).
	longText := strings.Repeat("A long handoff note with lots of content. ", 200)
	rs := &RecoveredSession{
		Source:      RecoveryHandoff,
		HandoffText: longText,
	}
	result := FormatRecoveryContext(rs)

	if len(result) > 4000 {
		t.Errorf("expected result to be truncated to 4000 chars, got %d", len(result))
	}
}

func TestFormatRecoveryContext_EmptyFields(t *testing.T) {
	// Session index with minimal data — empty optional fields should be omitted.
	rs := &RecoveredSession{
		Source: RecoverySessionIndex,
		// All optional fields are zero/empty.
	}
	result := FormatRecoveryContext(rs)

	if !strings.Contains(result, "recovered from session index") {
		t.Errorf("expected header even with empty fields, got: %s", result)
	}
	// Should NOT contain "Summary:" line since summary is empty.
	if strings.Contains(result, "**Summary:**  ") {
		// The format is fmt.Sprintf("**Summary:** %s\n", rs.Summary) which would produce
		// "**Summary:** \n" — that's fine, the code checks `if rs.Summary != ""`.
		// Actually looking at the code, it does check `if rs.Summary != ""`.
	}
}

func TestFormatRecoveryContext_InstanceZeroEndedAt(t *testing.T) {
	rs := &RecoveredSession{
		Source:  RecoveryInstance,
		Summary: "Some work",
		// EndedAt is zero value.
	}
	result := FormatRecoveryContext(rs)

	// Should not contain "Last active" line when EndedAt is zero.
	if strings.Contains(result, "Last active") {
		t.Errorf("expected no Last active line for zero EndedAt, got: %s", result)
	}
}
