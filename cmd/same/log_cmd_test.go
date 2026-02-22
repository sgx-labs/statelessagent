package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestLogCmd_EmptyLog(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runLog(10, false)
	})
	if runErr != nil {
		t.Fatalf("runLog: %v", runErr)
	}
	if !strings.Contains(out, "No recent activity") {
		t.Fatalf("expected empty activity message, got: %q", out)
	}
}

func TestLogCmd_ShowsHookActivity(t *testing.T) {
	_, db := setupCommandTestVault(t)

	ts1 := time.Date(2026, 2, 21, 14, 30, 0, 0, time.UTC).Unix()
	ts2 := time.Date(2026, 2, 21, 14, 29, 0, 0, time.UTC).Unix()

	if err := db.InsertHookActivity(&store.HookActivityRecord{
		TimestampUnix:   ts1,
		HookSessionID:   "sess-1",
		HookName:        "context-surfacing",
		Status:          "injected",
		SurfacedNotes:   3,
		EstimatedTokens: 240,
		NotePaths:       []string{"notes/a.md", "notes/b.md", "notes/c.md"},
	}); err != nil {
		t.Fatalf("InsertHookActivity injected: %v", err)
	}
	if err := db.InsertHookActivity(&store.HookActivityRecord{
		TimestampUnix: ts2,
		HookSessionID: "sess-1",
		HookName:      "decision-extractor",
		Status:        "skipped",
		Detail:        "no new decisions",
	}); err != nil {
		t.Fatalf("InsertHookActivity skipped: %v", err)
	}
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runLog(20, false)
	})
	if runErr != nil {
		t.Fatalf("runLog: %v", runErr)
	}

	expectTs1 := time.Unix(ts1, 0).Local().Format("2006-01-02 15:04")
	if !strings.Contains(out, expectTs1) {
		t.Fatalf("expected timestamp %q in output: %s", expectTs1, out)
	}
	if !strings.Contains(out, "context-surfacing") || !strings.Contains(out, "injected") {
		t.Fatalf("expected context-surfacing injected row, got: %s", out)
	}
	if !strings.Contains(out, "3 notes") || !strings.Contains(out, "~240 tokens") {
		t.Fatalf("expected injected counts/tokens, got: %s", out)
	}
	if !strings.Contains(out, "decision-extractor") || !strings.Contains(out, "skipped") {
		t.Fatalf("expected decision-extractor skipped row, got: %s", out)
	}
	if !strings.Contains(out, "(no new decisions)") {
		t.Fatalf("expected skipped detail, got: %s", out)
	}
}

func TestLogCmd_JSONOutput(t *testing.T) {
	_, db := setupCommandTestVault(t)
	if err := db.InsertHookActivity(&store.HookActivityRecord{
		HookSessionID:   "sess-json",
		HookName:        "staleness-check",
		Status:          "empty",
		EstimatedTokens: 0,
		Detail:          "no stale notes",
	}); err != nil {
		t.Fatalf("InsertHookActivity: %v", err)
	}
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runLog(20, true)
	})
	if runErr != nil {
		t.Fatalf("runLog: %v", runErr)
	}

	var entries []store.HookActivityRecord
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput=%s", err, out)
	}
	if len(entries) == 0 {
		t.Fatalf("expected at least one JSON entry, got %d", len(entries))
	}
	if entries[0].HookName != "staleness-check" {
		t.Fatalf("hook_name = %q, want staleness-check", entries[0].HookName)
	}
}
