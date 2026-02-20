package main

import (
	"strings"
	"testing"
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
