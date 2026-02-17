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
		{name: "owner repo", input: "owner/repo", want: "owner_repo"},
		{name: "ssh remote", input: "git@github.com:owner/repo.git", want: "git_github.com_owner_repo"},
		{name: "keeps safe chars", input: "my.repo_v2", want: "my.repo_v2"},
		{name: "replaces unsafe chars", input: "repo name!*", want: "repo_name"},
		{name: "maps plus to underscore", input: "owner/repo+dev", want: "owner_repo_dev"},
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
	if repo != "owner_repo" {
		t.Fatalf("ticket repo = %q, want owner_repo", repo)
	}
	if strings.Contains(path, "owner/") {
		t.Fatalf("ticket path should not contain nested repo path, got %q", path)
	}
	if filepath.Base(path) != "push-ticket-owner_repo" {
		t.Fatalf("ticket file = %q, want push-ticket-owner_repo", filepath.Base(path))
	}
}

func TestPushTicketPathAvoidsOwnerRepoCollisions(t *testing.T) {
	path1, repo1, err := pushTicketPath("owner-a/repo")
	if err != nil {
		t.Fatalf("pushTicketPath owner-a error: %v", err)
	}
	path2, repo2, err := pushTicketPath("owner-b/repo")
	if err != nil {
		t.Fatalf("pushTicketPath owner-b error: %v", err)
	}
	if repo1 == repo2 || path1 == path2 {
		t.Fatalf("ticket identity collision: %q (%q) vs %q (%q)", repo1, path1, repo2, path2)
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
	if !strings.Contains(hook, `REMOTE_URL=$(git remote get-url origin 2>/dev/null)`) {
		t.Fatalf("hook missing remote url detection:\n%s", hook)
	}
	if !strings.Contains(hook, `SAFE_REPO=$(printf "%s" "$REMOTE_URL" | tr -c 'A-Za-z0-9_.-' '_' | sed 's/^[._-]*//; s/[._-]*$//')`) {
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
