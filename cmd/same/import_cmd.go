package main

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
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
		return userError("No vault found", "run 'same init' first to set up your vault")
	}

	importsDir := filepath.Join(vaultPath, "imports")
	if err := os.MkdirAll(importsDir, 0o700); err != nil {
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

	// Detect Claude Code memory files (separate from config files)
	claudeMemories := detectClaudeMemories(scanDir)

	if len(toImport) == 0 && len(claudeMemories) == 0 {
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
	var successfulImports []importedFile

	for _, f := range toImport {
		content, err := os.ReadFile(f.sourcePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] reading %s: %v\n", f.sourcePath, err)
			continue
		}

		header := fmt.Sprintf("# Imported from %s\n# Imported: %s\n\n", f.toolName, now)
		output := header + string(content)

		destPath := filepath.Join(importsDir, f.slug)
		if err := os.WriteFile(destPath, []byte(output), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] writing %s: %v\n", destPath, err)
			continue
		}

		fmt.Printf("  %s✓%s %s → imports/%s\n", cli.Green, cli.Reset, f.toolName, f.slug)
		imported++
		successfulImports = append(successfulImports, f)
	}

	// Import Claude Code memory files
	if len(claudeMemories) > 0 {
		memFiles, memImported, err := importClaudeMemories(claudeMemories, importsDir, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] importing Claude memories: %v\n", err)
		}
		imported += memImported
		successfulImports = append(successfulImports, memFiles...)
	}

	if imported == 0 {
		return fmt.Errorf("no files were imported successfully")
	}

	// Index imported files so they're immediately searchable
	database, dbErr := store.Open()
	if dbErr == nil {
		defer database.Close()
		for _, f := range successfulImports {
			destPath := filepath.Join(importsDir, f.slug)
			relPath, relErr := filepath.Rel(vaultPath, destPath)
			if relErr != nil {
				continue
			}
			// Use lite indexing (keyword-only) — no embedding provider needed
			if idxErr := indexer.IndexSingleFileLite(database, destPath, relPath, vaultPath); idxErr != nil {
				fmt.Fprintf(os.Stderr, "  [WARN] indexing %s: %v\n", f.slug, idxErr)
			}
		}
	}

	fmt.Printf("\n  Imported %d file(s) into vault. Run %ssame search%s to verify.\n\n",
		imported, cli.Bold, cli.Reset)

	return nil
}

// importClaudeMemories writes Claude memory files to the vault with SAME frontmatter.
// Returns the count of successfully imported files.
func importClaudeMemories(memories []claudeMemory, importsDir, now string) ([]importedFile, int, error) {
	memDir := filepath.Join(importsDir, "claude-memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		return nil, 0, fmt.Errorf("create claude-memory directory: %w", err)
	}

	// Check for de-duplication — skip already-imported files
	var toImport []claudeMemory
	var skipped int
	for i := range memories {
		destPath := filepath.Join(memDir, memories[i].slug())
		if _, err := os.Stat(destPath); err == nil {
			skipped++
			continue
		}
		toImport = append(toImport, memories[i])
	}

	if len(toImport) == 0 {
		if skipped > 0 {
			fmt.Printf("\n  %sClaude memories: %d already imported, nothing new.%s\n",
				cli.Dim, skipped, cli.Reset)
		}
		return nil, 0, nil
	}

	// Recount by scope for preview (only non-skipped)
	globalCount, projectCount := 0, 0
	for _, m := range toImport {
		switch m.scope {
		case "global":
			globalCount++
		case "project":
			projectCount++
		}
	}

	// Preview
	fmt.Printf("\n  Found %d Claude Code memory file(s):\n", len(toImport))
	if globalCount > 0 {
		fmt.Printf("    %sGlobal (%d):%s\n", cli.Bold, globalCount, cli.Reset)
		for _, m := range toImport {
			if m.scope == "global" {
				fmt.Printf("      %s — %q\n", filepath.Base(m.absPath), m.desc)
			}
		}
	}
	if projectCount > 0 {
		fmt.Printf("    %sProject (%d):%s\n", cli.Bold, projectCount, cli.Reset)
		for _, m := range toImport {
			if m.scope == "project" {
				fmt.Printf("      %s — %q\n", filepath.Base(m.absPath), m.desc)
			}
		}
	}
	if skipped > 0 {
		fmt.Printf("    %s(%d already imported, skipped)%s\n", cli.Dim, skipped, cli.Reset)
	}

	// Confirmation prompt
	fmt.Printf("\n  Import these memories? [Y/n] ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		fmt.Println("  Skipped.")
		return nil, 0, nil
	}

	// Write files
	imported := 0
	var importedFiles []importedFile
	for _, m := range toImport {
		sameType := mapClaudeTypeToSAME(m.memType)
		tags := "claude-memory"
		if m.memType != "" {
			tags += ", " + m.memType
		}
		hash := fileSHA256(m.rawBytes)

		// Build SAME-compatible frontmatter
		var sb strings.Builder
		sb.WriteString("---\n")
		sb.WriteString(fmt.Sprintf("name: %s\n", m.name))
		sb.WriteString(fmt.Sprintf("description: %s\n", m.desc))
		sb.WriteString(fmt.Sprintf("content_type: %s\n", sameType))
		sb.WriteString(fmt.Sprintf("tags: [%s]\n", tags))
		sb.WriteString("trust_state: unknown\n")
		// SECURITY: absolute paths stored here are for imported external files only.
		// The vault is local-only and never transmitted.
		sb.WriteString(fmt.Sprintf("provenance_source: %s\n", m.absPath))
		sb.WriteString(fmt.Sprintf("provenance_hash: %s\n", hash))
		sb.WriteString("---\n\n")
		sb.WriteString(fmt.Sprintf("# Imported from Claude Code (%s)\n", m.scope))
		sb.WriteString(fmt.Sprintf("# Source: %s\n", m.absPath))
		sb.WriteString(fmt.Sprintf("# Imported: %s\n\n", now))
		sb.WriteString(m.body)

		destPath := filepath.Join(memDir, m.slug())
		if err := os.WriteFile(destPath, []byte(sb.String()), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] writing %s: %v\n", destPath, err)
			continue
		}

		slug := m.slug()
		fmt.Printf("  %s✓%s %s → imports/claude-memory/%s\n",
			cli.Green, cli.Reset, filepath.Base(m.absPath), slug)
		imported++
		importedFiles = append(importedFiles, importedFile{
			sourcePath: m.absPath,
			slug:       filepath.Join("claude-memory", slug),
			toolName:   "Claude Code memory",
		})
	}

	if imported > 0 {
		fmt.Printf("\n  %s✓%s %d Claude memories → imports/claude-memory/\n",
			cli.Green, cli.Reset, imported)
		fmt.Printf("    %sProvenance tracked — run 'same health' to check trust state.%s\n",
			cli.Dim, cli.Reset)
	}

	return importedFiles, imported, nil
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

// claudeMemory represents a detected Claude Code memory file.
type claudeMemory struct {
	absPath  string // absolute path to the source file
	scope    string // "global" or "project"
	name     string // from frontmatter or filename
	desc     string // from frontmatter or default
	memType  string // from frontmatter (user, feedback, project, reference)
	body     string // content with frontmatter stripped
	rawBytes []byte // original file bytes for hashing
}

// slug returns the import destination filename.
func (m *claudeMemory) slug() string {
	base := strings.TrimSuffix(filepath.Base(m.absPath), ".md")
	return m.scope + "-" + sanitizeSlug(base)
}

// parseClaudeFrontmatter extracts YAML frontmatter fields from a Claude memory file.
// Claude memory files use --- delimited YAML with name, description, and type fields.
func parseClaudeFrontmatter(content string) (name, desc, memType, body string) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		// No frontmatter — return content as body
		return "", "", "", content
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return "", "", "", content
	}

	// Parse frontmatter fields
	for _, line := range lines[1:endIdx] {
		key, val, ok := parseYAMLField(line)
		if !ok {
			continue
		}
		switch key {
		case "name":
			name = val
		case "description":
			desc = val
		case "type":
			memType = val
		}
	}

	// Body is everything after the closing ---
	body = strings.Join(lines[endIdx+1:], "\n")
	body = strings.TrimLeft(body, "\n")
	return name, desc, memType, body
}

// parseYAMLField extracts a simple key: value pair from a YAML line.
func parseYAMLField(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	// Strip surrounding quotes
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return key, value, true
}

// mapClaudeTypeToSAME maps Claude memory types to SAME content types.
func mapClaudeTypeToSAME(claudeType string) string {
	switch claudeType {
	case "project":
		return "project"
	default:
		return "note"
	}
}

// detectClaudeMemories scans for Claude Code memory files in standard locations.
// Looks in $HOME/.claude/memory/ (global) and <scanDir>/.claude/projects/*/memory/ (project).
// Skips MEMORY.md index files and non-.md files.
func detectClaudeMemories(scanDir string) []claudeMemory {
	var found []claudeMemory
	home, _ := os.UserHomeDir()

	// Global memories: ~/.claude/memory/*.md
	if home != "" {
		globalDir := filepath.Join(home, ".claude", "memory")
		found = append(found, scanClaudeMemoryDir(globalDir, "global")...)
	}

	// Project-scoped memories: <scanDir>/.claude/projects/*/memory/*.md
	projectPattern := filepath.Join(scanDir, ".claude", "projects", "*", "memory")
	matches, _ := filepath.Glob(projectPattern)
	for _, memDir := range matches {
		found = append(found, scanClaudeMemoryDir(memDir, "project")...)
	}

	return found
}

// scanClaudeMemoryDir reads .md files from a single Claude memory directory.
func scanClaudeMemoryDir(dir, scope string) []claudeMemory {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // Directory doesn't exist or unreadable — skip silently
	}

	var found []claudeMemory
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		// Skip MEMORY.md index files
		if strings.EqualFold(name, "MEMORY.md") {
			continue
		}

		absPath := filepath.Join(dir, name)
		content, err := os.ReadFile(absPath)
		if err != nil {
			continue // Permission error — skip with no noise
		}

		fmName, fmDesc, fmType, body := parseClaudeFrontmatter(string(content))

		// Fall back to filename for name
		if fmName == "" {
			fmName = strings.TrimSuffix(name, ".md")
		}
		if fmDesc == "" {
			fmDesc = "Imported from Claude Code"
		}

		found = append(found, claudeMemory{
			absPath:  absPath,
			scope:    scope,
			name:     fmName,
			desc:     fmDesc,
			memType:  fmType,
			body:     body,
			rawBytes: content,
		})
	}
	return found
}

// fileSHA256 returns the hex-encoded SHA256 hash of the given bytes.
func fileSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
