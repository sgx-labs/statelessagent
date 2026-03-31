package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeErrorForJSON_RemovesPaths(t *testing.T) {
	cases := []error{
		fmt.Errorf("open /home/user/vault/notes.db: permission denied"),
		fmt.Errorf(`open C:\Users\jdoe\vault\notes.db: access denied`),
	}

	for _, input := range cases {
		got := sanitizeErrorForJSON(input)
		if strings.Contains(got, "/home/user") || strings.Contains(strings.ToLower(got), `c:\users`) {
			t.Fatalf("expected path redaction, got: %q", got)
		}
		if !strings.Contains(strings.ToLower(got), "denied") {
			t.Fatalf("expected error detail to remain, got: %q", got)
		}
	}
}

func TestSanitizeErrorForJSON_PreservesCleanErrors(t *testing.T) {
	err := errors.New("connection refused")
	got := sanitizeErrorForJSON(err)
	if got != "connection refused" {
		t.Fatalf("sanitizeErrorForJSON() = %q, want %q", got, "connection refused")
	}
}

func TestRunDoctor_JSONOutput_Structure(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runDoctor(true)
	})
	if runErr != nil && !strings.Contains(runErr.Error(), "check(s) failed") {
		t.Fatalf("unexpected runDoctor error: %v", runErr)
	}

	var report DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor JSON output should parse: %v (%q)", err, out)
	}
	if report.Summary.Total <= 0 {
		t.Fatalf("expected at least one check in summary, got %+v", report.Summary)
	}
	if report.Summary.Total != report.Summary.Passed+report.Summary.Skipped+report.Summary.Failed {
		t.Fatalf("summary totals inconsistent: %+v", report.Summary)
	}
}

func TestRunDoctor_TextOutput_ReturnsSummary(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runDoctor(false)
	})
	if runErr != nil && !strings.Contains(runErr.Error(), "check(s) failed") {
		t.Fatalf("unexpected runDoctor error: %v", runErr)
	}
	if !strings.Contains(out, "SAME Health Check") {
		t.Fatalf("expected header in text output, got: %q", out)
	}
}

func TestDoctorResult_StatusValues(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runDoctor(true)
	})
	if runErr != nil && !strings.Contains(runErr.Error(), "check(s) failed") {
		t.Fatalf("unexpected runDoctor error: %v", runErr)
	}

	var report DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode doctor report: %v", err)
	}

	valid := map[string]bool{"pass": true, "skip": true, "fail": true}
	for _, check := range report.Checks {
		if !valid[check.Status] {
			t.Fatalf("invalid status %q for check %q", check.Status, check.Name)
		}
	}
}

func TestDoctor_BinaryShadowing(t *testing.T) {
	// Create a fake 'same' binary in a temp dir
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "same")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho fake"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	// Prepend temp dir to PATH so the fake binary is found
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+origPath)

	_, err := checkBinaryShadowing()
	if err == nil {
		t.Fatal("expected shadowing warning, got nil error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "Other 'same' binaries found in PATH") {
		t.Fatalf("expected shadowing warning message, got: %q", errMsg)
	}
	if !strings.Contains(errMsg, tmpDir) {
		t.Fatalf("expected shadowing path to include temp dir %q, got: %q", tmpDir, errMsg)
	}
}

func TestDoctor_NoBinaryShadowing(t *testing.T) {
	// With an empty PATH, no other binaries should be found
	t.Setenv("PATH", "")

	detail, err := checkBinaryShadowing()
	if err != nil {
		t.Fatalf("expected no shadowing error, got: %v", err)
	}
	if !strings.Contains(detail, "binary:") {
		t.Fatalf("expected binary detail in output, got: %q", detail)
	}
}
