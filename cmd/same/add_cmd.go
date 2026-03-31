package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func addCmd() *cobra.Command {
	var (
		notePath    string
		tags        string
		contentType string
		domain      string
		sources     string
		stdin       bool
	)
	cmd := &cobra.Command{
		Use:   "add [text]",
		Short: "Create a note from text and index it",
		Long: `Create a new note in your vault from the command line.

The note is written as a markdown file with frontmatter and immediately
indexed for search. No need to create a file and run reindex separately.

Examples:
  same add "JWT auth decision: use RS256 for service-to-service"
  same add "Switched from React to Vue" --tags frontend,decision
  same add "API rate limiting: 100 req/min per key" --type decision --domain api
  same add "Deploy checklist" --path ops/deploy-checklist.md
  echo "Note from pipe" | same add --stdin`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var text string
			if stdin {
				data, err := io.ReadAll(bufio.NewReader(os.Stdin))
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				text = strings.TrimSpace(string(data))
			} else if len(args) == 1 {
				text = strings.TrimSpace(args[0])
			} else {
				return userError("No text provided", "Pass text as an argument or use --stdin")
			}

			if text == "" {
				return userError("Empty note text", "Provide some content for the note")
			}

			var tagList []string
			if tags != "" {
				for _, t := range strings.Split(tags, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						tagList = append(tagList, t)
					}
				}
			}

			var sourceList []string
			if sources != "" {
				for _, s := range strings.Split(sources, ",") {
					s = strings.TrimSpace(s)
					if s != "" {
						sourceList = append(sourceList, s)
					}
				}
			}

			return runAdd(text, notePath, tagList, contentType, domain, sourceList)
		},
	}
	cmd.Flags().StringVar(&notePath, "path", "", "File path within the vault (auto-generated if not set)")
	cmd.Flags().StringVar(&tags, "tags", "", "Comma-separated tags for frontmatter")
	cmd.Flags().StringVar(&contentType, "type", "", "Content type (decision, note, research, handoff)")
	cmd.Flags().StringVar(&domain, "domain", "", "Domain for the note")
	cmd.Flags().StringVar(&sources, "sources", "", "Comma-separated source file paths for provenance tracking")
	cmd.Flags().BoolVar(&stdin, "stdin", false, "Read note text from stdin")
	return cmd
}

func runAdd(text, notePath string, tags []string, contentType, domain string, sources []string) error {
	vaultPath := config.VaultPath()
	if vaultPath == "" {
		return userError("No vault found", "run 'same init' first to set up your vault")
	}

	db, err := store.Open()
	if err != nil {
		return userError("No SAME vault found", "Run 'same init' first.")
	}
	defer db.Close()

	// Auto-generate path if not provided
	if notePath == "" {
		notePath = generateNotePath(text)
	}

	// Ensure .md extension
	if !strings.HasSuffix(strings.ToLower(notePath), ".md") {
		notePath = notePath + ".md"
	}

	// SECURITY: reject absolute paths
	if filepath.IsAbs(notePath) {
		return fmt.Errorf("path must be relative to the vault, not absolute: %s", notePath)
	}

	// SECURITY: resolve and verify the path stays inside the vault.
	// Canonicalize both sides with EvalSymlinks so that macOS /var → /private/var
	// (and similar symlinked roots) compare correctly.
	vaultAbs, _ := filepath.Abs(vaultPath)
	realVault, evalErr := filepath.EvalSymlinks(vaultAbs)
	if evalErr != nil {
		realVault = vaultAbs
	}

	fullPath := filepath.Join(vaultAbs, notePath)
	resolved, _ := filepath.Abs(fullPath)

	// Find nearest existing ancestor for symlink check.
	// The target file may not exist yet, so walk up to the nearest existing
	// directory and canonicalize that to detect symlink escapes.
	checkPath := resolved
	for checkPath != "/" && checkPath != "." {
		if _, err := os.Lstat(checkPath); err == nil {
			break
		}
		checkPath = filepath.Dir(checkPath)
	}
	realResolved, err := filepath.EvalSymlinks(checkPath)
	if err != nil {
		realResolved = checkPath
	}

	// Check containment with separator boundary
	if !strings.HasPrefix(realResolved, realVault+string(filepath.Separator)) && realResolved != realVault {
		return fmt.Errorf("path resolves outside the vault boundary: %s", notePath)
	}

	// SECURITY: block writes to internal/hidden paths (same policy as MCP safeVaultPath).
	// Clean the path FIRST to prevent bypass via normalization (e.g., "notes/../.same/test.md").
	cleaned := filepath.Clean(notePath)
	segments := strings.Split(cleaned, string(filepath.Separator))
	for _, seg := range segments {
		lowerSeg := strings.ToLower(seg)
		if strings.HasPrefix(lowerSeg, ".") || lowerSeg == "_private" {
			return fmt.Errorf("cannot write to internal path: %s", notePath)
		}
	}

	// Build the markdown content with frontmatter (uses YAML marshaler for safety)
	content := buildNoteContent(text, tags, contentType, domain)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Don't overwrite existing files
	if _, err := os.Stat(fullPath); err == nil {
		return userError(
			fmt.Sprintf("File already exists: %s", notePath),
			"Choose a different --path or remove the existing file first",
		)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	// Index the file
	relPath := filepath.ToSlash(notePath)

	// Try full indexing with embeddings first, fall back to lite
	embedClient, embedErr := newEmbedProvider()
	var chunkCount int
	if embedErr == nil {
		err = indexer.IndexSingleFile(db, fullPath, relPath, vaultPath, embedClient)
	} else {
		err = indexer.IndexSingleFileLite(db, fullPath, relPath, vaultPath)
	}
	if err != nil {
		// Non-fatal: file was written, just not indexed yet
		fmt.Printf("  %s\u2713%s Saved: %s\n", cli.Green, cli.Reset, notePath)
		fmt.Printf("  %s[WARN] Index failed: %v — run 'same reindex' to fix%s\n", cli.Yellow, err, cli.Reset)
		return nil
	}

	// Record provenance sources if provided
	if len(sources) > 0 {
		for _, src := range sources {
			if srcErr := db.RecordSource(relPath, src, "file", ""); srcErr != nil {
				fmt.Fprintf(os.Stderr, "  [WARN] failed to record source %s: %v\n", src, srcErr)
			}
		}
	}

	// Count chunks for the confirmation message
	notes, _ := db.GetNoteByPath(relPath)
	chunkCount = len(notes)
	if chunkCount == 0 {
		chunkCount = 1
	}

	sourceSuffix := ""
	if len(sources) > 0 {
		sourceSuffix = fmt.Sprintf(", %d source%s", len(sources), pluralS(len(sources)))
	}
	fmt.Printf("  %s\u2713%s Added: %s (%d chunk%s, indexed%s)\n",
		cli.Green, cli.Reset, notePath, chunkCount, pluralS(chunkCount), sourceSuffix)
	return nil
}

// generateNotePath creates a default path like "notes/2026-03-18-note.md"
// based on the current date. If a file with that name already exists,
// appends a numeric suffix.
func generateNotePath(text string) string {
	date := time.Now().Format("2006-01-02")

	// Try to derive a slug from the first few words
	slug := slugify(text)
	if slug != "" {
		return fmt.Sprintf("notes/%s-%s.md", date, slug)
	}

	return fmt.Sprintf("notes/%s-note.md", date)
}

// slugify turns the first ~5 words of text into a filename-safe slug.
func slugify(text string) string {
	// Take just the first line
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 5 {
		words = words[:5]
	}

	slug := strings.Join(words, "-")
	slug = strings.ToLower(slug)

	// Keep only alphanumeric and hyphens
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	slug = b.String()

	// Trim trailing hyphens and limit length
	slug = strings.Trim(slug, "-")
	if len(slug) > 50 {
		slug = slug[:50]
		slug = strings.TrimRight(slug, "-")
	}

	return slug
}

// noteFrontmatter holds typed frontmatter for safe YAML marshaling.
// Using a proper marshaler prevents YAML injection via crafted values.
type noteFrontmatter struct {
	Tags        []string `yaml:"tags,omitempty"`
	ContentType string   `yaml:"content_type,omitempty"`
	Domain      string   `yaml:"domain,omitempty"`
}

// buildNoteContent assembles a markdown file with YAML frontmatter.
// Uses yaml.Marshal to prevent YAML injection.
func buildNoteContent(text string, tags []string, contentType, domain string) string {
	var b strings.Builder

	fm := noteFrontmatter{
		Tags:        tags,
		ContentType: contentType,
		Domain:      domain,
	}

	hasFrontmatter := len(tags) > 0 || contentType != "" || domain != ""
	if hasFrontmatter {
		fmBytes, err := yaml.Marshal(fm)
		if err == nil {
			b.WriteString("---\n")
			b.Write(fmBytes)
			b.WriteString("---\n\n")
		}
	}

	b.WriteString(text)
	b.WriteString("\n")

	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
