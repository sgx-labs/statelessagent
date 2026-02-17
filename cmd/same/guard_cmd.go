package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/guard"
)

func guardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Scan notes for sensitive info before sharing",
		Long: `SAME Guard scans staged files for PII, blocklisted terms, and
unauthorized file paths before they reach git.

Run 'same guard install' to set up the git pre-commit hook.`,
	}

	cmd.AddCommand(guardScanCmd())
	cmd.AddCommand(guardInstallCmd())
	cmd.AddCommand(guardUninstallCmd())
	cmd.AddCommand(guardStatusCmd())
	cmd.AddCommand(guardReviewCmd())
	cmd.AddCommand(guardBlocklistCmd())
	cmd.AddCommand(guardAllowCmd())
	cmd.AddCommand(guardSettingsCmd())
	cmd.AddCommand(guardPushInstallCmd())
	cmd.AddCommand(guardPushUninstallCmd())

	return cmd
}

func guardScanCmd() *cobra.Command {
	var (
		staged  bool
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan staged files for PII and blocklisted content",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardScan(staged, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&staged, "staged", true, "Only scan staged files")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Machine-readable JSON output")
	return cmd
}

func runGuardScan(staged, jsonOut bool) error {
	vaultPath := config.VaultPath()
	scanner, err := guard.NewScanner(vaultPath)
	if err != nil {
		return fmt.Errorf("init scanner: %w", err)
	}

	var result *guard.ScanResult
	if staged {
		result, err = scanner.ScanStaged()
	} else {
		return fmt.Errorf("non-staged scanning not yet supported")
	}
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	if jsonOut {
		fmt.Println(result.FormatJSON())
	} else {
		fmt.Print(result.FormatFriendly())
	}

	// Cache last scan for the allow flow
	if !result.Passed {
		_ = guard.SaveLastScan(vaultPath, result)
		os.Exit(1)
	}
	return nil
}

func guardInstallCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install git pre-commit hook",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardInstall(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing hook")
	return cmd
}

func runGuardInstall(force bool) error {
	// Find git root
	gitRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}
	root := strings.TrimSpace(string(gitRoot))
	hookPath := filepath.Join(root, ".git", "hooks", "pre-commit")

	// Check for existing hook
	if _, err := os.Stat(hookPath); err == nil && !force {
		return fmt.Errorf("pre-commit hook already exists. Use --force to overwrite")
	}

	// Find the same binary
	sameBin, err := os.Executable()
	if err != nil {
		sameBin = "same" // fall back to PATH lookup
	}

	hook := fmt.Sprintf(`#!/bin/sh
# SAME Guard pre-commit hook
# Installed by: same guard install
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

	fmt.Printf("  %s✓%s Pre-commit hook installed at %s\n", cli.Green, cli.Reset, hookPath)
	fmt.Printf("  Guard will scan staged files on every commit.\n")
	fmt.Printf("  Bypass with: git commit --no-verify (emergency only)\n")
	return nil
}

func guardUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the git pre-commit hook",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardUninstall()
		},
	}
}

func runGuardUninstall() error {
	gitRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}
	root := strings.TrimSpace(string(gitRoot))
	hookPath := filepath.Join(root, ".git", "hooks", "pre-commit")

	// Read the hook to verify it's ours
	content, err := os.ReadFile(hookPath)
	if os.IsNotExist(err) {
		fmt.Println("  No pre-commit hook found.")
		return nil
	}
	if err != nil {
		return err
	}

	if !strings.Contains(string(content), "SAME Guard") {
		return fmt.Errorf("pre-commit hook exists but was not installed by SAME Guard. Remove manually if needed")
	}

	if err := os.Remove(hookPath); err != nil {
		return err
	}
	fmt.Printf("  %s✓%s Pre-commit hook removed.\n", cli.Green, cli.Reset)
	return nil
}

func guardStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show guard configuration and recent audit",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardStatus()
		},
	}
}

func runGuardStatus() error {
	vaultPath := config.VaultPath()

	fmt.Printf("\n%sSAME Guard Status%s\n\n", cli.Bold, cli.Reset)

	// Check hook
	gitRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		hookPath := filepath.Join(strings.TrimSpace(string(gitRoot)), ".git", "hooks", "pre-commit")
		if content, err := os.ReadFile(hookPath); err == nil && strings.Contains(string(content), "SAME Guard") {
			fmt.Printf("  Hook:       %s✓ installed%s\n", cli.Green, cli.Reset)
		} else {
			fmt.Printf("  Hook:       %snot installed%s (run: same guard install)\n", cli.Dim, cli.Reset)
		}
	}

	// Check blocklist
	blPath := filepath.Join(vaultPath, "_PRIVATE", ".blocklist")
	if _, err := os.Stat(blPath); err == nil {
		terms, _ := guard.LoadBlocklist(blPath)
		hard, soft := 0, 0
		for _, t := range terms {
			if t.Tier == guard.TierHard {
				hard++
			} else {
				soft++
			}
		}
		fmt.Printf("  Blocklist:  %d hard, %d soft terms\n", hard, soft)
	} else {
		fmt.Printf("  Blocklist:  %snot found%s (%s)\n", cli.Dim, cli.Reset, blPath)
	}

	// Check reviewed terms
	reviewed, err := guard.LoadReviewedTerms(vaultPath)
	if err == nil && len(reviewed.Terms) > 0 {
		fmt.Printf("  Reviewed:   %d terms\n", len(reviewed.Terms))
	} else {
		fmt.Printf("  Reviewed:   %snone%s\n", cli.Dim, cli.Reset)
	}

	// Audit log
	auditPath := filepath.Join(vaultPath, ".same", "publish-audit.log")
	if info, err := os.Stat(auditPath); err == nil {
		fmt.Printf("  Audit log:  %s (%.1f KB)\n", auditPath, float64(info.Size())/1024)
	} else {
		fmt.Printf("  Audit log:  %snot yet created%s\n", cli.Dim, cli.Reset)
	}

	fmt.Println()
	return nil
}

func guardReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Manage reviewed (false-positive) terms",
	}

	cmd.AddCommand(guardReviewListCmd())
	cmd.AddCommand(guardReviewAddCmd())
	cmd.AddCommand(guardReviewRemoveCmd())
	return cmd
}

func guardReviewListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show reviewed terms",
		RunE: func(cmd *cobra.Command, args []string) error {
			vaultPath := config.VaultPath()
			reviewed, err := guard.LoadReviewedTerms(vaultPath)
			if err != nil {
				return err
			}
			if len(reviewed.Terms) == 0 {
				fmt.Println("  No reviewed terms.")
				return nil
			}

			fmt.Printf("\n%sReviewed Terms%s\n\n", cli.Bold, cli.Reset)
			for _, t := range reviewed.Terms {
				fmt.Printf("  %-20s [%s]\n", t.Term, t.Category)
				fmt.Printf("    Files:    %s\n", strings.Join(t.Files, ", "))
				fmt.Printf("    Reason:   %s\n", t.Reason)
				fmt.Printf("    Reviewed: %s by %s\n", t.ReviewedAt, t.ReviewedBy)
			}
			fmt.Println()
			return nil
		},
	}
}

func guardReviewAddCmd() *cobra.Command {
	var (
		term     string
		reason   string
		file     string
		category string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a reviewed term (false positive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if term == "" {
				return fmt.Errorf("--term is required")
			}
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			if reason == "" {
				reason = "false positive"
			}
			if category == "" {
				category = string(guard.CatSoftBlock)
			}

			vaultPath := config.VaultPath()
			reviewed, err := guard.LoadReviewedTerms(vaultPath)
			if err != nil {
				return err
			}

			reviewed.Add(term, category, reason, "claude-agent", []string{file})
			if err := reviewed.Save(vaultPath); err != nil {
				return err
			}

			guard.AppendAudit(vaultPath, guard.AuditEntry{
				Action:  "review_add",
				Passed:  true,
				Details: map[string]string{"term": term, "file": file, "reason": reason},
			})

			fmt.Printf("  %s✓%s Added reviewed term: %s → %s\n", cli.Green, cli.Reset, term, file)
			return nil
		},
	}
	cmd.Flags().StringVar(&term, "term", "", "The term to mark as reviewed")
	cmd.Flags().StringVar(&reason, "reason", "", "Why this is a false positive")
	cmd.Flags().StringVar(&file, "file", "", "File path or glob pattern")
	cmd.Flags().StringVar(&category, "category", "", "Category (default: soft_blocklist)")
	return cmd
}

func guardReviewRemoveCmd() *cobra.Command {
	var (
		term     string
		category string
	)
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a reviewed term",
		RunE: func(cmd *cobra.Command, args []string) error {
			if term == "" {
				return fmt.Errorf("--term is required")
			}
			if category == "" {
				category = string(guard.CatSoftBlock)
			}

			vaultPath := config.VaultPath()
			reviewed, err := guard.LoadReviewedTerms(vaultPath)
			if err != nil {
				return err
			}

			if !reviewed.Remove(term, category) {
				return fmt.Errorf("term %q not found in reviewed list", term)
			}
			if err := reviewed.Save(vaultPath); err != nil {
				return err
			}

			guard.AppendAudit(vaultPath, guard.AuditEntry{
				Action:  "review_remove",
				Passed:  true,
				Details: map[string]string{"term": term},
			})

			fmt.Printf("  %s✓%s Removed reviewed term: %s\n", cli.Green, cli.Reset, term)
			return nil
		},
	}
	cmd.Flags().StringVar(&term, "term", "", "The term to remove")
	cmd.Flags().StringVar(&category, "category", "", "Category (default: soft_blocklist)")
	return cmd
}

func guardBlocklistCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "blocklist",
		Short: "Show effective blocklist",
		RunE: func(cmd *cobra.Command, args []string) error {
			vaultPath := config.VaultPath()
			blPath := filepath.Join(vaultPath, "_PRIVATE", ".blocklist")

			terms, err := guard.LoadBlocklist(blPath)
			if err != nil {
				return fmt.Errorf("load blocklist: %w", err)
			}
			if terms == nil {
				fmt.Printf("  No blocklist found at %s\n", blPath)
				fmt.Println("  Create it with [hard] and [soft] sections in TOML format.")
				return nil
			}

			fmt.Printf("\n%sEffective Blocklist%s (%s)\n\n", cli.Bold, cli.Reset, blPath)
			for _, t := range terms {
				fmt.Printf("  [%s] %s\n", t.Tier, t.Term)
			}
			fmt.Printf("\n  %d total terms\n\n", len(terms))

			// JSON for agent consumption
			if false { // placeholder for --json flag
				data, _ := json.Marshal(terms)
				fmt.Println(string(data))
			}

			return nil
		},
	}
}

// --- Allow command ---

func guardAllowCmd() *cobra.Command {
	var (
		file  string
		match string
		all   bool
		last  bool
	)
	cmd := &cobra.Command{
		Use:   "allow",
		Short: "Allow findings from the last scan",
		Long: `Allow specific findings so they no longer block commits.

Examples:
  same guard allow --file Makefile --match "/Us***/..."   Allow a specific finding
  same guard allow --file Makefile --all                   Allow all findings in a file
  same guard allow --last                                  Allow all findings from last scan`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardAllow(file, match, all, last)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "File path of the finding")
	cmd.Flags().StringVar(&match, "match", "", "Redacted match string to allow")
	cmd.Flags().BoolVar(&all, "all", false, "Allow all findings in the specified file")
	cmd.Flags().BoolVar(&last, "last", false, "Allow all findings from the last scan")
	return cmd
}

func runGuardAllow(file, match string, allowAll, last bool) error {
	vaultPath := config.VaultPath()

	// Load cached last scan
	ls, err := guard.LoadLastScan(vaultPath)
	if err != nil {
		return fmt.Errorf("no cached scan found. Run a scan first (same guard scan)")
	}

	reviewed, err := guard.LoadReviewedTerms(vaultPath)
	if err != nil {
		return err
	}

	var allowed int

	if last {
		// Allow everything from last scan
		for _, v := range ls.Violations {
			reviewed.Add(v.Redacted, string(v.Category), "user-allowed", "user", []string{v.File})
			allowed++
		}
	} else if file != "" && allowAll {
		// Allow all findings in a specific file
		for _, v := range ls.Violations {
			if v.File == file {
				reviewed.Add(v.Redacted, string(v.Category), "user-allowed", "user", []string{v.File})
				allowed++
			}
		}
	} else if file != "" && match != "" {
		// Allow a specific finding by file + redacted match
		for _, v := range ls.Violations {
			if v.File == file && v.Redacted == match {
				reviewed.Add(v.Redacted, string(v.Category), "user-allowed", "user", []string{v.File})
				allowed++
			}
		}
	} else {
		return fmt.Errorf("use --last, or --file with --match or --all")
	}

	if allowed == 0 {
		return fmt.Errorf("no matching findings in the last scan")
	}

	if err := reviewed.Save(vaultPath); err != nil {
		return err
	}

	guard.AppendAudit(vaultPath, guard.AuditEntry{
		Action:  "allow",
		Passed:  true,
		Details: map[string]string{"count": fmt.Sprintf("%d", allowed)},
	})

	fmt.Printf("  %s✓%s Allowed %d finding(s). You can now re-commit.\n", cli.Green, cli.Reset, allowed)
	return nil
}

// --- Settings command ---

func guardSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "View or change guard settings",
		Long: `View or change SAME Guard settings.

Examples:
  same guard settings                     Show current settings
  same guard settings set email off       Disable email scanning
  same guard settings set soft-mode warn  Switch soft blocks to warnings
  same guard settings reset               Reset all settings to defaults`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardSettingsShow()
		},
	}

	cmd.AddCommand(guardSettingsSetCmd())
	cmd.AddCommand(guardSettingsResetCmd())

	return cmd
}

func guardSettingsSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change a guard setting",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardSettingsSet(args[0], args[1])
		},
	}
}

func guardSettingsResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Reset guard settings to defaults",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardSettingsReset()
		},
	}
}

func runGuardSettingsShow() error {
	cfg := guard.LoadGuardConfig()
	vaultPath := config.VaultPath()

	fmt.Printf("\n%sSAME Guard Settings%s\n\n", cli.Bold, cli.Reset)

	// Status
	if cfg.Enabled {
		fmt.Printf("  Status:       %s✓ enabled%s\n", cli.Green, cli.Reset)
	} else {
		fmt.Printf("  Status:       %s✗ disabled%s\n", cli.Red, cli.Reset)
	}

	// PII scan
	if cfg.PII.Enabled {
		fmt.Printf("  PII scan:     %s✓ on%s\n", cli.Green, cli.Reset)
	} else {
		fmt.Printf("  PII scan:     %s✗ off%s\n", cli.Dim, cli.Reset)
	}

	// Blocklist
	if cfg.Blocklist.Enabled {
		blPath := filepath.Join(vaultPath, "_PRIVATE", ".blocklist")
		terms, _ := guard.LoadBlocklist(blPath)
		hard, soft := 0, 0
		for _, t := range terms {
			if t.Tier == guard.TierHard {
				hard++
			} else {
				soft++
			}
		}
		if len(terms) > 0 {
			fmt.Printf("  Blocklist:    %s✓ on%s (%d hard, %d soft terms)\n", cli.Green, cli.Reset, hard, soft)
		} else {
			fmt.Printf("  Blocklist:    %s✓ on%s\n", cli.Green, cli.Reset)
		}
	} else {
		fmt.Printf("  Blocklist:    %s✗ off%s\n", cli.Dim, cli.Reset)
	}

	// Path filter
	if cfg.PathFilter.Enabled {
		fmt.Printf("  Path filter:  %s✓ on%s\n", cli.Green, cli.Reset)
	} else {
		fmt.Printf("  Path filter:  %s✗ off%s\n", cli.Dim, cli.Reset)
	}

	// Soft mode
	fmt.Printf("  Soft blocks:  %s\n", cfg.SoftMode)

	// Push protection
	if cfg.PushProtect.Enabled {
		fmt.Printf("  Push protect: %s✓ on%s (timeout: %ds)\n", cli.Green, cli.Reset, cfg.PushProtect.Timeout)
		// Check if hook is installed
		if hookInstalled, _ := isPushHookInstalled(); hookInstalled {
			fmt.Printf("                %shook installed — run 'same push-allow' before each push%s\n", cli.Dim, cli.Reset)
		} else {
			fmt.Printf("                %s⚠ hook not installed (run: same guard push-install)%s\n", cli.Yellow, cli.Reset)
		}
	} else {
		fmt.Printf("  Push protect: %s✗ off%s %s(recommended for multi-agent workflows)%s\n", cli.Dim, cli.Reset, cli.Dim, cli.Reset)
	}

	// PII Patterns
	fmt.Printf("\n  PII Patterns:\n")
	type patRow struct {
		key  string
		on   bool
		tier string
	}
	rows := []patRow{
		{"email", cfg.PII.Patterns.Email, "hard"},
		{"phone", cfg.PII.Patterns.Phone, "hard"},
		{"ssn", cfg.PII.Patterns.SSN, "hard"},
		{"local_path", cfg.PII.Patterns.LocalPath, "soft"},
		{"api_key", cfg.PII.Patterns.APIKey, "hard"},
		{"aws_key", cfg.PII.Patterns.AWSKey, "hard"},
		{"private_key", cfg.PII.Patterns.PrivateKey, "hard"},
	}
	for _, r := range rows {
		check := fmt.Sprintf("%s✓ on%s", cli.Green, cli.Reset)
		if !r.on {
			check = fmt.Sprintf("%s✗ off%s", cli.Dim, cli.Reset)
		}
		fmt.Printf("    %-14s %s   [%s]\n", r.key, check, r.tier)
	}

	fmt.Printf("\n  Change: same guard settings set <key> on|off\n")
	fmt.Printf("  Keys:   guard, pii, blocklist, path-filter, soft-mode, push-protect, push-timeout,\n")
	fmt.Printf("          email, phone, ssn, local_path, api_key, aws_key, private_key\n\n")

	return nil
}

// isPushHookInstalled checks if the SAME push hook is installed in the current repo.
func isPushHookInstalled() (bool, error) {
	gitRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return false, err
	}
	hookPath := filepath.Join(strings.TrimSpace(string(gitRoot)), ".git", "hooks", "pre-push")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(content), "SAME Guard pre-push"), nil
}

func runGuardSettingsSet(key, value string) error {
	cfg := guard.LoadGuardConfig()
	if err := cfg.SetKey(key, value); err != nil {
		return err
	}
	if err := guard.SaveGuardConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("  %s✓%s Set %s = %s\n", cli.Green, cli.Reset, key, value)

	// Auto-install/uninstall push hook when push-protect is toggled
	if key == "push-protect" || key == "push_protect" {
		boolVal := value == "on" || value == "true" || value == "yes"
		if boolVal {
			fmt.Println("  Installing push protection hook...")
			if err := runGuardPushInstall(true); err != nil {
				fmt.Printf("  %s!%s Could not install hook: %v\n", cli.Yellow, cli.Reset, err)
			}
		} else {
			fmt.Println("  Removing push protection hook...")
			if err := runGuardPushUninstall(); err != nil {
				fmt.Printf("  %s!%s Could not remove hook: %v\n", cli.Yellow, cli.Reset, err)
			}
		}
	}
	return nil
}

func runGuardSettingsReset() error {
	cfg := guard.DefaultGuardConfig()
	if err := guard.SaveGuardConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("  %s✓%s Guard settings reset to defaults.\n", cli.Green, cli.Reset)
	return nil
}

// --- Push protection ---

// pushAllowCmd is a top-level command for creating push tickets.
func pushAllowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push-allow [repo]",
		Short: "Create a one-time push ticket for a repo",
		Long: `Create a one-time push ticket that allows a single git push.

If repo is not specified, auto-detects from the current directory.
The ticket expires after 60 seconds or after one push.

This command works with the pre-push hook installed by 'same guard push-install'.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var repo string
			if len(args) > 0 {
				repo = args[0]
			} else {
				// Auto-detect from current git remote
				out, err := exec.Command("git", "remote", "get-url", "origin").Output()
				if err != nil {
					return fmt.Errorf("not in a git repo or no origin remote. Specify repo name explicitly")
				}
				repo = strings.TrimSpace(string(out))
			}
			return createPushTicket(repo)
		},
	}
}

func createPushTicket(repo string) error {
	cfg := guard.LoadGuardConfig()
	timeout := cfg.PushProtect.Timeout
	if timeout == 0 {
		timeout = 60 // default
	}

	ticketPath, ticketRepo, err := pushTicketPath(repo)
	if err != nil {
		return err
	}

	// Create ticket with timestamp:timeout format
	content := fmt.Sprintf("%d:%d", time.Now().Unix(), timeout)
	if err := os.WriteFile(ticketPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("create ticket: %w", err)
	}

	fmt.Printf("  %s✓%s Push ticket created for %s%s%s\n", cli.Green, cli.Reset, cli.Cyan, ticketRepo, cli.Reset)
	fmt.Printf("  Ticket expires in %d seconds or after one push.\n", timeout)
	return nil
}

func pushTicketPath(repo string) (path string, ticketRepo string, err error) {
	ticketRepo, err = sanitizeRepoTicketName(repo)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("push-ticket-%s", ticketRepo)), ticketRepo, nil
}

func sanitizeRepoTicketName(repo string) (string, error) {
	raw := strings.TrimSpace(repo)
	if raw == "" {
		return "", fmt.Errorf("repo is required")
	}

	normalized := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	normalized = strings.TrimSuffix(normalized, ".git")
	if normalized == "" || normalized == "." || normalized == ".." {
		return "", fmt.Errorf("invalid repo %q", repo)
	}

	var b strings.Builder
	for _, r := range normalized {
		switch {
		case isRepoTicketChar(r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	safe := strings.Trim(b.String(), "._-")
	if safe == "" {
		return "", fmt.Errorf("invalid repo %q", repo)
	}
	if len(safe) > 128 {
		safe = safe[:128]
	}
	return safe, nil
}

func isRepoTicketChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' ||
		r == '_' ||
		r == '.'
}

func guardPushInstallCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "push-install",
		Short: "Install git pre-push hook for push protection",
		Long: `Install a pre-push hook that requires explicit authorization before pushing.

After installation, all pushes require running 'same push-allow' first.
This prevents accidental pushes to the wrong repo, especially when running
multiple agent instances.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardPushInstall(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing hook")
	return cmd
}

func runGuardPushInstall(force bool) error {
	gitRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}
	root := strings.TrimSpace(string(gitRoot))
	hookPath := filepath.Join(root, ".git", "hooks", "pre-push")

	if _, err := os.Stat(hookPath); err == nil && !force {
		return fmt.Errorf("pre-push hook already exists. Use --force to overwrite")
	}

	hook := `#!/bin/sh
# SAME Guard pre-push hook
# Installed by: same guard push-install
# Requires 'same push-allow' before each push.

REMOTE_URL=$(git remote get-url origin 2>/dev/null)
if [ -z "$REMOTE_URL" ]; then
    echo "Warning: Could not determine origin remote. Allowing push."
    exit 0
fi

REPO=$(basename "$REMOTE_URL" .git 2>/dev/null)
[ -z "$REPO" ] && REPO="$REMOTE_URL"

SAFE_REPO=$(printf "%s" "$REMOTE_URL" | tr -c 'A-Za-z0-9_.-' '_' | sed 's/^[._-]*//; s/[._-]*$//')
if [ -z "$SAFE_REPO" ]; then
    SAFE_REPO=$(printf "%s" "$REPO" | tr -c 'A-Za-z0-9_.-' '_' | sed 's/^[._-]*//; s/[._-]*$//')
fi

# Use TMPDIR if set (macOS), fall back to /tmp
TMPBASE="${TMPDIR:-/tmp}"
TICKET="${TMPBASE}/push-ticket-$SAFE_REPO"

if [ ! -f "$TICKET" ]; then
    echo ""
    echo "❌ Push blocked — authorization required"
    echo ""
    echo "   This safety check prevents accidental pushes to the wrong repo"
    echo "   when running multiple AI agents."
    echo ""
    echo "   To authorize this push:"
    echo "     same push-allow"
    echo ""
    echo "   Then run your push command again."
    echo ""
    exit 1
fi

# Parse ticket: format is "timestamp:timeout"
TICKET_CONTENT=$(cat "$TICKET")
TICKET_TIME=$(echo "$TICKET_CONTENT" | cut -d: -f1)
TICKET_TIMEOUT=$(echo "$TICKET_CONTENT" | cut -d: -f2)
[ -z "$TICKET_TIMEOUT" ] && TICKET_TIMEOUT=60

NOW=$(date +%s)
AGE=$((NOW - TICKET_TIME))
if [ "$AGE" -gt "$TICKET_TIMEOUT" ]; then
    rm -f "$TICKET"
    echo ""
    echo "❌ Push ticket expired (>${TICKET_TIMEOUT}s). Run: same push-allow"
    echo ""
    exit 1
fi

# Consume ticket (single-use)
rm -f "$TICKET"

echo "✓ Push authorized for $REPO"
`

	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}
	if err := os.WriteFile(hookPath, []byte(hook), 0o755); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}

	fmt.Printf("  %s✓%s Pre-push hook installed at %s\n", cli.Green, cli.Reset, hookPath)
	fmt.Printf("  All pushes now require: same push-allow\n")
	fmt.Printf("  Bypass with: git push --no-verify (emergency only)\n")
	return nil
}

func guardPushUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push-uninstall",
		Short: "Remove the git pre-push hook",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuardPushUninstall()
		},
	}
}

func runGuardPushUninstall() error {
	gitRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}
	root := strings.TrimSpace(string(gitRoot))
	hookPath := filepath.Join(root, ".git", "hooks", "pre-push")

	content, err := os.ReadFile(hookPath)
	if os.IsNotExist(err) {
		fmt.Println("  No pre-push hook found.")
		return nil
	}
	if err != nil {
		return err
	}

	if !strings.Contains(string(content), "SAME Guard pre-push") {
		return fmt.Errorf("pre-push hook exists but was not installed by SAME. Remove manually if needed")
	}

	if err := os.Remove(hookPath); err != nil {
		return err
	}
	fmt.Printf("  %s✓%s Pre-push hook removed.\n", cli.Green, cli.Reset)
	return nil
}
