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

type rtFunc func(req *http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
