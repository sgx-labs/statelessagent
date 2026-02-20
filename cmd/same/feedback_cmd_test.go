package main

import (
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestFeedbackCmd_ValidUp(t *testing.T) {
	_, db := setupCommandTestVault(t)
	insertCommandTestNote(t, db, "test.md", "Test Note", "Some content.")
	_ = db.Close()

	if err := runFeedback("test.md", "up"); err != nil {
		t.Fatalf("runFeedback up: %v", err)
	}

	db2, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	notes, err := db2.GetNoteByPath("test.md")
	if err != nil {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if len(notes) == 0 {
		t.Fatalf("expected note to exist after feedback")
	}
	if notes[0].Confidence <= 0.8 {
		t.Fatalf("expected confidence boost, got %.2f", notes[0].Confidence)
	}
}

func TestFeedbackCmd_InvalidDirection(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	err := runFeedback("test.md", "sideways")
	if err == nil {
		t.Fatal("expected error for invalid direction")
	}
}
