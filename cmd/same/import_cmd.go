package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
)

// knownConfigFiles lists AI tool configuration files to auto-detect.
var knownConfigFiles = []struct {
	path     string // relative path within project
	slug     string // output filename under imports/
	toolName string // human-readable tool name
}{
	{"CLAUDE.md", "claude-md.md", "Claude Code"},
	{".cursorrules", "cursorrules.md", "Cursor"},
	{".windsurfrules", "windsurfrules.md", "Windsurf"},
	{"AGENTS.md", "agents-md.md", "Codex CLI"},
	{".github/copilot-instructions.md", "copilot-instructions.md", "GitHub Copilot"},
}

func importCmd() *cobra.Command {
	var (
		file    string
		all     bool
		scanDir string
	)

	cmd := &cobra.Command{
		Use:   "import [path]",
		Short: "Import AI config files (CLAUDE.md, .cursorrules, etc.) into your vault",
		Long: `Import existing AI tool configuration files into your SAME vault as notes.

This makes your project context portable across Claude Code, Cursor,
Windsurf, Codex CLI, and GitHub Copilot.

Auto-detected files:
  CLAUDE.md                          Claude Code instructions
  .cursorrules                       Cursor rules
  .windsurfrules                     Windsurf rules
  AGENTS.md                          Codex CLI agents file
  .github/copilot-instructions.md    GitHub Copilot instructions

Examples:
  same import                  Auto-detect config files in current directory
  same import /path/to/project Scan a specific project directory
  same import --file rules.md  Import a specific file
  same import --all            Scan recursively for all known config files`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				scanDir = args[0]
			}
			if scanDir == "" {
				var err error
				scanDir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
			}
			return runImport(scanDir, file, all)
		},
	}

	cmd.Flags().StringVar(&file, "file", "", "Import a specific file")
	cmd.Flags().BoolVar(&all, "all", false, "Scan recursively for all known config files")

	return cmd
}

// importedFile tracks a file that was imported.
type importedFile struct {
	sourcePath string
	slug       string
	toolName   string
}

func runImport(scanDir, explicitFile string, recursive bool) error {
	vaultPath := config.VaultPath()
	if vaultPath == "" {
		return userError("No vault found", "Run 'same init' first to set up your vault")
	}

	importsDir := filepath.Join(vaultPath, "imports")
	if err := os.MkdirAll(importsDir, 0o755); err != nil {
		return fmt.Errorf("create imports directory: %w", err)
	}

	var toImport []importedFile

	// If --file is specified, import just that file
	if explicitFile != "" {
		absPath := explicitFile
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(scanDir, absPath)
		}
		if _, err := os.Stat(absPath); err != nil {
			return userError(
				fmt.Sprintf("File not found: %s", explicitFile),
				"Check the file path and try again",
			)
		}
		base := filepath.Base(explicitFile)
		slug := sanitizeSlug(base)
		toImport = append(toImport, importedFile{
			sourcePath: absPath,
			slug:       slug,
			toolName:   base,
		})
	} else {
		// Auto-detect known config files
		toImport = detectConfigFiles(scanDir, recursive)
	}

	if len(toImport) == 0 {
		fmt.Println()
		fmt.Printf("  No AI config files found in %s\n", scanDir)
		fmt.Printf("  %sLooking for: CLAUDE.md, .cursorrules, .windsurfrules, AGENTS.md, .github/copilot-instructions.md%s\n",
			cli.Dim, cli.Reset)
		fmt.Printf("\n  %sUse --file to import a specific file%s\n", cli.Dim, cli.Reset)
		return nil
	}

	fmt.Println()
	fmt.Printf("  %sImporting AI config files into vault%s\n\n", cli.Bold, cli.Reset)

	imported := 0
	now := time.Now().Format("2006-01-02")

	for _, f := range toImport {
		content, err := os.ReadFile(f.sourcePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] reading %s: %v\n", f.sourcePath, err)
			continue
		}

		header := fmt.Sprintf("# Imported from %s\n# Imported: %s\n\n", f.toolName, now)
		output := header + string(content)

		destPath := filepath.Join(importsDir, f.slug)
		if err := os.WriteFile(destPath, []byte(output), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] writing %s: %v\n", destPath, err)
			continue
		}

		fmt.Printf("  %s✓%s %s → imports/%s\n", cli.Green, cli.Reset, f.toolName, f.slug)
		imported++
	}

	if imported == 0 {
		return fmt.Errorf("no files were imported successfully")
	}

	fmt.Printf("\n  Imported %d file(s) into vault. Run %ssame search%s to verify.\n\n",
		imported, cli.Bold, cli.Reset)

	return nil
}

// detectConfigFiles finds known AI config files in the given directory.
func detectConfigFiles(dir string, recursive bool) []importedFile {
	var found []importedFile

	if recursive {
		// Walk directory tree looking for known files
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				name := info.Name()
				// Skip hidden dirs (except .github), node_modules, vendor, etc.
				if strings.HasPrefix(name, ".") && name != ".github" && name != "." {
					return filepath.SkipDir
				}
				if name == "node_modules" || name == "vendor" || name == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}

			rel, _ := filepath.Rel(dir, path)
			rel = filepath.ToSlash(rel)

			for _, kf := range knownConfigFiles {
				if rel == kf.path {
					found = append(found, importedFile{
						sourcePath: path,
						slug:       kf.slug,
						toolName:   kf.path,
					})
				}
			}
			return nil
		})
	} else {
		// Check only the top-level directory
		for _, kf := range knownConfigFiles {
			path := filepath.Join(dir, kf.path)
			if _, err := os.Stat(path); err == nil {
				found = append(found, importedFile{
					sourcePath: path,
					slug:       kf.slug,
					toolName:   kf.path,
				})
			}
		}
	}

	return found
}

// sanitizeSlug converts a filename into a safe slug for the imports directory.
func sanitizeSlug(name string) string {
	// Ensure it ends with .md
	if !strings.HasSuffix(name, ".md") {
		name = name + ".md"
	}
	// Replace spaces and special chars
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ToLower(name)
	return name
}
