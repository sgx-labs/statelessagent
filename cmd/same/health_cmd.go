package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func healthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check your vault's health [experimental]",
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
	fmt.Fprintf(os.Stderr, "%s  This feature is experimental. Feedback welcome: https://github.com/sgx-labs/statelessagent/issues%s\n\n", cli.Dim, cli.Reset)

	db, err := store.Open()
	if err != nil {
		return dbOpenError(err)
	}
	defer db.Close()

	// Total notes (chunk_id=0 to deduplicate chunks, excluding suppressed)
	var totalNotes int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0 AND COALESCE(suppressed, 0) = 0`,
	).Scan(&totalNotes); err != nil {
		return fmt.Errorf("query notes: %w", err)
	}

	// Suppressed notes (counted separately)
	var suppressedCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0 AND COALESCE(suppressed, 0) = 1`,
	).Scan(&suppressedCount)

	if totalNotes == 0 {
		fmt.Printf("\n  Your vault is empty. Add markdown files to your vault directory, or run %ssame seed install%s for starter content.\n\n",
			cli.Bold, cli.Reset)
		return nil
	}

	// Embedding coverage
	var embeddedCount int
	err = db.Conn().QueryRow(
		`SELECT COUNT(DISTINCT vn.id) FROM vault_notes vn
		 INNER JOIN vault_notes_vec vnv ON vn.id = vnv.note_id
		 WHERE vn.chunk_id = 0 AND COALESCE(vn.suppressed, 0) = 0`,
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
		 FROM vault_notes WHERE chunk_id = 0 AND COALESCE(suppressed, 0) = 0
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
		`SELECT AVG(confidence) FROM vault_notes WHERE chunk_id = 0 AND confidence > 0 AND COALESCE(suppressed, 0) = 0`,
	).Scan(&avgConfidence)

	// Stale notes (older than 30 days, never accessed)
	thirtyDaysAgo := float64(time.Now().Add(-30 * 24 * time.Hour).Unix())
	var staleCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND access_count = 0
		 AND modified < ? AND COALESCE(suppressed, 0) = 0`,
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
		 FROM vault_notes WHERE chunk_id = 0 AND COALESCE(suppressed, 0) = 0
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
		`SELECT MIN(modified) FROM vault_notes WHERE chunk_id = 0 AND COALESCE(suppressed, 0) = 0`,
	).Scan(&oldestModified)

	// Recent activity (notes modified in last 7 days)
	sevenDaysAgo := float64(time.Now().Add(-7 * 24 * time.Hour).Unix())
	var recentCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND modified > ? AND COALESCE(suppressed, 0) = 0`,
		sevenDaysAgo,
	).Scan(&recentCount)

	// Knowledge notes (in knowledge/ directory or content_type = 'knowledge')
	var knowledgeCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND (path LIKE 'knowledge/%' OR content_type = 'knowledge')
		 AND COALESCE(suppressed, 0) = 0`,
	).Scan(&knowledgeCount)

	// Notes with at least one access
	var accessedCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND access_count > 0 AND COALESCE(suppressed, 0) = 0`,
	).Scan(&accessedCount)

	// Never accessed count
	var neverAccessedCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND access_count = 0 AND COALESCE(suppressed, 0) = 0`,
	).Scan(&neverAccessedCount)

	// Trust / Provenance: check source divergence
	vaultPath := config.VaultPath()
	diverged, _ := db.CheckSourceDivergence(vaultPath)

	// Update trust states based on divergence results
	var stalePaths, validatedPaths []string
	divergedByNote := make(map[string]store.DivergenceResult) // first diverged source per note
	for _, d := range diverged {
		if _, seen := divergedByNote[d.NotePath]; !seen {
			divergedByNote[d.NotePath] = d
			stalePaths = append(stalePaths, d.NotePath)
		}
	}
	// Find validated notes: notes with sources that are NOT diverged
	allSourcedPaths, _ := db.GetNotesWithSources()
	staleSet := make(map[string]bool, len(stalePaths))
	for _, p := range stalePaths {
		staleSet[p] = true
	}
	for _, p := range allSourcedPaths {
		if !staleSet[p] {
			validatedPaths = append(validatedPaths, p)
		}
	}

	if len(stalePaths) > 0 {
		_ = db.UpdateTrustState(stalePaths, "stale")
	}
	if len(validatedPaths) > 0 {
		_ = db.UpdateTrustState(validatedPaths, "validated")
	}

	trustSummaryPtr, _ := db.GetTrustStateSummary()
	trustSummary := store.TrustSummary{}
	if trustSummaryPtr != nil {
		trustSummary = *trustSummaryPtr
	}

	// Compute health score (0-100)
	score := computeHealthScore(totalNotes, embeddedCount, knowledgeCount, recentCount, accessedCount, trustSummary)

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
		`SELECT MAX(modified) FROM vault_notes WHERE chunk_id = 0 AND COALESCE(suppressed, 0) = 0`,
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
	if suppressedCount > 0 {
		fmt.Printf("  %-14s %d\n", "Suppressed:", suppressedCount)
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

	// Trust / Provenance
	cli.Section("Trust")

	validatedLabel := fmt.Sprintf("%d notes", trustSummary.Validated)
	if trustSummary.Validated > 0 {
		validatedLabel += " (sources unchanged)"
	}
	staleLabel := fmt.Sprintf("%d notes", trustSummary.Stale)
	if trustSummary.Stale > 0 {
		staleLabel += " (source files modified since capture)"
	}
	unknownLabel := fmt.Sprintf("%d notes", trustSummary.Unknown)
	if trustSummary.Unknown > 0 {
		unknownLabel += " (no provenance recorded)"
	}

	// Build contradicted label with breakdown if available
	contradictedLabel := fmt.Sprintf("%d notes", trustSummary.Contradicted)
	if trustSummary.Contradicted > 0 {
		cBreakdown, _ := db.GetContradictionSummary()
		if cBreakdown != nil {
			var parts []string
			if cBreakdown.Factual > 0 {
				parts = append(parts, fmt.Sprintf("%d factual", cBreakdown.Factual))
			}
			if cBreakdown.Preference > 0 {
				parts = append(parts, fmt.Sprintf("%d preference", cBreakdown.Preference))
			}
			if cBreakdown.Context > 0 {
				parts = append(parts, fmt.Sprintf("%d context", cBreakdown.Context))
			}
			if cBreakdown.Untyped > 0 {
				parts = append(parts, fmt.Sprintf("%d untyped", cBreakdown.Untyped))
			}
			if len(parts) > 0 {
				contradictedLabel += " (" + strings.Join(parts, ", ") + ")"
			}
		}
	}

	fmt.Printf("  %-14s %s%s%s\n", "Validated:", cli.Green, validatedLabel, cli.Reset)
	fmt.Printf("  %-14s %s%s%s\n", "Stale:", cli.Yellow, staleLabel, cli.Reset)
	if trustSummary.Contradicted > 0 {
		fmt.Printf("  %-14s %s%s%s\n", "Contradicted:", cli.Yellow, contradictedLabel, cli.Reset)
	}
	fmt.Printf("  %-14s %s%s%s\n", "Unknown:", cli.Dim, unknownLabel, cli.Reset)

	// Show up to 5 stale notes with details
	if len(stalePaths) > 0 {
		fmt.Println()
		fmt.Printf("  %sStale notes:%s\n", cli.Bold, cli.Reset)
		shown := 0
		for _, notePath := range stalePaths {
			if shown >= 5 {
				break
			}
			d := divergedByNote[notePath]
			sourceBase := filepath.Base(d.SourcePath)
			// Show when the source file was modified, not when we captured it
			sourcePath := d.SourcePath
			if !filepath.IsAbs(sourcePath) {
				sourcePath = filepath.Join(vaultPath, sourcePath)
			}
			var agoStr string
			if sourceInfo, statErr := os.Stat(sourcePath); statErr == nil {
				agoStr = relativeTimeStr(time.Since(sourceInfo.ModTime()))
			} else {
				agoStr = "source deleted"
			}
			fmt.Printf("    %s%-30s%s %s%s— %s changed %s%s\n",
				cli.Yellow, notePath, cli.Reset,
				cli.Dim, "", sourceBase, agoStr, cli.Reset)
			shown++
		}
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
	if trustSummary.Stale > 0 {
		recs = append(recs, fmt.Sprintf("%d notes have stale sources — review or re-capture them", trustSummary.Stale))
	}

	// Kaizen items
	var kaizenOpenCount int
	_ = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND content_type = 'kaizen'
		 AND COALESCE(suppressed, 0) = 0`,
	).Scan(&kaizenOpenCount)
	if kaizenOpenCount > 0 {
		recs = append(recs, fmt.Sprintf("%d kaizen items logged — run 'same kaizen' to review", kaizenOpenCount))
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
//   - Embedding coverage: (embedded/total) * 20 points
//   - Knowledge ratio: min(knowledge/total * 10, 20) points
//   - Freshness: (recent_7day/total) * 20 points (capped at 20)
//   - Usage: (accessed/total) * 20 points
//   - Trust: (validated / (validated + stale)) * 20 points (20/20 if no sources tracked)
func computeHealthScore(total, embedded, knowledge, recent, accessed int, trust store.TrustSummary) int {
	if total == 0 {
		return 0
	}

	// Embedding coverage: 0-20 points
	embedScore := float64(embedded) / float64(total) * 20.0

	// Knowledge ratio: 0-20 points
	knowledgeScore := float64(knowledge) / float64(total) * 10.0 * 20.0
	if knowledgeScore > 20.0 {
		knowledgeScore = 20.0
	}

	// Freshness: 0-20 points
	freshnessScore := float64(recent) / float64(total) * 20.0
	if freshnessScore > 20.0 {
		freshnessScore = 20.0
	}

	// Usage: 0-20 points
	usageScore := float64(accessed) / float64(total) * 20.0

	// Trust: 0-20 points
	// If no sources are tracked (all unknown), don't penalize — score at 20/20
	var trustScore float64
	tracked := trust.Validated + trust.Stale
	if tracked == 0 {
		trustScore = 20.0
	} else {
		trustScore = float64(trust.Validated) / float64(tracked) * 20.0
	}

	score := int(embedScore + knowledgeScore + freshnessScore + usageScore + trustScore)
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

// relativeTimeStr formats a duration as a human-readable relative time string.
func relativeTimeStr(d time.Duration) string {
	hours := int(d.Hours())
	if hours < 1 {
		return "just now"
	}
	if hours < 24 {
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := hours / 24
	if days == 1 {
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", days)
}
