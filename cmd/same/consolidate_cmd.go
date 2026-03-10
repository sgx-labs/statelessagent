package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/consolidate"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func consolidateCmd() *cobra.Command {
	var (
		dryRun    bool
		threshold float64
	)
	cmd := &cobra.Command{
		Use:   "consolidate",
		Short: "Extract and merge knowledge from your notes",
		Long: `Analyze your vault for related notes and consolidate them into structured knowledge.

Consolidation:
  * Finds clusters of related notes using semantic similarity
  * Extracts key facts, decisions, and patterns using your LLM
  * Detects and resolves contradictions (newest information wins)
  * Writes consolidated knowledge to knowledge/ directory
  * NEVER modifies or deletes your original notes

Requires an LLM provider (Ollama or OpenAI). Run 'same init' to configure one.

Examples:
  same consolidate              Consolidate with defaults
  same consolidate --dry-run    Preview what would be consolidated
  same consolidate --threshold 0.8  Require higher similarity to group`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConsolidate(dryRun, threshold)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview consolidation without writing files")
	cmd.Flags().Float64Var(&threshold, "threshold", 0.75, "Similarity threshold for grouping notes (0.0-1.0)")
	return cmd
}

func runConsolidate(dryRun bool, threshold float64) error {
	// 1. Open database
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// 2. Create LLM client
	chat, err := llm.NewClient()
	if err != nil {
		return userError(
			"Consolidation requires an LLM",
			"Run 'same init' to configure Ollama or OpenAI.",
		)
	}

	// 3. Create embedding provider (optional — engine handles nil)
	var embedClient consolidate.EmbedProvider
	ep, epErr := newEmbedProvider()
	if epErr == nil {
		embedClient = ep
	}

	// 4. Pick best model
	model, err := chat.PickBestModel()
	if err != nil {
		if chat.Provider() == "ollama" {
			return userError(
				"No chat model available",
				"Start Ollama or set SAME_CHAT_PROVIDER=openai/openai-compatible, then retry.",
			)
		}
		return userError(
			fmt.Sprintf("Can't list models from %s provider", chat.Provider()),
			"Check that your provider has at least one chat model installed. For Ollama: ollama pull llama3.2",
		)
	}
	if model == "" {
		return userError(
			"No chat model found",
			"Set SAME_CHAT_MODEL explicitly or install/configure at least one chat-capable model.",
		)
	}

	// 5. Create consolidation engine
	vaultPath := config.VaultPath()
	engine := consolidate.NewEngine(db, chat, embedClient, model, vaultPath, threshold)

	// 6. Run consolidation
	if dryRun {
		fmt.Printf("\n  %s── Consolidation Preview ─────────────────%s\n", cli.Cyan, cli.Reset)
	} else {
		fmt.Printf("\n  %s── Consolidating ─────────────────────────%s\n", cli.Cyan, cli.Reset)
	}

	result, err := engine.Run(dryRun)
	if err != nil {
		return fmt.Errorf("consolidation failed: %w", err)
	}

	if result.GroupsFound == 0 {
		fmt.Printf("\n  No groups of related notes found.\n")
		fmt.Printf("  %sTry lowering the threshold or adding more notes to your vault.%s\n\n", cli.Dim, cli.Reset)
		return nil
	}

	// 7. Display results
	if dryRun {
		printConsolidatePreview(result)
	} else {
		printConsolidateResult(result)
	}

	return nil
}

func printConsolidatePreview(result *consolidate.Result) {
	fmt.Printf("\n  Found %d groups of related notes\n", result.GroupsFound)

	for i, g := range result.Groups {
		fmt.Printf("\n  %sGroup %d:%s %q\n", cli.Bold, i+1, cli.Reset, g.Theme)
		for _, src := range g.SourceNotes {
			dateStr := formatNoteDate(src.Modified)
			fmt.Printf("    %s*%s %s %s(%s)%s\n", cli.Dim, cli.Reset, src.Path, cli.Dim, dateStr, cli.Reset)
		}
		relPath, _ := filepath.Rel(config.VaultPath(), g.OutputPath)
		if relPath == "" {
			relPath = g.OutputPath
		}
		fmt.Printf("    %s->%s Would create: %s%s%s\n", cli.Yellow, cli.Reset, cli.Bold, relPath, cli.Reset)
	}

	fmt.Printf("\n  %d groups, %d notes -> %d consolidated knowledge files\n",
		result.GroupsFound, result.NotesProcessed, result.GroupsFound)
	fmt.Printf("\n  %sRun without --dry-run to consolidate.%s\n\n", cli.Dim, cli.Reset)
}

func printConsolidateResult(result *consolidate.Result) {
	fmt.Printf("\n  Analyzing %d notes...\n", result.NotesProcessed)
	fmt.Printf("  Found %d groups of related notes\n", result.GroupsFound)

	for _, g := range result.Groups {
		relPath, _ := filepath.Rel(config.VaultPath(), g.OutputPath)
		if relPath == "" {
			relPath = g.OutputPath
		}

		conflictMsg := "0 conflicts"
		if len(g.Conflicts) == 1 {
			conflictMsg = "1 conflict resolved"
		} else if len(g.Conflicts) > 1 {
			conflictMsg = fmt.Sprintf("%d conflicts resolved", len(g.Conflicts))
		}

		fmt.Printf("\n  %s%s✓%s %s\n", cli.Green, cli.Bold, cli.Reset, relPath)
		fmt.Printf("    %d facts extracted, %s\n", len(g.Facts), conflictMsg)

		var sourcePaths []string
		for _, src := range g.SourceNotes {
			sourcePaths = append(sourcePaths, src.Path)
		}
		fmt.Printf("    %sSources: %s%s\n", cli.Dim, strings.Join(sourcePaths, ", "), cli.Reset)
	}

	// Summary
	fmt.Printf("\n  %s── Summary ───────────────────────────────%s\n", cli.Cyan, cli.Reset)
	fmt.Printf("\n  %d groups consolidated\n", result.GroupsFound)
	fmt.Printf("  %d facts extracted\n", result.FactsExtracted)
	if result.ConflictsFound == 1 {
		fmt.Printf("  1 conflict resolved\n")
	} else {
		fmt.Printf("  %d conflicts resolved\n", result.ConflictsFound)
	}
	fmt.Printf("  %d knowledge files created in knowledge/\n", result.NotesCreated)
	fmt.Printf("\n  %sYour original notes are untouched.%s\n", cli.Dim, cli.Reset)
	fmt.Printf("  %sRun 'same search' to find consolidated knowledge.%s\n\n", cli.Dim, cli.Reset)
}

// formatNoteDate formats a Unix timestamp (float64) as a short date string.
func formatNoteDate(modified float64) string {
	t := time.Unix(int64(modified), 0).Local()
	return t.Format("Jan 2")
}
