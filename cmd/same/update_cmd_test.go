package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchSHA256Sums(t *testing.T) {
	h1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	h2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	body := fmt.Sprintf("%s  same-linux-amd64\n%s  artifacts/same-windows-amd64.exe/same-windows-amd64.exe\n", h1, h2)

	client := &http.Client{
		Transport: rtFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	m, err := fetchSHA256Sums(client, "https://example.com/sha256sums.txt")
	if err != nil {
		t.Fatalf("fetchSHA256Sums: %v", err)
	}
	if got := m["same-linux-amd64"]; got != h1 {
		t.Fatalf("linux checksum mismatch: got %q, want %q", got, h1)
	}
	if got := m["same-windows-amd64.exe"]; got != h2 {
		t.Fatalf("windows checksum mismatch: got %q, want %q", got, h2)
	}
}

func TestFetchSHA256Sums_RejectsMalformed(t *testing.T) {
	client := &http.Client{
		Transport: rtFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("not-a-valid-checksum-line\n")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	if _, err := fetchSHA256Sums(client, "https://example.com/sha256sums.txt"); err == nil {
		t.Fatal("expected parse error for malformed checksum file")
	}
}

func TestRemoveTempFile_RemovesExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "same-update.tmp")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	removeTempFile(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, stat err=%v", err)
	}
}

func TestRemoveTempFile_IgnoresMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.tmp")
	removeTempFile(path)
}

func TestPrepareWindowsBackup_NoExistingOld(t *testing.T) {
	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "same.exe")

	got := prepareWindowsBackup(execPath)
	if got != execPath+".old" {
		t.Fatalf("expected %q, got %q", execPath+".old", got)
	}
}

func TestPrepareWindowsBackup_RemovesExistingOld(t *testing.T) {
	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "same.exe")
	oldPath := execPath + ".old"
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := prepareWindowsBackup(execPath)
	if got != oldPath {
		t.Fatalf("expected %q, got %q", oldPath, got)
	}
	// The .old file should have been removed.
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected .old to be removed, stat err=%v", err)
	}
}

func TestPrepareWindowsBackup_FallsBackToNumbered(t *testing.T) {
	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "same.exe")

	// Create a non-empty directory at .old so os.Remove fails (not a "not exist" error).
	oldPath := execPath + ".old"
	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := prepareWindowsBackup(execPath)
	expected := execPath + ".old.1"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestVersionOutput_IncludesHash(t *testing.T) {
	oldVersion := Version
	oldHash := CommitHash
	Version = "0.12.1"
	CommitHash = "abc1234"
	t.Cleanup(func() {
		Version = oldVersion
		CommitHash = oldHash
	})

	out := captureCommandStdout(t, func() {
		cmd := versionCmd()
		_ = cmd.Execute()
	})
	// Should show version+hash format
	if !strings.Contains(out, "same 0.12.1+abc1234") {
		t.Fatalf("expected version with hash, got: %q", out)
	}
}

func TestVersionOutput_DevBuild(t *testing.T) {
	oldVersion := Version
	oldHash := CommitHash
	Version = "dev"
	CommitHash = "unknown"
	t.Cleanup(func() {
		Version = oldVersion
		CommitHash = oldHash
	})

	out := captureCommandStdout(t, func() {
		cmd := versionCmd()
		_ = cmd.Execute()
	})
	// Should not include hash for dev builds
	if strings.Contains(out, "+") {
		t.Fatalf("dev build should not include hash, got: %q", out)
	}
	if !strings.Contains(out, "same dev") {
		t.Fatalf("expected 'same dev' in output, got: %q", out)
	}
}

type rtFunc func(req *http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
