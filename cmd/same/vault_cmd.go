package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/guard"
	"github.com/sgx-labs/statelessagent/internal/store"
	"github.com/sgx-labs/statelessagent/internal/watcher"
)

func watchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Auto-update the index when notes change",
		Long:  "Monitor the vault filesystem for markdown file changes. Automatically reindexes modified, created, or deleted notes with a 2-second debounce.",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			return watcher.Watch(db)
		},
	}
}

func vaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage multiple note collections",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered vaults",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := config.LoadRegistry()
			if len(reg.Vaults) == 0 {
				fmt.Println("No vaults registered. Use 'same vault add <name> <path>' to register one.")
				fmt.Printf("Current vault (auto-detected): %s\n", config.VaultPath())
				return nil
			}
			fmt.Println("Registered vaults:")
			for name, path := range reg.Vaults {
				marker := "  "
				if name == reg.Default {
					marker = "* "
				}
				fmt.Printf("  %s%-15s %s\n", marker, name, path)
			}
			if reg.Default != "" {
				fmt.Printf("\n  (* = default)\n")
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "add [name] [path]",
		Short: "Register a vault",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := args[0], args[1]
			if err := validateAlias(name); err != nil {
				return fmt.Errorf("invalid vault name: %w", err)
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			if info, err := os.Stat(absPath); err != nil || !info.IsDir() {
				return fmt.Errorf("path does not exist or is not a directory: %s", cli.ShortenHome(absPath))
			}
			reg := config.LoadRegistry()
			reg.Vaults[name] = absPath
			if len(reg.Vaults) == 1 {
				reg.Default = name
			}
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Registered vault %q at %s\n", name, absPath)
			if reg.Default == name {
				fmt.Println("Set as default vault.")
			}
			if len(reg.Vaults) >= 2 {
				fmt.Printf("\n  %sTry: same search --all \"query\" to search across vaults%s\n", cli.Dim, cli.Reset)
			} else {
				fmt.Printf("\n  %sTip: register a second vault to enable cross-vault search%s\n", cli.Dim, cli.Reset)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "remove [name]",
		Short: "Unregister a vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			reg := config.LoadRegistry()
			if _, ok := reg.Vaults[name]; !ok {
				return fmt.Errorf("vault %q not registered", name)
			}
			delete(reg.Vaults, name)
			if reg.Default == name {
				reg.Default = ""
			}
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Removed vault %q\n", name)
			if reg.Default == "" && len(reg.Vaults) > 0 {
				fmt.Printf("  %sNo default vault. Use 'same vault default <name>' to set one.%s\n", cli.Dim, cli.Reset)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "default [name]",
		Short: "Set the default vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			reg := config.LoadRegistry()
			if _, ok := reg.Vaults[name]; !ok {
				return fmt.Errorf("vault %q not registered", name)
			}
			reg.Default = name
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Default vault set to %q (%s)\n", name, reg.Vaults[name])
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "rename [old-name] [new-name]",
		Short: "Rename a registered vault",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			if err := validateAlias(newName); err != nil {
				return fmt.Errorf("invalid new name: %w", err)
			}
			reg := config.LoadRegistry()
			path, ok := reg.Vaults[oldName]
			if !ok {
				return fmt.Errorf("vault %q not registered", oldName)
			}
			if _, exists := reg.Vaults[newName]; exists {
				return fmt.Errorf("vault %q already exists", newName)
			}
			delete(reg.Vaults, oldName)
			reg.Vaults[newName] = path
			if reg.Default == oldName {
				reg.Default = newName
			}
			if err := reg.Save(); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
			fmt.Printf("Renamed vault %q → %q\n", oldName, newName)
			return nil
		},
	})

	var dryRun bool
	feedCmd := &cobra.Command{
		Use:   "feed [source] [target]",
		Short: "Copy notes from one vault to another with PII guard",
		Long: `One-way note propagation between vaults. Reads notes from the source
vault and copies them to the target vault. Each note is scanned by the PII
guard before copying — notes with detected PII violations are blocked.

Source and target must be registered vault aliases. Notes are copied into a
'fed/<source-alias>/' subdirectory in the target vault to avoid collisions.

Private notes (_PRIVATE/), hidden files, and symlinks are never copied.

Example:
  same vault feed dev marketing
  same vault feed dev marketing --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVaultFeed(args[0], args[1], dryRun)
		},
	}
	feedCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be copied without copying")
	cmd.AddCommand(feedCmd)

	return cmd
}

// maxFeedFileSize is the maximum size of a single file that vault feed will copy.
// Prevents memory exhaustion from huge files.
const maxFeedFileSize = 10 * 1024 * 1024 // 10MB

// maxAliasLen is the maximum length of a vault alias name.
const maxAliasLen = 64

// validAlias matches alphanumeric, hyphens, and underscores.
var validAlias = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// validateAlias checks that a vault alias name is safe for use as a
// registry key and filesystem directory name.
func validateAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("alias cannot be empty")
	}
	if len(alias) > maxAliasLen {
		return fmt.Errorf("alias too long (%d chars, max %d)", len(alias), maxAliasLen)
	}
	if !validAlias.MatchString(alias) {
		return fmt.Errorf("alias must be alphanumeric with hyphens or underscores (got %q)", alias)
	}
	return nil
}

// sanitizeAlias strips path separators and traversal characters from a vault alias
// so it is safe to use as a filesystem directory name.
func sanitizeAlias(alias string) string {
	// Replace any path-dangerous characters with underscores
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', '.', '\x00':
			return '_'
		default:
			return r
		}
	}, alias)
	// Strip leading dots/underscores to prevent hidden directories
	clean = strings.TrimLeft(clean, "_.")
	if clean == "" {
		clean = "unnamed"
	}
	return clean
}

// safeFeedPath validates that a note path is safe to use for file operations.
// Returns the cleaned path or empty string if the path is dangerous.
func safeFeedPath(notePath string) string {
	// SECURITY: reject null bytes
	if strings.ContainsRune(notePath, 0) {
		return ""
	}
	// SECURITY: normalize backslashes before any checks so traversal patterns
	// like "..\" are caught on all platforms.
	normalized := strings.ReplaceAll(notePath, "\\", "/")
	// SECURITY: reject Windows drive-letter absolute paths (e.g. C:/...)
	if len(normalized) >= 3 {
		ch := normalized[0]
		if ((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')) && normalized[1] == ':' && normalized[2] == '/' {
			return ""
		}
	}
	// Clean the path to resolve any ../
	clean := filepath.Clean(filepath.FromSlash(normalized))
	// Convert back to forward slashes for consistency
	cleanSlash := filepath.ToSlash(clean)
	// Reject absolute paths
	if filepath.IsAbs(clean) {
		return ""
	}
	// Reject paths that escape via ..
	if strings.HasPrefix(cleanSlash, "..") || strings.Contains(cleanSlash, "/../") {
		return ""
	}
	// Reject _PRIVATE (case-insensitive)
	upper := strings.ToUpper(cleanSlash)
	if strings.HasPrefix(upper, "_PRIVATE/") || upper == "_PRIVATE" {
		return ""
	}
	// Reject dot-prefixed files/dirs (hidden files, .same/, .git/, etc.)
	if strings.HasPrefix(cleanSlash, ".") {
		return ""
	}
	// Reject paths containing dot-directory components
	for _, part := range strings.Split(cleanSlash, "/") {
		if strings.HasPrefix(part, ".") {
			return ""
		}
	}
	return clean
}

func runVaultFeed(sourceAlias, targetAlias string, dryRun bool) error {
	reg := config.LoadRegistry()

	// SECURITY: Prevent self-feed (would create recursive copies)
	sourcePath := reg.ResolveVault(sourceAlias)
	if sourcePath == "" {
		return fmt.Errorf("source vault %q not found in registry", sourceAlias)
	}
	targetPath := reg.ResolveVault(targetAlias)
	if targetPath == "" {
		return fmt.Errorf("target vault %q not found in registry", targetAlias)
	}

	absSource, _ := filepath.Abs(sourcePath)
	absTarget, _ := filepath.Abs(targetPath)
	if absSource == absTarget {
		return fmt.Errorf("source and target cannot be the same vault")
	}

	sourceDB, err := store.OpenPath(filepath.Join(sourcePath, ".same", "data", "vault.db"))
	if err != nil {
		return fmt.Errorf("open source vault database: %w", err)
	}
	defer sourceDB.Close()

	// Get all notes from source (chunk_id=0 only, i.e. one per note)
	notes, err := sourceDB.AllNotes()
	if err != nil {
		return fmt.Errorf("read source notes: %w", err)
	}

	if len(notes) == 0 {
		fmt.Println("Source vault has no notes to feed.")
		return nil
	}

	// Set up PII guard scanner for the target vault
	scanner, err := guard.NewScanner(targetPath)
	if err != nil {
		// Guard not configured — continue without PII checks but warn
		fmt.Fprintf(os.Stderr, "Warning: PII guard not available, proceeding without PII checks\n")
		scanner = nil
	}

	// SECURITY: Sanitize alias before using in filesystem path
	safeDirName := sanitizeAlias(sourceAlias)
	destDir := filepath.Join(targetPath, "fed", safeDirName)

	// SECURITY: Verify destDir is within targetPath
	absDestDir, err := filepath.Abs(destDir)
	if err != nil || !pathWithinBase(absTarget, absDestDir) {
		return fmt.Errorf("invalid feed destination")
	}

	if !dryRun {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return fmt.Errorf("create feed directory: %w", err)
		}
	}

	var copied, skipped, blocked int
	for _, note := range notes {
		// SECURITY: Validate note path for traversal attacks
		cleanPath := safeFeedPath(note.Path)
		if cleanPath == "" {
			skipped++
			continue
		}

		// SECURITY: Build source file path and verify it stays within source vault
		srcFile := filepath.Join(sourcePath, cleanPath)
		absSrc, err := filepath.Abs(srcFile)
		if err != nil || !pathWithinBase(absSource, absSrc) {
			skipped++
			continue
		}

		// SECURITY: Reject symlinks in source (could point outside vault)
		srcInfo, err := os.Lstat(srcFile)
		if err != nil {
			skipped++
			continue
		}
		if srcInfo.Mode()&os.ModeSymlink != 0 {
			skipped++
			continue
		}

		// SECURITY: Enforce file size limit
		if srcInfo.Size() > maxFeedFileSize {
			fmt.Fprintf(os.Stderr, "  SKIPPED (too large): %s (%d bytes, max %d)\n",
				cleanPath, srcInfo.Size(), maxFeedFileSize)
			skipped++
			continue
		}

		content, err := os.ReadFile(srcFile)
		if err != nil {
			skipped++
			continue
		}

		// Run PII guard on content
		if scanner != nil {
			// Create a new content reader closure for each file to avoid race
			fileContent := content
			contentReader := func(file string) ([]byte, error) {
				return fileContent, nil
			}
			scanner.ContentReader = contentReader
			result, scanErr := scanner.ScanFiles([]string{cleanPath})
			if scanErr == nil && result.HasBlocking() {
				blocked++
				fmt.Fprintf(os.Stderr, "  BLOCKED: %s (%d PII violation(s))\n",
					cleanPath, len(result.Violations))
				continue
			}
		}

		// SECURITY: Build dest file path and verify it stays within destDir
		destFile := filepath.Join(destDir, cleanPath)
		absDest, err := filepath.Abs(destFile)
		if err != nil || !pathWithinBase(absDestDir, absDest) {
			skipped++
			continue
		}

		if dryRun {
			fmt.Printf("  would copy: %s\n", cleanPath)
			copied++
			continue
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destFile), 0o755); err != nil {
			skipped++
			continue
		}

		// Only copy if destination doesn't exist or source is newer
		if destInfo, err := os.Stat(destFile); err == nil {
			if !srcInfo.ModTime().After(destInfo.ModTime()) {
				skipped++
				continue
			}
		}

		if err := os.WriteFile(destFile, content, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR writing %s\n", cleanPath)
			skipped++
			continue
		}
		copied++
	}

	if dryRun {
		fmt.Printf("\nDry run: %d note(s) would be copied, %d skipped, %d blocked by PII guard\n",
			copied, skipped, blocked)
	} else {
		fmt.Printf("\nFed %d note(s) from %q to %q (skipped %d, blocked %d)\n",
			copied, sourceAlias, targetAlias, skipped, blocked)
		if copied > 0 {
			fmt.Println("Run 'same reindex' in the target vault to index the new notes.")
		}
	}

	return nil
}

func pathWithinBase(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}
