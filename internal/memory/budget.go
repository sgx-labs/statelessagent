package memory

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/store"
)

var specialCharReplacer = strings.NewReplacer(
	"\u2014", " ", // em-dash
	"\u2013", " ", // en-dash
	"_", " ",
	"-", " ",
)

var whitespaceCollapse = regexp.MustCompile(`\s+`)
var datePrefixPattern = regexp.MustCompile(`^\d{4}[-_]\d{2}[-_]\d{2}[-_ ]*`)

// NormalizeForMatching strips special characters and collapses whitespace.
func NormalizeForMatching(text string) string {
	text = specialCharReplacer.Replace(text)
	return strings.TrimSpace(whitespaceCollapse.ReplaceAllString(text, " "))
}

// ExtractTitleWords extracts the meaningful title portion from a filename.
func ExtractTitleWords(filename string) string {
	cleaned := datePrefixPattern.ReplaceAllString(filename, "")
	cleaned = NormalizeForMatching(cleaned)
	return strings.ToLower(strings.TrimSpace(cleaned))
}

// EstimateTokens estimates token count from text (~4 chars per token).
func EstimateTokens(text string) int {
	return len(text) / 4
}

// LogInjection logs a context injection event.
func LogInjection(db *store.DB, sessionID, hookName string, injectedPaths []string, injectedText string) {
	rec := &store.UsageRecord{
		SessionID:       sessionID,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
		HookName:        hookName,
		InjectedPaths:   injectedPaths,
		EstimatedTokens: EstimateTokens(injectedText),
		WasReferenced:   false,
	}
	db.InsertUsage(rec) // ignore errors — non-critical
}

// DetectReferences scans assistant text for references to injected vault paths/titles.
func DetectReferences(db *store.DB, sessionID string, assistantText string) int {
	records, err := db.GetUsageBySession(sessionID)
	if err != nil || len(records) == 0 {
		return 0
	}

	textLower := strings.ToLower(assistantText)
	textNormalized := NormalizeForMatching(textLower)

	referencedCount := 0

	for _, rec := range records {
		wasRef := false
		for _, path := range rec.InjectedPaths {
			// Check full path
			if strings.Contains(textLower, strings.ToLower(path)) {
				wasRef = true
				break
			}
			// Check by filename (exact)
			filename := strings.TrimSuffix(filepath.Base(path), ".md")
			filenameLower := strings.ToLower(filename)
			if len(filenameLower) > 3 && strings.Contains(textLower, filenameLower) {
				wasRef = true
				break
			}
			// Check by normalized filename
			filenameNorm := NormalizeForMatching(filenameLower)
			if len(filenameNorm) > 5 && strings.Contains(textNormalized, filenameNorm) {
				wasRef = true
				break
			}
			// Check by title words (filename without date prefix)
			titleWords := ExtractTitleWords(filename)
			if len(titleWords) >= 3 && strings.Contains(textNormalized, titleWords) {
				wasRef = true
				break
			}
		}

		if wasRef {
			referencedCount++
			db.MarkReferenced(rec.ID) // ignore errors
		}
	}

	return referencedCount
}

// BudgetReport holds context budget utilization statistics.
type BudgetReport struct {
	SessionsAnalyzed    int                    `json:"sessions_analyzed"`
	TotalInjections     int                    `json:"total_injections"`
	TotalTokensInjected int                    `json:"total_tokens_injected"`
	ReferencedCount     int                    `json:"referenced_injections"`
	UtilizationRate     float64                `json:"utilization_rate"`
	PerHook             map[string]HookStats   `json:"per_hook"`
	Suggestions         []string               `json:"suggestions"`
}

// HookStats holds per-hook utilization stats.
type HookStats struct {
	Injections          int     `json:"injections"`
	Referenced          int     `json:"referenced"`
	UtilizationRate     float64 `json:"utilization_rate"`
	TotalTokens         int     `json:"total_tokens"`
	AvgTokensPerInject  int     `json:"avg_tokens_per_injection"`
}

// GetBudgetReport generates a context budget utilization report.
func GetBudgetReport(db *store.DB, sessionID string, lastNSessions int) interface{} {
	var records []store.UsageRecord
	var err error

	if sessionID != "" {
		records, err = db.GetUsageBySession(sessionID)
	} else {
		records, err = db.GetRecentUsage(lastNSessions)
	}

	if err != nil || len(records) == 0 {
		return map[string]string{
			"status": "no data",
			"hint":   "Context usage tracking starts after hooks inject context.",
		}
	}

	totalInjections := len(records)
	totalTokens := 0
	referenced := 0
	sessions := make(map[string]bool)
	hookData := make(map[string]*HookStats)

	for _, rec := range records {
		sessions[rec.SessionID] = true
		totalTokens += rec.EstimatedTokens
		if rec.WasReferenced {
			referenced++
		}

		hs, ok := hookData[rec.HookName]
		if !ok {
			hs = &HookStats{}
			hookData[rec.HookName] = hs
		}
		hs.Injections++
		hs.TotalTokens += rec.EstimatedTokens
		if rec.WasReferenced {
			hs.Referenced++
		}
	}

	utilizationRate := 0.0
	if totalInjections > 0 {
		utilizationRate = float64(referenced) / float64(totalInjections)
	}

	perHook := make(map[string]HookStats)
	for name, hs := range hookData {
		if hs.Injections > 0 {
			hs.UtilizationRate = math.Round(float64(hs.Referenced)/float64(hs.Injections)*1000) / 1000
			hs.AvgTokensPerInject = hs.TotalTokens / hs.Injections
		}
		perHook[name] = *hs
	}

	var suggestions []string
	if utilizationRate < 0.3 {
		suggestions = append(suggestions, "Low utilization rate (<30%). Consider raising min_confidence threshold for context surfacing.")
	}
	if utilizationRate > 0.8 {
		suggestions = append(suggestions, "High utilization rate (>80%). Context surfacing is well-calibrated.")
	}
	for name, hs := range perHook {
		if hs.UtilizationRate < 0.2 && hs.Injections > 3 {
			suggestions = append(suggestions, fmt.Sprintf("%s: Very low utilization (%.0f%%). Consider adjusting or disabling.", name, hs.UtilizationRate*100))
		}
		if hs.AvgTokensPerInject > 500 {
			suggestions = append(suggestions, fmt.Sprintf("%s: High average tokens (%d). Consider shorter snippets.", name, hs.AvgTokensPerInject))
		}
	}

	return BudgetReport{
		SessionsAnalyzed:    len(sessions),
		TotalInjections:     totalInjections,
		TotalTokensInjected: totalTokens,
		ReferencedCount:     referenced,
		UtilizationRate:     math.Round(utilizationRate*1000) / 1000,
		PerHook:             perHook,
		Suggestions:         suggestions,
	}
}

// SaveBudgetReport writes the budget report to a JSON file.
func SaveBudgetReport(report interface{}, outputPath string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	return os.WriteFile(outputPath, data, 0o644)
}
