package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
)

func versionCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the SAME version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return runVersionCheck()
			}
			fmt.Printf("same %s\n", Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Check for updates against GitHub releases")
	return cmd
}

func runVersionCheck() error {
	if Version == "dev" {
		fmt.Println("same dev (built from source, no version check)")
		return nil
	}

	// Fetch latest release tag from GitHub API (no auth needed for public repos)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sgx-labs/statelessagent/releases/latest")
	if err != nil {
		// Network error — silently succeed (don't block hooks)
		fmt.Printf("same %s (update check failed: %v)\n", Version, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		// No releases yet or API issue
		fmt.Printf("same %s (no releases found)\n", Version)
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("same %s\n", Version)
		return nil
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		fmt.Printf("same %s\n", Version)
		return nil
	}

	latestVer := strings.TrimPrefix(release.TagName, "v")
	currentVer := strings.TrimPrefix(Version, "v")

	// C3: Use semver comparison instead of string comparison
	if compareSemver(latestVer, currentVer) > 0 {
		// Output as systemMessage for SessionStart hook
		// (hookSpecificOutput is only valid for UserPromptSubmit/PostToolUse)
		fmt.Printf(`{"systemMessage":"\n**SAME update available:** %s → %s\nRun: same update\n"}`, currentVer, latestVer)
		fmt.Println()
	}

	return nil
}

func updateCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update SAME to the latest version",
		Long: `Check for and install the latest version of SAME from GitHub releases.

This command will:
  1. Check the current version against GitHub releases
  2. Download the appropriate binary for your platform
  3. Replace the current binary with the new version

Example:
  same update          Check and install if newer version available
  same update --force  Force reinstall even if already on latest`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Force update even if already on latest version")
	return cmd
}

func runUpdate(force bool) error {
	cli.Header("SAME Update")
	fmt.Println()

	// Get current version
	currentVer := strings.TrimPrefix(Version, "v")
	fmt.Printf("  Current version: %s%s%s\n", cli.Bold, Version, cli.Reset)

	if Version == "dev" && !force {
		fmt.Printf("\n  %s⚠%s  Running dev build (built from source)\n", cli.Yellow, cli.Reset)
		fmt.Println("     Use --force to update anyway, or rebuild from source.")
		return nil
	}

	// Fetch latest release from GitHub
	fmt.Printf("  Checking GitHub releases...")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/sgx-labs/statelessagent/releases/latest")
	if err != nil {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("parse release: %w", err)
	}

	latestVer := strings.TrimPrefix(release.TagName, "v")
	fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)
	fmt.Printf("  Latest version:  %s%s%s\n", cli.Bold, release.TagName, cli.Reset)

	// C3: Use semver comparison instead of string comparison
	cmp := compareSemver(latestVer, currentVer)
	if cmp == 0 && !force {
		fmt.Printf("\n  %s✓%s Already on the latest version.\n\n", cli.Green, cli.Reset)
		return nil
	}

	if cmp <= 0 && !force {
		fmt.Printf("\n  %s✓%s Already up to date.\n\n", cli.Green, cli.Reset)
		return nil
	}

	// Determine the asset to download
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	var assetName string
	switch {
	case goos == "darwin" && goarch == "arm64":
		assetName = "same-darwin-arm64"
	case goos == "darwin" && goarch == "amd64":
		assetName = "same-darwin-amd64"
	case goos == "linux" && goarch == "amd64":
		assetName = "same-linux-amd64"
	case goos == "linux" && goarch == "arm64":
		assetName = "same-linux-arm64"
	case goos == "windows" && goarch == "amd64":
		assetName = "same-windows-amd64.exe"
	default:
		return fmt.Errorf("unsupported platform: %s/%s", goos, goarch)
	}

	// Find the download URL
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no binary found for %s/%s in release %s", goos, goarch, release.TagName)
	}

	fmt.Printf("\n  Downloading %s...", assetName)

	// Download to temp file
	dlResp, err := client.Get(downloadURL)
	if err != nil {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("download: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("download returned %d", dlResp.StatusCode)
	}

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// Create temp file in same directory (for atomic rename)
	tmpFile, err := os.CreateTemp(filepath.Dir(execPath), "same-update-*")
	if err != nil {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Download the file
	_, err = io.Copy(tmpFile, dlResp.Body)
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("write file: %w", err)
	}

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("chmod: %w", err)
	}

	fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)

	// Replace the binary
	fmt.Printf("  Installing...")

	// On Windows, we need to rename the old binary first
	if goos == "windows" {
		oldPath := execPath + ".old"
		os.Remove(oldPath) // ignore error
		if err := os.Rename(execPath, oldPath); err != nil {
			os.Remove(tmpPath)
			fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
			return fmt.Errorf("backup old binary: %w", err)
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("install: %w", err)
	}

	fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)

	// Success message
	fmt.Println()
	fmt.Printf("  %s✓%s Updated to %s%s%s\n", cli.Green, cli.Reset, cli.Bold, release.TagName, cli.Reset)
	fmt.Println()
	fmt.Printf("  Run %ssame doctor%s to verify.\n", cli.Bold, cli.Reset)

	cli.Footer()
	return nil
}
