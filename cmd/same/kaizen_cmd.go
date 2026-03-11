package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func kaizenCmd() *cobra.Command {
	var agent string
	var status string
	var showAll bool

	cmd := &cobra.Command{
		Use:   "kaizen [description]",
		Short: "Log and review friction, bugs, and improvement ideas",
		Long: `A lightweight continuous improvement system.

Log friction points, bugs, and improvement ideas as you work. Provenance
tracking automatically detects when source files change, hinting that
issues may have been addressed.

Modes:
  same kaizen                          List open kaizen items
  same kaizen "description"            Log a new item
  same kaizen --all                    Show all statuses
  same kaizen --status addressed       Filter by status

Flags:
  --agent <name>       Attribution for who observed the issue
  --status <status>    Filter (list) or set (add) status (default: open)
  --all                Show all statuses in list mode`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return runKaizenAdd(args[0], agent, status)
			}
			return runKaizenList(status, showAll)
		},
	}

	cmd.Flags().StringVar(&agent, "agent", "", "Attribution (who observed the issue)")
	cmd.Flags().StringVar(&status, "status", "open", "Filter for list mode, set for add mode")
	cmd.Flags().BoolVar(&showAll, "all", false, "Show all statuses in list mode")

	return cmd
}

// kaizenItem holds a kaizen record from the database.
type kaizenItem struct {
	Path       string
	Title      string
	Modified   float64
	Status     string
	TrustState sql.NullString
}

func runKaizenList(status string, showAll bool) error {
	db, err := store.Open()
	if err != nil {
		return dbOpenError(err)
	}
	defer db.Close()

	conn := db.Conn()
	margin := "  "

	// Query kaizen notes — extract status from frontmatter in Go
	query := `SELECT path, title, modified, text, trust_state
		FROM vault_notes
		WHERE chunk_id = 0
		AND content_type = 'kaizen'
		AND COALESCE(suppressed, 0) = 0
		ORDER BY modified DESC`

	rows, err := conn.Query(query)
	if err != nil {
		return fmt.Errorf("query kaizen notes: %w", err)
	}
	defer rows.Close()

	var items []kaizenItem
	for rows.Next() {
		var item kaizenItem
		var text string
		if err := rows.Scan(&item.Path, &item.Title, &item.Modified, &text, &item.TrustState); err != nil {
			continue
		}
		item.Status = extractFrontmatterStatus(text)
		items = append(items, item)
	}

	if len(items) == 0 {
		fmt.Printf("\n%sNo kaizen items found. Log one with:%s\n", margin, "")
		fmt.Printf("%s  %ssame kaizen \"description of friction or idea\"%s\n\n", margin, cli.Cyan, cli.Reset)
		return nil
	}

	// Count by status
	openCount := 0
	addressedCount := 0
	wontfixCount := 0
	for _, item := range items {
		switch item.Status {
		case "addressed":
			addressedCount++
		case "wontfix":
			wontfixCount++
		default:
			openCount++
		}
	}

	total := len(items)

	// Summary line
	fmt.Println()
	fmt.Printf("%s%s%d items%s", margin, cli.Bold, total, cli.Reset)
	parts := []string{}
	if addressedCount > 0 {
		parts = append(parts, fmt.Sprintf("%d addressed", addressedCount))
	}
	if openCount > 0 {
		parts = append(parts, fmt.Sprintf("%d open", openCount))
	}
	if wontfixCount > 0 {
		parts = append(parts, fmt.Sprintf("%d wontfix", wontfixCount))
	}
	if len(parts) > 0 {
		fmt.Printf(" %s· %s%s", cli.Dim, strings.Join(parts, " · "), cli.Reset)
	}
	fmt.Println()

	// Filter items
	var filtered []kaizenItem
	for _, item := range items {
		if showAll {
			filtered = append(filtered, item)
		} else if item.Status == status {
			filtered = append(filtered, item)
		}
	}

	if len(filtered) == 0 {
		fmt.Printf("\n%s%sNo items with status '%s'. Use --all to see everything.%s\n\n",
			margin, cli.Dim, status, cli.Reset)
		return nil
	}

	// Display items grouped by status
	currentStatus := ""
	for _, item := range filtered {
		if item.Status != currentStatus {
			currentStatus = item.Status
			cli.Section(capitalize(currentStatus))
		}

		icon := statusIcon(item.Status)
		ago := time.Since(time.Unix(int64(item.Modified), 0))
		agoStr := formatDuration(ago) + " ago"

		// Title display
		title := item.Title
		if title == "" {
			title = filepath.Base(item.Path)
		}

		fmt.Printf("%s%s %s%s%s  %s%s%s",
			margin, icon, cli.Bold, title, cli.Reset,
			cli.Dim, agoStr, cli.Reset)

		// Source file hint
		if item.Path != "" {
			fmt.Printf("  %s%s%s", cli.Dim, item.Path, cli.Reset)
		}
		fmt.Println()

		// Provenance hint
		if item.TrustState.Valid && item.TrustState.String == "stale" {
			fmt.Printf("%s  %s(source changed — may be addressed)%s\n",
				margin, cli.Yellow, cli.Reset)
		}
	}

	fmt.Println()
	return nil
}

func runKaizenAdd(description, agent, status string) error {
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Errorf("description cannot be empty")
	}

	vaultPath := config.VaultPath()
	if vaultPath == "" {
		return userError("No vault configured", "Run 'same init' to set up your vault.")
	}

	// Build filename
	date := time.Now().Format("2006-01-02")
	slug := kaizenSlugify(description)
	if slug == "" {
		slug = "item"
	}
	filename := fmt.Sprintf("kaizen/%s-%s.md", date, slug)
	fullPath := filepath.Join(vaultPath, filename)

	// Ensure kaizen directory exists
	kaizenDir := filepath.Join(vaultPath, "kaizen")
	if err := os.MkdirAll(kaizenDir, 0o755); err != nil {
		return fmt.Errorf("create kaizen directory: %w", err)
	}

	// Build frontmatter
	var content strings.Builder
	content.WriteString("---\n")
	content.WriteString(fmt.Sprintf("title: %s\n", description))
	content.WriteString("content_type: kaizen\n")
	content.WriteString(fmt.Sprintf("status: %s\n", status))
	if agent != "" {
		content.WriteString(fmt.Sprintf("agent: %s\n", agent))
	}
	content.WriteString("tags: [kaizen]\n")
	content.WriteString("---\n\n")
	content.WriteString(description)
	content.WriteString("\n")

	// Write file
	if err := os.WriteFile(fullPath, []byte(content.String()), 0o644); err != nil {
		return fmt.Errorf("write kaizen note: %w", err)
	}

	// Auto-index the file
	db, err := store.Open()
	if err == nil {
		defer db.Close()
		_ = indexer.IndexSingleFileLite(db, fullPath, filename, vaultPath)
	}

	fmt.Printf("  Saved to %s%s%s\n", cli.Cyan, filename, cli.Reset)
	return nil
}

// kaizenSlugify converts a description into a filesystem-safe slug.
func kaizenSlugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	result := strings.TrimRight(b.String(), "-")
	if len(result) > 60 {
		result = result[:60]
		result = strings.TrimRight(result, "-")
	}
	return result
}

// capitalize returns s with its first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// extractFrontmatterStatus extracts the "status:" field from markdown frontmatter.
// Returns "open" if not found.
func extractFrontmatterStatus(text string) string {
	// Look for status: value in YAML frontmatter (between --- markers)
	lines := strings.Split(text, "\n")
	inFrontmatter := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break // end of frontmatter
		}
		if inFrontmatter && strings.HasPrefix(trimmed, "status:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "status:"))
			if val != "" {
				return val
			}
		}
	}
	return "open"
}

func statusIcon(status string) string {
	switch status {
	case "addressed":
		return cli.Green + "✓" + cli.Reset
	case "wontfix":
		return cli.Dim + "✕" + cli.Reset
	default:
		return cli.Yellow + "○" + cli.Reset
	}
}
