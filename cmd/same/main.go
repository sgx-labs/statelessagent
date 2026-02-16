// Package main is the entrypoint for the SAME CLI.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
)

// Version is set at build time via ldflags.
var Version = "dev"

// compareSemver compares two semver strings (without "v" prefix).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Falls back to string comparison if parsing fails.
func compareSemver(a, b string) int {
	parseSemver := func(s string) (major, minor, patch int, ok bool) {
		// Strip any pre-release suffix (e.g., "1.2.3-beta")
		if idx := strings.IndexByte(s, '-'); idx >= 0 {
			s = s[:idx]
		}
		parts := strings.Split(s, ".")
		if len(parts) < 1 || len(parts) > 3 {
			return 0, 0, 0, false
		}
		var err error
		major, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, 0, false
		}
		if len(parts) >= 2 {
			minor, err = strconv.Atoi(parts[1])
			if err != nil {
				return 0, 0, 0, false
			}
		}
		if len(parts) >= 3 {
			patch, err = strconv.Atoi(parts[2])
			if err != nil {
				return 0, 0, 0, false
			}
		}
		return major, minor, patch, true
	}

	aMaj, aMin, aPat, aOK := parseSemver(a)
	bMaj, bMin, bPat, bOK := parseSemver(b)

	if !aOK || !bOK {
		// Fallback to string comparison if parsing fails
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}

	if aMaj != bMaj {
		if aMaj < bMaj {
			return -1
		}
		return 1
	}
	if aMin != bMin {
		if aMin < bMin {
			return -1
		}
		return 1
	}
	if aPat != bPat {
		if aPat < bPat {
			return -1
		}
		return 1
	}
	return 0
}

// newEmbedProvider creates an embedding provider from config.
func newEmbedProvider() (embedding.Provider, error) {
	ec := config.EmbeddingProviderConfig()
	cfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
	}

	// For ollama provider, use the legacy [ollama] URL if no base_url is set
	if (cfg.Provider == "ollama" || cfg.Provider == "") && cfg.BaseURL == "" {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			return nil, fmt.Errorf("ollama URL: %w", err)
		}
		cfg.BaseURL = ollamaURL
	}

	return embedding.NewProvider(cfg)
}

func main() {
	root := &cobra.Command{
		Use:     "same",
		Short:   "Give your AI a memory of your project",
		Version: Version,
		Long: `SAME (Stateless Agent Memory Engine) gives your AI a memory.

Your AI will remember your project decisions, your preferences, and what you've
built together across sessions. No more re-explaining everything.

Quick Start:
  same init     Set up SAME for your project (run this first)
  same status   See what SAME is tracking
  same doctor   Check if everything is working

Need help? https://discord.gg/9KfTkcGs7g`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// Command groups for organized --help output
	root.AddGroup(
		&cobra.Group{ID: "start", Title: "Getting Started:"},
		&cobra.Group{ID: "search", Title: "Search & Discovery:"},
		&cobra.Group{ID: "knowledge", Title: "Knowledge Management:"},
		&cobra.Group{ID: "diagnostics", Title: "Diagnostics:"},
		&cobra.Group{ID: "config", Title: "Configuration:"},
		&cobra.Group{ID: "advanced", Title: "Advanced:"},
	)

	addGrouped := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}

	addGrouped("start",
		initCmd(),
		demoCmd(),
		tutorialCmd(),
		seedCmd(),
	)

	addGrouped("search",
		searchCmd(),
		askCmd(),
		relatedCmd(),
		webCmd(),
	)

	addGrouped("knowledge",
		pinCmd(),
		feedbackCmd(),
		claimCmd(),
		vaultCmd(),
		graphCmd(),
	)

	addGrouped("diagnostics",
		statusCmd(),
		doctorCmd(),
		logCmd(),
		hooksCmd(),
	)

	addGrouped("config",
		configCmd(),
		displayCmd(),
		profileCmd(),
		modelCmd(),
		setupSubCmd(),
		reindexCmd(),
	)

	addGrouped("advanced",
		mcpCmd(),
		guardCmd(),
		watchCmd(),
		benchCmd(),
		ciCmd(),
	)

	addGrouped("diagnostics",
		statsCmd(),
		repairCmd(),
	)

	addGrouped("config",
		versionCmd(),
		updateCmd(),
	)

	// Internal commands (hidden from --help)
	for _, cmd := range []*cobra.Command{migrateCmd(), hookCmd(), budgetCmd(), pluginCmd(), pushAllowCmd()} {
		cmd.Hidden = true
		root.AddCommand(cmd)
	}

	// Global --vault flag
	root.PersistentFlags().StringVar(&config.VaultOverride, "vault", "", "Vault name or path (overrides auto-detect)")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

func formatRelevance(score float64) string {
	// score is 0-1, higher is better
	stars := int(score * 5)
	if stars > 5 {
		stars = 5
	}
	if stars < 1 {
		stars = 1
	}
	filled := strings.Repeat("★", stars)
	empty := strings.Repeat("☆", 5-stars)

	var label string
	switch {
	case score >= 0.90:
		label = "Excellent match"
	case score >= 0.70:
		label = "Strong match"
	case score >= 0.50:
		label = "Good match"
	default:
		label = "Weak match"
	}

	return fmt.Sprintf("%s%s %s", filled, empty, label)
}

// ---------- error helpers ----------

type sameError struct {
	message string
	hint    string
}

func (e *sameError) Error() string {
	return fmt.Sprintf("%s\n  Hint: %s", e.message, e.hint)
}

func userError(message, hint string) error {
	return &sameError{message: message, hint: hint}
}
