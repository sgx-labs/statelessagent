package mcp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	maxGitDirtyFiles = 20
	maxGitCommits    = 5
)

// collectGitContext returns best-effort git metadata for get_session_context.
// Returns nil when the vault is not in a git repository.
func collectGitContext(startPath string) map[string]any {
	gitRoot := findGitRoot(startPath)
	if gitRoot == "" {
		return nil
	}

	result := map[string]any{}
	var notes []string

	branch, err := runGit(gitRoot, "branch", "--show-current")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return map[string]any{"note": "git not installed"}
		}
		notes = append(notes, "branch unavailable")
	} else if branch != "" {
		result["branch"] = strings.TrimSpace(branch)
	}

	logOut, err := runGit(gitRoot, "log", "--oneline", "-5")
	if err != nil {
		notes = append(notes, "commit history unavailable")
	} else {
		commits := splitNonEmptyLines(logOut, maxGitCommits)
		if len(commits) > 0 {
			result["last_commits"] = commits
		}
	}

	statusOut, err := runGit(gitRoot, "status", "--porcelain")
	if err != nil {
		notes = append(notes, "status unavailable")
	} else {
		dirty, untracked := parsePorcelainStatus(statusOut)
		if len(dirty) > 0 {
			result["dirty_files"] = dirty
		}
		if len(untracked) > 0 {
			result["untracked"] = untracked
		}
	}

	if len(notes) > 0 {
		result["note"] = strings.Join(notes, "; ")
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func findGitRoot(startPath string) string {
	dir, err := filepath.Abs(startPath)
	if err != nil {
		return ""
	}
	info, err := os.Stat(dir)
	if err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	for {
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func runGit(root string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func splitNonEmptyLines(text string, max int) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= max {
			break
		}
	}
	return out
}

func parsePorcelainStatus(status string) (dirty []string, untracked []string) {
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 3 {
			continue
		}
		code := line[:2]
		pathStart := 3
		if len(line) > 2 && line[2] != ' ' {
			pathStart = 2
		}
		if len(line) < pathStart {
			continue
		}
		path := strings.TrimSpace(line[pathStart:])
		if path == "" {
			continue
		}
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = strings.TrimSpace(parts[len(parts)-1])
		}
		if code == "??" {
			if len(untracked) < maxGitDirtyFiles {
				untracked = append(untracked, path)
			}
			continue
		}
		if len(dirty) < maxGitDirtyFiles {
			dirty = append(dirty, path)
		}
	}
	return dirty, untracked
}
