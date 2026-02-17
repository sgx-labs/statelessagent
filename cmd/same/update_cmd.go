package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
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

const (
	maxReleaseMetadataSize = 2 * 1024 * 1024   // 2MB
	maxChecksumFileSize    = 512 * 1024        // 512KB
	maxBinaryDownloadSize  = 200 * 1024 * 1024 // 200MB
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseMetadataSize))
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
  3. Verify SHA256 checksum from release manifest
  4. Replace the current binary with the new version

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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseMetadataSize))
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

	downloadURL, ok := findReleaseAssetURL(release.Assets, assetName)
	if !ok {
		return fmt.Errorf("no binary found for %s/%s in release %s", goos, goarch, release.TagName)
	}

	checksumURL, ok := findReleaseAssetURL(release.Assets, "sha256sums.txt")
	if !ok {
		return fmt.Errorf("release %s is missing sha256sums.txt; refusing unverified update", release.TagName)
	}

	fmt.Printf("\n  Fetching checksums...")
	checksumMap, err := fetchSHA256Sums(client, checksumURL)
	if err != nil {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("checksum fetch failed: %w", err)
	}
	expectedSHA256, ok := checksumMap[assetName]
	if !ok {
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("checksum for asset %s not found in sha256sums.txt", assetName)
	}
	fmt.Printf(" %s✓%s\n", cli.Green, cli.Reset)

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

	// Download + hash the file (with a hard size cap).
	hasher := sha256.New()
	limited := &io.LimitedReader{R: dlResp.Body, N: maxBinaryDownloadSize + 1}
	n, err := io.Copy(io.MultiWriter(tmpFile, hasher), limited)
	closeErr := tmpFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("write file: %w", err)
	}
	if n > maxBinaryDownloadSize {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("download too large (> %d MB)", maxBinaryDownloadSize/(1024*1024))
	}

	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualSHA256, expectedSHA256) {
		os.Remove(tmpPath)
		fmt.Printf(" %sfailed%s\n", cli.Red, cli.Reset)
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", assetName, expectedSHA256, actualSHA256)
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

func findReleaseAssetURL(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}, name string) (string, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset.BrowserDownloadURL, true
		}
	}
	return "", false
}

func fetchSHA256Sums(client *http.Client, checksumURL string) (map[string]string, error) {
	resp, err := client.Get(checksumURL)
	if err != nil {
		return nil, fmt.Errorf("download checksum file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checksum endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumFileSize))
	if err != nil {
		return nil, fmt.Errorf("read checksum file: %w", err)
	}

	out := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(fields[0]))
		if len(sum) != 64 || !isHexString(sum) {
			continue
		}
		name := filepath.Base(strings.TrimSpace(fields[len(fields)-1]))
		if name == "" {
			continue
		}
		out[name] = sum
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse checksum file: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("checksum file was empty or malformed")
	}
	return out, nil
}

func isHexString(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
