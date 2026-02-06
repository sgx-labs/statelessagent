package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/guard"
)

// SetupGuard installs the SAME Guard pre-commit hook and writes default config.
func SetupGuard(vaultPath string) error {
	// Write default config (all patterns on)
	cfg := guard.DefaultGuardConfig()
	if err := guard.SaveGuardConfig(cfg); err != nil {
		return fmt.Errorf("save guard config: %w", err)
	}

	// Install pre-commit hook
	gitRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		// Not a git repo — config saved but no hook to install
		fmt.Printf("  %s✓%s Guard config saved\n", cli.Green, cli.Reset)
		fmt.Printf("    Run 'same guard install' inside a git repo to add the hook.\n")
		return nil
	}
	root := strings.TrimSpace(string(gitRoot))
	hookPath := filepath.Join(root, ".git", "hooks", "pre-commit")

	// Don't overwrite existing hooks that aren't ours
	if content, err := os.ReadFile(hookPath); err == nil {
		if !strings.Contains(string(content), "SAME Guard") {
			fmt.Printf("  %s✓%s Guard config saved\n", cli.Green, cli.Reset)
			fmt.Printf("  %s!%s Existing pre-commit hook detected (not SAME).\n",
				cli.Yellow, cli.Reset)
			fmt.Printf("    Run 'same guard install --force' to replace it.\n")
			return nil
		}
	}

	// Find the same binary
	sameBin, err := os.Executable()
	if err != nil {
		sameBin = "same"
	}

	hook := fmt.Sprintf(`#!/bin/sh
# SAME Guard pre-commit hook
# Installed by: same init
# Scans staged files for PII, blocklisted terms, and unauthorized paths.

SAME_BIN="%s"

if [ ! -x "$SAME_BIN" ] && ! command -v same >/dev/null 2>&1; then
    echo "Warning: SAME binary not found. Skipping guard check."
    echo "Reinstall with: same guard install"
    exit 0
fi

if command -v same >/dev/null 2>&1; then
    SAME_BIN="same"
fi

$SAME_BIN guard scan --staged
`, sameBin)

	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}
	if err := os.WriteFile(hookPath, []byte(hook), 0o755); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}

	return nil
}

// setupGuardInteractive runs the guard setup with a yes/no/later prompt.
func setupGuardInteractive(vaultPath string, autoAccept bool) {
	cli.Section("Guard")

	if !autoAccept {
		fmt.Printf("  SAME Guard protects your vault by scanning git commits\n")
		fmt.Printf("  for personal info (emails, phone numbers, file paths,\n")
		fmt.Printf("  API keys) before they leave your machine.\n")
		fmt.Println()
		fmt.Printf("  It never touches your notes — only git commits.\n")
		fmt.Println()
		fmt.Printf("  Recommended: %sOn%s\n", cli.Green, cli.Reset)
		fmt.Println()
	}

	if autoAccept {
		if err := SetupGuard(vaultPath); err != nil {
			fmt.Printf("  %s!%s Could not set up Guard: %v\n",
				cli.Yellow, cli.Reset, err)
			return
		}
		fmt.Printf("  %s✓%s Guard installed\n", cli.Green, cli.Reset)
		fmt.Printf("    It scans every commit silently. You'll only see it\n")
		fmt.Printf("    when it finds something.\n")
		fmt.Printf("    Adjust anytime: %ssame guard settings%s\n", cli.Bold, cli.Reset)
		return
	}

	choice := askYesNoLater("  Enable SAME Guard?")

	switch choice {
	case "yes":
		if err := SetupGuard(vaultPath); err != nil {
			fmt.Printf("  %s!%s Could not set up Guard: %v\n",
				cli.Yellow, cli.Reset, err)
			return
		}
		fmt.Println()
		fmt.Printf("  %s✓%s Guard installed!\n", cli.Green, cli.Reset)
		fmt.Printf("    It scans every commit silently. You'll only see it\n")
		fmt.Printf("    when it finds something.\n")
		fmt.Printf("    Adjust anytime: %ssame guard settings%s\n", cli.Bold, cli.Reset)

	case "later":
		fmt.Println()
		fmt.Printf("  No problem! Enable anytime:\n")
		fmt.Printf("    %ssame guard install%s\n", cli.Bold, cli.Reset)

	case "no":
		// Write config with guard explicitly disabled
		cfg := guard.DefaultGuardConfig()
		cfg.Enabled = false
		if err := guard.SaveGuardConfig(cfg); err != nil {
			fmt.Printf("  %s!%s Could not save config: %v\n",
				cli.Yellow, cli.Reset, err)
			return
		}
		fmt.Println()
		fmt.Printf("  Guard disabled. Enable anytime:\n")
		fmt.Printf("    %ssame guard settings set guard on%s\n", cli.Bold, cli.Reset)
	}
}

// askYesNoLater prompts for yes/no/later. Returns "yes", "no", or "later".
func askYesNoLater(question string) string {
	fmt.Printf("%s (yes/no/later) [yes]: ", question)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "yes" // default
	}
	line = strings.TrimSpace(strings.ToLower(line))

	switch line {
	case "":
		return "yes"
	case "y", "yes":
		return "yes"
	case "n", "no":
		return "no"
	case "l", "later":
		return "later"
	default:
		return "yes"
	}
}
