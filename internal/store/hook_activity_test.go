package store

import (
	"fmt"
	"testing"
	"time"
)

func TestHookActivityCRUD(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	rec := &HookActivityRecord{
		TimestampUnix:   time.Now().Unix(),
		HookSessionID:   "sess-1",
		HookName:        "context-surfacing",
		Status:          "injected",
		SurfacedNotes:   3,
		EstimatedTokens: 240,
		Detail:          "context injected",
		NotePaths:       []string{"notes/a.md", "notes/b.md", "notes/c.md"},
	}
	if err := db.InsertHookActivity(rec); err != nil {
		t.Fatalf("InsertHookActivity: %v", err)
	}

	rows, err := db.GetRecentHookActivity(20)
	if err != nil {
		t.Fatalf("GetRecentHookActivity: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 hook entry, got %d", len(rows))
	}
	got := rows[0]
	if got.HookName != "context-surfacing" {
		t.Fatalf("hook name = %q, want context-surfacing", got.HookName)
	}
	if got.Status != "injected" {
		t.Fatalf("status = %q, want injected", got.Status)
	}
	if got.SurfacedNotes != 3 || got.EstimatedTokens != 240 {
		t.Fatalf("counts = notes:%d tokens:%d, want 3/240", got.SurfacedNotes, got.EstimatedTokens)
	}
	if len(got.NotePaths) != 3 {
		t.Fatalf("expected 3 note paths, got %d", len(got.NotePaths))
	}
}

func TestHookActivityPruneKeepsLatest500(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	base := time.Now().Unix()
	total := maxHookActivityRows + 25
	for i := 0; i < total; i++ {
		if err := db.InsertHookActivity(&HookActivityRecord{
			TimestampUnix: base + int64(i),
			HookName:      "context-surfacing",
			Status:        "empty",
			Detail:        fmt.Sprintf("entry-%d", i),
		}); err != nil {
			t.Fatalf("InsertHookActivity[%d]: %v", i, err)
		}
	}

	var hookCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM session_log WHERE entry_kind = 'hook'`).Scan(&hookCount); err != nil {
		t.Fatalf("count hook rows: %v", err)
	}
	if hookCount != maxHookActivityRows {
		t.Fatalf("hook row count = %d, want %d", hookCount, maxHookActivityRows)
	}

	rows, err := db.GetRecentHookActivity(1)
	if err != nil {
		t.Fatalf("GetRecentHookActivity: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Detail != fmt.Sprintf("entry-%d", total-1) {
		t.Fatalf("latest entry detail = %q, want entry-%d", rows[0].Detail, total-1)
	}
}

func TestHookActivityDoesNotAffectSessionHistory(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if err := db.InsertSession(&SessionRecord{
		SessionID:    "session-1",
		StartedAt:    "2026-02-01T10:00:00Z",
		EndedAt:      "2026-02-01T10:15:00Z",
		HandoffPath:  "sessions/2026-02-01-handoff.md",
		Machine:      "machine-1",
		FilesChanged: []string{"a.md"},
		Summary:      "summary",
	}); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	if err := db.InsertHookActivity(&HookActivityRecord{
		HookSessionID: "session-1",
		HookName:      "decision-extractor",
		Status:        "skipped",
		Detail:        "no new decisions",
	}); err != nil {
		t.Fatalf("InsertHookActivity: %v", err)
	}

	sessions, err := db.GetRecentSessions(10, "")
	if err != nil {
		t.Fatalf("GetRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session history row, got %d", len(sessions))
	}
	if sessions[0].SessionID != "session-1" {
		t.Fatalf("session_id = %q, want session-1", sessions[0].SessionID)
	}
}
