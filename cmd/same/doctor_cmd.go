package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/setup"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func doctorCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system health and diagnose issues",
		Long:  "Runs health checks on your SAME setup: verifies Ollama is running, your notes are indexed, and search is working.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

// DoctorResult represents a single health check result
type DoctorResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "pass", "skip", "fail"
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// DoctorReport represents the complete health check report
type DoctorReport struct {
	Checks  []DoctorResult `json:"checks"`
	Summary struct {
		Total   int `json:"total"`
		Passed  int `json:"passed"`
		Skipped int `json:"skipped"`
		Failed  int `json:"failed"`
	} `json:"summary"`
}

// sanitizeErrorForJSON removes potentially sensitive information from error messages
// SECURITY: Prevents leaking absolute file paths, hostnames, or other PII in JSON output
func sanitizeErrorForJSON(err error) string {
	msg := err.Error()
	// Remove absolute paths by stripping anything that looks like a filesystem path
	// This is a simple heuristic: if the error contains a '/', replace with generic message
	if strings.Contains(msg, "/") || strings.Contains(msg, "\\") {
		// Try to extract just the error type without the path
		if idx := strings.LastIndex(msg, ":"); idx != -1 {
			return strings.TrimSpace(msg[idx+1:])
		}
		return "operation failed"
	}
	return msg
}

func runDoctor(jsonOut bool) error {
	passed := 0
	failed := 0
	skipped := 0
	var results []DoctorResult

	// Probe Ollama once up front so embedding-dependent checks can skip gracefully.
	ollamaAvailable := false
	if embedClient, err := newEmbedProvider(); err == nil {
		if _, err := embedClient.GetQueryEmbedding("test"); err == nil {
			ollamaAvailable = true
		}
	}

	check := func(name string, hint string, fn func() (string, error)) {
		detail, err := fn()
		if err != nil {
			if jsonOut {
				results = append(results, DoctorResult{
					Name:    name,
					Status:  "fail",
					Message: sanitizeErrorForJSON(err),
					Hint:    hint,
				})
			} else {
				fmt.Printf("  %s✗%s %s: %s\n",
					cli.Red, cli.Reset, name, err)
				if hint != "" {
					fmt.Printf("    → %s\n", hint)
				}
			}
			failed++
		} else {
			if jsonOut {
				results = append(results, DoctorResult{
					Name:    name,
					Status:  "pass",
					Message: detail,
				})
			} else {
				if detail != "" {
					fmt.Printf("  %s✓%s %s (%s)\n",
						cli.Green, cli.Reset, name, detail)
				} else {
					fmt.Printf("  %s✓%s %s\n",
						cli.Green, cli.Reset, name)
				}
			}
			passed++
		}
	}

	// skip marks a check as skipped (lite mode) instead of failed.
	skip := func(name string, reason string) {
		if jsonOut {
			results = append(results, DoctorResult{
				Name:    name,
				Status:  "skip",
				Message: reason,
			})
		} else {
			fmt.Printf("  %s-%s %s: %s\n",
				cli.Dim, cli.Reset, name, reason)
		}
		skipped++
	}

	if !jsonOut {
		cli.Header("SAME Health Check")
		fmt.Println()
	}

	// 1. Vault path
	check("Vault path", "run 'same init' or set VAULT_PATH", func() (string, error) {
		vp := config.VaultPath()
		if vp == "" {
			return "", fmt.Errorf("no vault found")
		}
		info, err := os.Stat(vp)
		if err != nil {
			return "", fmt.Errorf("path does not exist")
		}
		if !info.IsDir() {
			return "", fmt.Errorf("not a directory")
		}
		return "", nil
	})

	// 2. Database
	check("Database", "run 'same init' or 'same reindex'", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		noteCount, err := db.NoteCount()
		if err != nil {
			return "", fmt.Errorf("cannot query")
		}
		chunkCount, err := db.ChunkCount()
		if err != nil {
			return "", fmt.Errorf("cannot query")
		}
		if noteCount == 0 {
			return "", fmt.Errorf("empty")
		}
		return fmt.Sprintf("%s notes, %s chunks",
			cli.FormatNumber(noteCount),
			cli.FormatNumber(chunkCount)), nil
	})

	// 2b. Index mode
	check("Index mode", "run 'same reindex' with Ollama for semantic search", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open database")
		}
		defer db.Close()
		if db.HasVectors() {
			return "semantic (Ollama embeddings)", nil
		}
		noteCount, _ := db.NoteCount()
		if noteCount > 0 {
			return "keyword-only (install Ollama + run 'same reindex' to upgrade)", nil
		}
		return "empty", nil
	})

	// 3. Embedding provider — skip gracefully in lite mode
	if ollamaAvailable {
		check("Ollama connection", "make sure Ollama is running (look for llama icon), or use keyword-only mode", func() (string, error) {
			embedClient, err := newEmbedProvider()
			if err != nil {
				return "", fmt.Errorf("not connected (keyword search still works)")
			}
			return fmt.Sprintf("connected via %s", embedClient.Name()), nil
		})
	} else {
		skip("Ollama connection", "skipped (lite mode — keyword search active)")
	}

	// 4. Vector search — skip gracefully in lite mode
	if ollamaAvailable {
		check("Search working", "run 'same reindex' to rebuild", func() (string, error) {
			db, err := store.Open()
			if err != nil {
				return "", err
			}
			defer db.Close()

			embedClient, err := newEmbedProvider()
			if err != nil {
				return "", fmt.Errorf("provider error")
			}
			vec, err := embedClient.GetQueryEmbedding("test query")
			if err != nil {
				return "", fmt.Errorf("embedding failed")
			}

			results, err := db.VectorSearch(vec, store.SearchOptions{TopK: 1})
			if err != nil {
				return "", fmt.Errorf("search failed")
			}
			if len(results) == 0 {
				return "", fmt.Errorf("no results")
			}
			return "", nil
		})
	} else {
		skip("Search working", "skipped (lite mode — needs Ollama for vector search)")
	}

	// 5. Context surfacing — fall back to keyword test in lite mode
	if ollamaAvailable {
		check("Finding relevant notes", "try 'same search <query>' to test", func() (string, error) {
			db, err := store.Open()
			if err != nil {
				return "", err
			}
			defer db.Close()

			embedClient, err := newEmbedProvider()
			if err != nil {
				return "", fmt.Errorf("provider error")
			}
			vec, err := embedClient.GetQueryEmbedding("what notes are in this vault")
			if err != nil {
				return "", fmt.Errorf("embedding failed")
			}

			raw, err := db.VectorSearchRaw(vec, 3)
			if err != nil {
				return "", fmt.Errorf("raw search failed")
			}
			if len(raw) == 0 {
				return "", fmt.Errorf("no results")
			}
			return "", nil
		})
	} else {
		check("Finding relevant notes", "try 'same search <query>' to test", func() (string, error) {
			db, err := store.Open()
			if err != nil {
				return "", err
			}
			defer db.Close()
			noteCount, _ := db.NoteCount()
			if noteCount == 0 {
				return "", fmt.Errorf("no notes indexed")
			}
			// Actually test keyword search works (FTS5 or LIKE-based)
			mode := "keyword"
			if db.FTSAvailable() {
				results, ftsErr := db.FTS5Search("test", store.SearchOptions{TopK: 1})
				if ftsErr != nil || results == nil {
					mode = "keyword (LIKE)"
				} else {
					mode = "keyword (FTS5)"
				}
			} else {
				terms := store.ExtractSearchTerms("test")
				_, kwErr := db.KeywordSearch(terms, 1)
				if kwErr != nil {
					return "", fmt.Errorf("keyword search failed: %w", kwErr)
				}
				mode = "keyword (LIKE)"
			}
			return fmt.Sprintf("%s (%s notes)", mode, cli.FormatNumber(noteCount)), nil
		})
	}

	// 6. Private content excluded
	check("Private folders hidden", "'same reindex --force' to refresh", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", err
		}
		defer db.Close()

		var count int
		err = db.Conn().QueryRow("SELECT COUNT(*) FROM vault_notes WHERE path LIKE '_PRIVATE/%'").Scan(&count)
		if err != nil {
			return "", nil
		}
		if count > 0 {
			return "", fmt.Errorf("%d _PRIVATE/ entries in index", count)
		}
		return "", nil
	})

	// 7. Ollama localhost only
	check("Data stays local", "Ollama should run on your computer, not a remote server", func() (string, error) {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			return "", err
		}
		if !strings.Contains(ollamaURL, "localhost") && !strings.Contains(ollamaURL, "127.0.0.1") && !strings.Contains(ollamaURL, "::1") {
			// SECURITY: Don't leak the actual URL in error message
			return "", fmt.Errorf("Ollama URL is not localhost")
		}
		return "", nil
	})

	// 8. Config file validity
	check("Config file", "check .same/config.toml for syntax errors", func() (string, error) {
		_, err := config.LoadConfig()
		if err != nil {
			return "", err
		}
		return "", nil
	})

	// 9. Hook installation
	check("Hooks installed", "run 'same setup hooks'", func() (string, error) {
		vp := config.VaultPath()
		if vp == "" {
			return "", fmt.Errorf("no vault to check")
		}
		settingsPath := filepath.Join(vp, ".claude", "settings.json")
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			return "", fmt.Errorf("no .claude/settings.json found")
		}
		hookStatus := setup.HooksInstalled(vp)
		activeCount := 0
		for _, v := range hookStatus {
			if v {
				activeCount++
			}
		}
		if activeCount == 0 {
			return "", fmt.Errorf("no SAME hooks found in settings")
		}
		return fmt.Sprintf("%d hooks active", activeCount), nil
	})

	// 10. Vault registry
	check("Vault registry", "register vaults with 'same vault add <name> <path>'", func() (string, error) {
		reg := config.LoadRegistry()
		if len(reg.Vaults) == 0 {
			return "no vaults registered (optional)", nil
		}
		var missingNames []string
		for name, path := range reg.Vaults {
			if _, err := os.Stat(path); err != nil {
				missingNames = append(missingNames, name)
			}
		}
		if len(missingNames) > 0 {
			return "", fmt.Errorf("%d of %d vault path(s) missing: %s",
				len(missingNames), len(reg.Vaults), strings.Join(missingNames, ", "))
		}
		if reg.Default != "" {
			if _, ok := reg.Vaults[reg.Default]; !ok {
				return "", fmt.Errorf("default vault %q not in registry", reg.Default)
			}
		}
		return fmt.Sprintf("%d vault(s) registered", len(reg.Vaults)), nil
	})

	// 11. Database integrity (orphaned vectors)
	check("Database integrity", "run 'same reindex' to rebuild", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		var orphaned int
		err = db.Conn().QueryRow(`
			SELECT COUNT(*) FROM vault_notes_vec v
			LEFT JOIN vault_notes n ON v.note_id = n.id
			WHERE n.id IS NULL
		`).Scan(&orphaned)
		if err != nil {
			return "", nil // table may not exist yet, not an error
		}
		if orphaned > 0 {
			return "", fmt.Errorf("%d orphaned vectors", orphaned)
		}
		return "", nil
	})

	// 11. Index freshness
	check("Index freshness", "run 'same reindex' to update", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		age, err := db.IndexAge()
		if err != nil {
			return "", nil // no index yet
		}
		if age > 7*24*time.Hour {
			return "", fmt.Errorf("last indexed %s ago", formatDuration(age))
		}
		return fmt.Sprintf("last indexed %s ago", formatDuration(age)), nil
	})

	// 12. Log file size
	check("Log file size", "rotation keeps logs under 5MB automatically", func() (string, error) {
		logPath := filepath.Join(config.DataDir(), "verbose.log")
		info, err := os.Stat(logPath)
		if os.IsNotExist(err) {
			return "no log file", nil
		}
		if err != nil {
			return "", nil
		}
		sizeMB := float64(info.Size()) / (1024 * 1024)
		if sizeMB > 10 {
			return "", fmt.Errorf("verbose.log is %.1f MB", sizeMB)
		}
		return fmt.Sprintf("%.1f MB", sizeMB), nil
	})

	// 13. Embedding config
	check("Embedding config", "run 'same reindex --force' if model changed", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		embedClient, err := newEmbedProvider()
		if err != nil {
			return "", fmt.Errorf("cannot create provider: %v", err)
		}
		if mismatchErr := db.CheckEmbeddingMeta(embedClient.Name(), embedClient.Model(), embedClient.Dimensions()); mismatchErr != nil {
			return "", mismatchErr
		}
		provider, _ := db.GetMeta("embed_provider")
		dims, _ := db.GetMeta("embed_dims")
		if provider == "" {
			return "no metadata stored yet", nil
		}
		return fmt.Sprintf("%s, %s dims", provider, dims), nil
	})

	// 14. SQLite integrity (PRAGMA)
	check("SQLite integrity", "run 'same repair' to rebuild", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		return "", db.IntegrityCheck()
	})

	// 15. Retrieval utilization
	check("Retrieval utilization", "try different queries or adjust your profile", func() (string, error) {
		db, err := store.Open()
		if err != nil {
			return "", fmt.Errorf("cannot open")
		}
		defer db.Close()
		usage, err := db.GetRecentUsage(5)
		if err != nil || len(usage) == 0 {
			return "no usage data yet", nil
		}
		total := 0
		referenced := 0
		for _, u := range usage {
			total++
			if u.WasReferenced {
				referenced++
			}
		}
		rate := float64(referenced) / float64(total)
		detail := fmt.Sprintf("%.0f%% of injected context was used", rate*100)
		if rate < 0.20 {
			return fmt.Sprintf("%.0f%% — this improves as your AI references more notes", rate*100), nil
		}
		return detail, nil
	})

	if jsonOut {
		report := DoctorReport{
			Checks: results,
		}
		report.Summary.Total = len(results)
		report.Summary.Passed = passed
		report.Summary.Skipped = skipped
		report.Summary.Failed = failed

		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal JSON: %w", err)
		}
		fmt.Println(string(data))

		if failed > 0 {
			return fmt.Errorf("%d check(s) failed", failed)
		}
		return nil
	}

	summary := fmt.Sprintf("%d passed, %d failed", passed, failed)
	if skipped > 0 {
		summary += fmt.Sprintf(", %d skipped", skipped)
	}
	lines := []string{summary}
	if !ollamaAvailable {
		lines = append(lines, "SAME is running in lite mode (keyword search). Install Ollama for semantic search.")
	}
	if failed > 0 {
		lines = append(lines, "Still stuck? Report a bug: https://github.com/sgx-labs/statelessagent/issues")
	}
	cli.Box(lines)

	cli.Footer()

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}
