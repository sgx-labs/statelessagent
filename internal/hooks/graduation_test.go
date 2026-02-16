package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestGraduationMessagesNonEmpty(t *testing.T) {
	if msg := ciTipMessage(); !strings.Contains(msg, "same ci init") {
		t.Fatalf("ciTipMessage missing command guidance: %q", msg)
	}
	if msg := pushProtectTipMessage(); !strings.Contains(msg, "push-protect on") {
		t.Fatalf("pushProtectTipMessage missing command guidance: %q", msg)
	}
}

func TestCheckGraduationAndMilestones(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	repo := initGitRepoWithCommits(t, 10)
	cwd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	msg := CheckGraduation(db)
	if !strings.Contains(msg, "same ci init") {
		t.Fatalf("expected CI graduation tip, got %q", msg)
	}

	// The next call may show the next milestone (push-protect).
	msg = CheckGraduation(db)
	if !strings.Contains(msg, "push-protect on") {
		t.Fatalf("expected push-protect tip on second call, got %q", msg)
	}

	// After both milestones are recorded, no further tip is shown.
	msg = CheckGraduation(db)
	if msg != "" {
		t.Fatalf("expected no further tips after all milestones recorded, got %q", msg)
	}
}

func TestShouldSuggestPushProtect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	db, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	repo := initGitRepoWithCommits(t, 6)
	cwd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if !shouldSuggestPushProtect(db) {
		t.Fatal("expected push protection suggestion with 6 commits and no SAME pre-push hook")
	}

	hookPath := filepath.Join(repo, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hookPath, []byte("# SAME Guard\n"), 0o755); err != nil {
		t.Fatalf("write pre-push hook: %v", err)
	}
	if shouldSuggestPushProtect(db) {
		t.Fatal("expected no suggestion when SAME pre-push hook already exists")
	}
}

func initGitRepoWithCommits(t *testing.T, commitCount int) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "test-user")
	runGit(t, repo, "config", "user.email", "test@example.com")

	seed := filepath.Join(repo, "README.md")
	if err := os.WriteFile(seed, []byte("# repo\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "seed")

	for i := 1; i < commitCount; i++ {
		runGit(t, repo, "commit", "--allow-empty", "-m", "commit")
	}
	return repo
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}
