package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeRepoTicketName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "owner repo", input: "owner/repo", want: "repo"},
		{name: "ssh remote", input: "git@github.com:owner/repo.git", want: "repo"},
		{name: "keeps safe chars", input: "my.repo_v2", want: "my.repo_v2"},
		{name: "replaces unsafe chars", input: "repo name!*", want: "repo_name"},
		{name: "maps plus to underscore", input: "owner/repo+dev", want: "repo_dev"},
		{name: "empty", input: "   ", wantErr: true},
		{name: "invalid only", input: "///", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeRepoTicketName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("sanitizeRepoTicketName(%q) expected error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeRepoTicketName(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("sanitizeRepoTicketName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPushTicketPathSanitizesRepoName(t *testing.T) {
	path, repo, err := pushTicketPath("owner/repo")
	if err != nil {
		t.Fatalf("pushTicketPath() error: %v", err)
	}
	if repo != "repo" {
		t.Fatalf("ticket repo = %q, want repo", repo)
	}
	if strings.Contains(path, "owner/") {
		t.Fatalf("ticket path should not contain nested repo path, got %q", path)
	}
	if filepath.Base(path) != "push-ticket-repo" {
		t.Fatalf("ticket file = %q, want push-ticket-repo", filepath.Base(path))
	}
}

func TestRunGuardPushInstallWritesTicketPathWithSlash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "git@github.com:owner/repo.git")

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir to temp repo: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	if err := runGuardPushInstall(false); err != nil {
		t.Fatalf("runGuardPushInstall() error: %v", err)
	}

	hookPath := filepath.Join(repoDir, ".git", "hooks", "pre-push")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	hook := string(content)
	if !strings.Contains(hook, `SAFE_REPO=$(printf "%s" "$REPO" | tr -c 'A-Za-z0-9_.-' '_' | sed 's/^[._-]*//; s/[._-]*$//')`) {
		t.Fatalf("hook missing repo sanitization:\n%s", hook)
	}
	if !strings.Contains(hook, `TICKET="${TMPBASE}/push-ticket-$SAFE_REPO"`) {
		t.Fatalf("hook missing expected ticket path:\n%s", hook)
	}
	if strings.Contains(hook, `TICKET="${TMPBASE}push-ticket-$REPO"`) {
		t.Fatalf("hook still contains invalid ticket path:\n%s", hook)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
