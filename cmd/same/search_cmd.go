package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func searchCmd() *cobra.Command {
	var (
		topK      int
		domain    string
		jsonOut   bool
		verbose   bool
		allVaults bool
		vaults    string
	)
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the vault from the command line",
		Long: `Search the current vault, or search across multiple vaults.

Examples:
  same search "authentication approach"
  same search --all "JWT patterns"
  same search --vaults dev,marketing "launch timeline"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			if allVaults || vaults != "" {
				return runFederatedSearch(query, topK, domain, jsonOut, verbose, allVaults, vaults)
			}
			return runSearch(query, topK, domain, jsonOut, verbose)
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 5, "Number of results")
	cmd.Flags().StringVar(&domain, "domain", "", "Filter by domain")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show raw scores for debugging")
	cmd.Flags().BoolVar(&allVaults, "all", false, "Search across all registered vaults")
	cmd.Flags().StringVar(&vaults, "vaults", "", "Comma-separated vault aliases to search")
	return cmd
}

func runSearch(query string, topK int, domain string, jsonOut bool, verbose bool) error {
	if strings.TrimSpace(query) == "" {
		return userError("Empty search query", "Provide a search term: same search \"your query\"")
	}
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	// Detect lite mode (no vectors) and fall back to FTS5
	var results []store.SearchResult
	if !db.HasVectors() {
		if db.FTSAvailable() {
			results, err = db.FTS5Search(query, store.SearchOptions{TopK: topK, Domain: domain})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
			if !jsonOut && len(results) > 0 {
				fmt.Printf("  %s(keyword search — install Ollama for semantic search)%s\n", cli.Dim, cli.Reset)
			}
		} else {
			return userError("No search index available", "run 'same reindex' with Ollama running for best results")
		}
	} else {
		client, err := newEmbedProvider()
		if err != nil {
			// Ollama went down — try FTS5 fallback
			if db.FTSAvailable() {
				results, err = db.FTS5Search(query, store.SearchOptions{TopK: topK, Domain: domain})
				if err != nil {
					return fmt.Errorf("search: %w", err)
				}
				if !jsonOut && len(results) > 0 {
					fmt.Printf("  %s(keyword fallback — Ollama not available)%s\n", cli.Dim, cli.Reset)
				}
			} else {
				return fmt.Errorf("can't connect to embedding provider — is Ollama running? (%w)", err)
			}
		} else {
			if mismatchErr := db.CheckEmbeddingMeta(client.Name(), client.Model(), client.Dimensions()); mismatchErr != nil {
				return mismatchErr
			}

			queryVec, err := client.GetQueryEmbedding(query)
			if err != nil {
				return fmt.Errorf("embed query: %w", err)
			}

			results, err = db.HybridSearch(queryVec, query, store.SearchOptions{
				TopK:   topK,
				Domain: domain,
			})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
	}

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, r := range results {
		typeTag := ""
		if r.ContentType != "" && r.ContentType != "note" {
			typeTag = fmt.Sprintf(" [%s]", r.ContentType)
		}

		fmt.Printf("\n%d. %s%s\n", i+1, r.Title, typeTag)
		fmt.Printf("   %s\n", r.Path)
		if verbose {
			fmt.Printf("   Score: %.3f  Distance: %.1f  Confidence: %.3f\n", r.Score, r.Distance, r.Confidence)
		} else {
			fmt.Printf("   Match: %s\n", formatRelevance(r.Score))
		}

		// Show first 150 chars of snippet
		snippet := r.Snippet
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		// Replace newlines with spaces for compact display
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", "")
		fmt.Printf("   %s\n", snippet)
	}
	fmt.Println()

	if !jsonOut {
		reg := config.LoadRegistry()
		if len(reg.Vaults) >= 2 {
			fmt.Printf("  %sSearching 1 vault. Use --all to search %d vaults.%s\n", cli.Dim, len(reg.Vaults), cli.Reset)
		}
	}

	return nil
}

func runFederatedSearch(query string, topK int, domain string, jsonOut bool, verbose bool, allVaults bool, vaultsFlag string) error {
	if strings.TrimSpace(query) == "" {
		return userError("Empty search query", "Provide a search term: same search --all \"your query\"")
	}

	// Resolve which vaults to search
	reg := config.LoadRegistry()
	vaultDBPaths := make(map[string]string)

	if allVaults {
		for alias, vaultPath := range reg.Vaults {
			dbPath := vaultDBPath(vaultPath)
			if _, err := os.Stat(dbPath); err == nil {
				vaultDBPaths[alias] = dbPath
			}
		}
	} else {
		for _, alias := range strings.Split(vaultsFlag, ",") {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			resolved := reg.ResolveVault(alias)
			if resolved == "" {
				fmt.Fprintf(os.Stderr, "Warning: vault %q not found, skipping\n", alias)
				continue
			}
			dbPath := vaultDBPath(resolved)
			if _, err := os.Stat(dbPath); err == nil {
				vaultDBPaths[alias] = dbPath
			} else {
				fmt.Fprintf(os.Stderr, "Warning: no database for vault %q, skipping\n", alias)
			}
		}
	}

	if len(vaultDBPaths) == 0 {
		return userError("No searchable vaults found",
			"Register vaults with 'same vault add <name> <path>' and ensure they have been indexed.")
	}

	// Try to get query embedding for vector search
	var queryVec []float32
	client, err := newEmbedProvider()
	if err == nil {
		queryVec, _ = client.GetQueryEmbedding(query)
	}

	results, err := store.FederatedSearch(vaultDBPaths, queryVec, query, store.SearchOptions{
		TopK:   topK,
		Domain: domain,
	})
	if err != nil {
		return fmt.Errorf("federated search: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Printf("No results found across %d vault(s).\n", len(vaultDBPaths))
		return nil
	}

	if queryVec == nil {
		fmt.Printf("  %s(keyword search — install Ollama for semantic search)%s\n", cli.Dim, cli.Reset)
	}

	for i, r := range results {
		typeTag := ""
		if r.ContentType != "" && r.ContentType != "note" {
			typeTag = fmt.Sprintf(" [%s]", r.ContentType)
		}

		fmt.Printf("\n%d. %s%s  %s[%s]%s\n", i+1, r.Title, typeTag, cli.Dim, r.Vault, cli.Reset)
		fmt.Printf("   %s\n", r.Path)
		if verbose {
			fmt.Printf("   Score: %.3f  Distance: %.1f  Confidence: %.3f\n", r.Score, r.Distance, r.Confidence)
		} else {
			fmt.Printf("   Match: %s\n", formatRelevance(r.Score))
		}

		snippet := r.Snippet
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", "")
		fmt.Printf("   %s\n", snippet)
	}
	fmt.Println()

	return nil
}

// vaultDBPath returns the database file path for a given vault root directory.
func vaultDBPath(vaultRoot string) string {
	return filepath.Join(vaultRoot, ".same", "data", "vault.db")
}

func relatedCmd() *cobra.Command {
	var (
		topK    int
		jsonOut bool
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "related [note-path]",
		Short: "Find notes related to a given note",
		Long:  "Find notes related to a specific vault note using its stored embedding. Path is relative to vault root.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelated(args[0], topK, jsonOut, verbose)
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 5, "Number of related notes to show")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show raw scores for debugging")
	return cmd
}

func runRelated(notePath string, topK int, jsonOut bool, verbose bool) error {
	db, err := store.Open()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Check for embedding model/dimension mismatch
	client, err := newEmbedProvider()
	if err != nil {
		return fmt.Errorf("embedding provider: %w", err)
	}
	if mismatchErr := db.CheckEmbeddingMeta(client.Name(), client.Model(), client.Dimensions()); mismatchErr != nil {
		return mismatchErr
	}

	// Get the stored embedding for this note
	noteVec, err := db.GetNoteEmbedding(notePath)
	if err != nil {
		return fmt.Errorf("get embedding: %w", err)
	}
	if noteVec == nil {
		return fmt.Errorf("note not found in index: %s", notePath)
	}

	// Search for similar notes, requesting extra to filter out the source note
	results, err := db.VectorSearch(noteVec, store.SearchOptions{
		TopK: topK + 3,
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	// Filter out the source note itself
	var filtered []store.SearchResult
	for _, r := range results {
		if r.Path != notePath {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) > topK {
		filtered = filtered[:topK]
	}

	if jsonOut {
		data, _ := json.MarshalIndent(filtered, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(filtered) == 0 {
		fmt.Println("No related notes found.")
		return nil
	}

	fmt.Printf("\nNotes related to: %s\n", notePath)
	for i, r := range filtered {
		typeTag := ""
		if r.ContentType != "" && r.ContentType != "note" {
			typeTag = fmt.Sprintf(" [%s]", r.ContentType)
		}

		fmt.Printf("\n%d. %s%s\n", i+1, r.Title, typeTag)
		fmt.Printf("   %s\n", r.Path)
		if verbose {
			fmt.Printf("   Score: %.3f  Distance: %.1f\n", r.Score, r.Distance)
		} else {
			fmt.Printf("   Match: %s\n", formatRelevance(r.Score))
		}

		snippet := r.Snippet
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", "")
		fmt.Printf("   %s\n", snippet)
	}
	fmt.Println()

	return nil
}
