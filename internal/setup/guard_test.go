package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAskYesNoLater(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"\n", "yes"},
		{"yes\n", "yes"},
		{"y\n", "yes"},
		{"no\n", "no"},
		{"n\n", "no"},
		{"later\n", "later"},
		{"l\n", "later"},
		{"unexpected\n", "yes"},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.input), func(t *testing.T) {
			got := withStdin(t, tc.input, func() string {
				return askYesNoLater("Enable guard?")
			})
			if got != tc.want {
				t.Fatalf("askYesNoLater(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSetupGuard_NotGitRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := SetupGuard(dir); err != nil {
		t.Fatalf("SetupGuard: %v", err)
	}

	cfgPath := filepath.Join(home, ".config", "same", "config.json")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("expected guard config at %s: %v", cfgPath, err)
	}
}

func TestSetupGuard_InstallsHookInGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := t.TempDir()
	runGitCmd(t, repo, "init")

	cwd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := SetupGuard(repo); err != nil {
		t.Fatalf("SetupGuard: %v", err)
	}

	hookPath := filepath.Join(repo, ".git", "hooks", "pre-commit")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read pre-commit hook: %v", err)
	}
	if !strings.Contains(string(content), "SAME Guard pre-commit hook") {
		t.Fatalf("expected SAME guard hook content, got:\n%s", string(content))
	}
}

func TestSetupGuard_PreservesExistingNonSAMEHook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := t.TempDir()
	runGitCmd(t, repo, "init")

	hookPath := filepath.Join(repo, ".git", "hooks", "pre-commit")
	original := "#!/bin/sh\necho custom-hook\n"
	if err := os.WriteFile(hookPath, []byte(original), 0o755); err != nil {
		t.Fatalf("write existing hook: %v", err)
	}

	cwd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := SetupGuard(repo); err != nil {
		t.Fatalf("SetupGuard: %v", err)
	}

	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if string(content) != original {
		t.Fatalf("expected non-SAME hook to remain unchanged")
	}
}

func withStdin(t *testing.T, input string, fn func() string) string {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "stdin.txt")
	if err := os.WriteFile(tmp, []byte(input), 0o600); err != nil {
		t.Fatalf("write temp stdin: %v", err)
	}

	f, err := os.Open(tmp)
	if err != nil {
		t.Fatalf("open temp stdin: %v", err)
	}
	defer f.Close()

	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()

	return fn()
}

func runGitCmd(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}
