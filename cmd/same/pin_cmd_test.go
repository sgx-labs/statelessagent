package main

import (
	"strings"
	"testing"
)

func TestPinCmd_ListEmpty(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runPinList()
	})
	if runErr != nil {
		t.Fatalf("runPinList: %v", runErr)
	}
	if !strings.Contains(out, "No pinned notes") {
		t.Fatalf("expected empty pin list message, got: %q", out)
	}
}

func TestPinCmd_PinAndList(t *testing.T) {
	_, db := setupCommandTestVault(t)
	insertCommandTestNote(t, db, "important.md", "Important Note", "Critical information.")
	_ = db.Close()

	if err := runPinAdd("important.md"); err != nil {
		t.Fatalf("runPinAdd: %v", err)
	}

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runPinList()
	})
	if runErr != nil {
		t.Fatalf("runPinList: %v", runErr)
	}
	if !strings.Contains(out, "Important Note") {
		t.Fatalf("expected pinned note in list output, got: %q", out)
	}
}
