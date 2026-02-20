package main

import (
	"strings"
	"testing"
)

func TestHooksCmd_ShowStatus(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	cmd := hooksCmd()

	var execErr error
	out := captureCommandStdout(t, func() {
		execErr = cmd.Execute()
	})
	if execErr != nil {
		t.Fatalf("hooks: %v", execErr)
	}
	if !strings.Contains(out, "context-surfacing") {
		t.Fatalf("expected context-surfacing in output, got: %q", out)
	}
	if !strings.Contains(out, "session-bootstrap") {
		t.Fatalf("expected session-bootstrap in output, got: %q", out)
	}
}
