package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func healthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check your vault's health",
		Long: `Analyze your vault and report on its health.

Health checks:
  - Total notes and embedding coverage
  - Knowledge ratio (consolidated vs raw notes)
  - Stale notes (old, never retrieved)
  - Average confidence score
  - Content type distribution
  - Vault age and growth rate

A healthy vault improves over time through consolidation,
curation, and regular use. Run 'same consolidate' to improve
vault health.

Examples:
  same health`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHealth()
		},
	}
	return cmd
}

func runHealth() error {
	db, err := store.Open()
	if err != nil {
		return dbOpenError(err)
	}
	defer db.Close()

	// Total notes (chunk_id=0 to deduplicate chunks)
	var totalNotes int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0`,
	).Scan(&totalNotes); err != nil {
		return fmt.Errorf("query notes: %w", err)
	}

	if totalNotes == 0 {
		fmt.Printf("\n  Your vault is empty. Run %ssame store%s or %ssame demo%s to get started.\n\n",
			cli.Bold, cli.Reset, cli.Bold, cli.Reset)
		return nil
	}

	// Embedding coverage
	var embeddedCount int
	err = db.Conn().QueryRow(
		`SELECT COUNT(DISTINCT vn.id) FROM vault_notes vn
		 INNER JOIN vault_notes_vec vnv ON vn.id = vnv.note_id
		 WHERE vn.chunk_id = 0`,
	).Scan(&embeddedCount)
	if err != nil {
		// vault_notes_vec might not exist if no embeddings configured
		embeddedCount = 0
	}

	// Content type distribution
	type contentTypeStat struct {
		name  string
		count int
	}
	var contentTypes []contentTypeStat
	ctRows, err := db.Conn().Query(
		`SELECT COALESCE(content_type, 'note') as ct, COUNT(*)
		 FROM vault_notes WHERE chunk_id = 0
		 GROUP BY ct ORDER BY COUNT(*) DESC`,
	)
	if err == nil {
		defer ctRows.Close()
		for ctRows.Next() {
			var ct contentTypeStat
			if err := ctRows.Scan(&ct.name, &ct.count); err == nil {
				contentTypes = append(contentTypes, ct)
			}
		}
	}

	// Average confidence
	var avgConfidence sql.NullFloat64
	_ = db.Conn().QueryRow(
		`SELECT AVG(confidence) FROM vault_notes WHERE chunk_id = 0 AND confidence > 0`,
	).Scan(&avgConfidence)

	// Stale notes (older than 30 days, never accessed)
	thirtyDaysAgo := float64(time.Now().Add(-30 * 24 * time.Hour).Unix())
	var staleCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND access_count = 0
		 AND modified < ?`,
		thirtyDaysAgo,
	).Scan(&staleCount)

	// Most active notes (highest access_count)
	type topNote struct {
		path        string
		title       string
		accessCount int
		confidence  float64
	}
	var topNotes []topNote
	topRows, err := db.Conn().Query(
		`SELECT path, title, access_count, confidence
		 FROM vault_notes WHERE chunk_id = 0
		 ORDER BY access_count DESC LIMIT 5`,
	)
	if err == nil {
		defer topRows.Close()
		for topRows.Next() {
			var n topNote
			if err := topRows.Scan(&n.path, &n.title, &n.accessCount, &n.confidence); err == nil {
				topNotes = append(topNotes, n)
			}
		}
	}

	// Vault age (oldest note)
	var oldestModified sql.NullFloat64
	_ = db.Conn().QueryRow(
		`SELECT MIN(modified) FROM vault_notes WHERE chunk_id = 0`,
	).Scan(&oldestModified)

	// Recent activity (notes modified in last 7 days)
	sevenDaysAgo := float64(time.Now().Add(-7 * 24 * time.Hour).Unix())
	var recentCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND modified > ?`,
		sevenDaysAgo,
	).Scan(&recentCount)

	// Knowledge notes (in knowledge/ directory or content_type = 'knowledge')
	var knowledgeCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND (path LIKE 'knowledge/%' OR content_type = 'knowledge')`,
	).Scan(&knowledgeCount)

	// Notes with at least one access
	var accessedCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND access_count > 0`,
	).Scan(&accessedCount)

	// Never accessed count
	var neverAccessedCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND access_count = 0`,
	).Scan(&neverAccessedCount)

	// Compute health score (0-100)
	score := computeHealthScore(totalNotes, embeddedCount, knowledgeCount, recentCount, accessedCount)

	// Vault age string
	vaultAge := ""
	if oldestModified.Valid && oldestModified.Float64 > 0 {
		oldest := time.Unix(int64(oldestModified.Float64), 0)
		age := time.Since(oldest)
		vaultAge = formatDuration(age)
	}

	// Last active (most recent modification)
	var newestModified sql.NullFloat64
	_ = db.Conn().QueryRow(
		`SELECT MAX(modified) FROM vault_notes WHERE chunk_id = 0`,
	).Scan(&newestModified)
	lastActive := ""
	if newestModified.Valid && newestModified.Float64 > 0 {
		newest := time.Unix(int64(newestModified.Float64), 0)
		ago := time.Since(newest)
		lastActive = formatDuration(ago) + " ago"
	}

	// --- Display output ---

	cli.Section("Vault Health")

	fmt.Printf("  Score: %s\n", healthBar(score))

	cli.Section("Overview")

	fmt.Printf("  %-14s %s\n", "Notes:", cli.FormatNumber(totalNotes))
	if embeddedCount > 0 || totalNotes > 0 {
		pct := 0
		if totalNotes > 0 {
			pct = embeddedCount * 100 / totalNotes
		}
		fmt.Printf("  %-14s %s/%s (%d%%)\n", "Embedded:", cli.FormatNumber(embeddedCount), cli.FormatNumber(totalNotes), pct)
	}
	if knowledgeCount > 0 {
		label := "consolidated note"
		if knowledgeCount != 1 {
			label = "consolidated notes"
		}
		fmt.Printf("  %-14s %d %s\n", "Knowledge:", knowledgeCount, label)
	} else {
		fmt.Printf("  %-14s %s0%s\n", "Knowledge:", cli.Dim, cli.Reset)
	}
	if vaultAge != "" {
		fmt.Printf("  %-14s %s\n", "Vault age:", vaultAge)
	}
	if lastActive != "" {
		fmt.Printf("  %-14s %s\n", "Last active:", lastActive)
	}

	// Content type distribution
	if len(contentTypes) > 0 {
		cli.Section("Content Types")
		for _, ct := range contentTypes {
			pct := ct.count * 100 / totalNotes
			fmt.Printf("  %-14s %d (%d%%)\n", ct.name+":", ct.count, pct)
		}
	}

	// Activity
	cli.Section("Activity")

	fmt.Printf("  %-20s %d notes\n", "Active (7 days):", recentCount)
	fmt.Printf("  %-20s %d notes\n", "Stale (30+ days):", staleCount)
	fmt.Printf("  %-20s %d notes\n", "Never accessed:", neverAccessedCount)
	if avgConfidence.Valid {
		fmt.Printf("  %-20s %.2f\n", "Avg confidence:", avgConfidence.Float64)
	}

	// Top notes (only show notes that have been accessed)
	hasAccessed := false
	for _, n := range topNotes {
		if n.accessCount > 0 {
			hasAccessed = true
			break
		}
	}
	if hasAccessed {
		cli.Section("Top Notes")
		rank := 1
		for _, n := range topNotes {
			if n.accessCount == 0 {
				continue
			}
			confStr := ""
			if n.confidence > 0 {
				confStr = fmt.Sprintf(", confidence: %.1f", n.confidence)
			}
			fmt.Printf("  %d. %s (accessed %d times%s)\n", rank, n.path, n.accessCount, confStr)
			rank++
			if rank > 5 {
				break
			}
		}
	}

	// Recommendations
	var recs []string
	if staleCount > 0 {
		recs = append(recs, fmt.Sprintf("Run 'same consolidate' to organize %d stale notes", staleCount))
	}
	noEmbeddings := totalNotes - embeddedCount
	if noEmbeddings > 0 {
		label := "notes have"
		if noEmbeddings == 1 {
			label = "note has"
		}
		recs = append(recs, fmt.Sprintf("%d %s no embeddings -- run 'same reindex'", noEmbeddings, label))
	}
	if neverAccessedCount > 0 {
		recs = append(recs, fmt.Sprintf("Consider reviewing %d never-accessed notes", neverAccessedCount))
	}
	if knowledgeCount == 0 && totalNotes >= 5 {
		recs = append(recs, "Run 'same consolidate' to create knowledge summaries")
	}

	if len(recs) > 0 {
		cli.Section("Recommendations")
		for _, r := range recs {
			fmt.Printf("  %s%s %s%s\n", cli.Dim, "\u00b7", r, cli.Reset)
		}
	}

	cli.Footer()
	return nil
}

// computeHealthScore calculates a 0-100 health score from vault metrics.
//
//   - Embedding coverage: (embedded/total) * 25 points
//   - Knowledge ratio: min(knowledge/total * 10, 25) points
//   - Freshness: (recent_7day/total) * 25 points (capped at 25)
//   - Usage: (accessed/total) * 25 points
func computeHealthScore(total, embedded, knowledge, recent, accessed int) int {
	if total == 0 {
		return 0
	}

	// Embedding coverage: 0-25 points
	embedScore := float64(embedded) / float64(total) * 25.0

	// Knowledge ratio: 0-25 points
	knowledgeScore := float64(knowledge) / float64(total) * 10.0 * 25.0
	if knowledgeScore > 25.0 {
		knowledgeScore = 25.0
	}

	// Freshness: 0-25 points
	freshnessScore := float64(recent) / float64(total) * 25.0
	if freshnessScore > 25.0 {
		freshnessScore = 25.0
	}

	// Usage: 0-25 points
	usageScore := float64(accessed) / float64(total) * 25.0

	score := int(embedScore + knowledgeScore + freshnessScore + usageScore)
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

// healthBar renders a visual health bar with score label.
func healthBar(score int) string {
	filled := score / 5 // 20 chars total
	if filled > 20 {
		filled = 20
	}
	empty := 20 - filled
	bar := strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", empty)

	var label string
	var color string
	switch {
	case score >= 80:
		label = "Excellent"
		color = cli.Green
	case score >= 60:
		label = "Good"
		color = cli.Cyan
	case score >= 40:
		label = "Fair"
		color = cli.Yellow
	default:
		label = "Needs attention"
		color = cli.Yellow
	}
	return fmt.Sprintf("%s%d/100 %s %s%s", color, score, bar, label, cli.Reset)
}
