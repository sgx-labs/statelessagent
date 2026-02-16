package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFindGitRoot_NotRepo(t *testing.T) {
	dir := t.TempDir()
	if got := findGitRoot(dir); got != "" {
		t.Fatalf("expected no git root, got %q", got)
	}
}

func TestCollectGitContext_Repo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	repo := t.TempDir()
	runGitCmd(t, repo, "init")
	runGitCmd(t, repo, "config", "user.name", "test-user")
	runGitCmd(t, repo, "config", "user.email", "test@example.com")

	tracked := filepath.Join(repo, "tracked.md")
	if err := os.WriteFile(tracked, []byte("# tracked\n"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	runGitCmd(t, repo, "add", "tracked.md")
	runGitCmd(t, repo, "commit", "-m", "initial commit")

	// Dirty tracked file
	if err := os.WriteFile(tracked, []byte("# tracked\nupdated\n"), 0o644); err != nil {
		t.Fatalf("update tracked file: %v", err)
	}
	// Untracked file
	if err := os.WriteFile(filepath.Join(repo, "new.md"), []byte("# new\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	ctx := collectGitContext(repo)
	if ctx == nil {
		t.Fatal("expected git context, got nil")
	}

	branch, ok := ctx["branch"].(string)
	if !ok || branch == "" {
		t.Fatalf("expected non-empty branch, got %#v", ctx["branch"])
	}

	commits, ok := ctx["last_commits"].([]string)
	if !ok || len(commits) == 0 {
		t.Fatalf("expected commits in context, got %#v", ctx["last_commits"])
	}

	dirty, ok := ctx["dirty_files"].([]string)
	if !ok || !containsString(dirty, "tracked.md") {
		t.Fatalf("expected tracked.md in dirty_files, got %#v", ctx["dirty_files"])
	}

	untracked, ok := ctx["untracked"].([]string)
	if !ok || !containsString(untracked, "new.md") {
		t.Fatalf("expected new.md in untracked, got %#v", ctx["untracked"])
	}
}

func TestParsePorcelainStatus(t *testing.T) {
	status := " M tracked.md\n?? new.md\nR  old.md -> renamed.md\n"
	dirty, untracked := parsePorcelainStatus(status)

	if !containsString(dirty, "tracked.md") {
		t.Fatalf("expected tracked.md in dirty list, got %v", dirty)
	}
	if !containsString(dirty, "renamed.md") {
		t.Fatalf("expected renamed.md in dirty list, got %v", dirty)
	}
	if !containsString(untracked, "new.md") {
		t.Fatalf("expected new.md in untracked list, got %v", untracked)
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
