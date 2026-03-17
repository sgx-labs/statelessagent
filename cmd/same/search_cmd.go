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
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func searchCmd() *cobra.Command {
	var (
		topK        int
		domain      string
		trustState  string
		contentType string
		tag         string
		jsonOut     bool
		verbose     bool
		allVaults   bool
		vaults      string
	)
	cmd := &cobra.Command{
		Use:     "search [query]",
		Aliases: []string{"s"},
		Short:   "Search your notes by meaning or keyword",
		Long: `Search the current vault, or search across multiple vaults.
Filter by metadata: trust state, content type, domain, or tags.

Examples:
  same search "authentication approach"
  same search "auth decisions" --trust validated
  same search "deployment pipeline" --type decision
  same search "stale notes" --trust stale
  same search "api design" --domain engineering
  same search "auth" --tag security
  same search --all "JWT patterns"
  same search --vaults dev,marketing "launch timeline"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			var tags []string
			if tag != "" {
				for _, t := range strings.Split(tag, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						tags = append(tags, t)
					}
				}
			}
			if allVaults || vaults != "" {
				return runFederatedSearch(query, topK, domain, trustState, contentType, tags, jsonOut, verbose, allVaults, vaults)
			}
			return runSearch(query, topK, domain, trustState, contentType, tags, jsonOut, verbose)
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 5, "Number of results")
	cmd.Flags().StringVar(&domain, "domain", "", "Filter by domain")
	cmd.Flags().StringVarP(&trustState, "trust", "t", "", "Filter by trust state (validated, stale, contradicted, unknown)")
	cmd.Flags().StringVar(&contentType, "type", "", "Filter by content type (decision, handoff, note, research)")
	cmd.Flags().StringVar(&tag, "tag", "", "Filter by tag (comma-separated for multiple)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show raw scores for debugging")
	cmd.Flags().BoolVar(&allVaults, "all", false, "Search across all registered vaults")
	cmd.Flags().StringVar(&vaults, "vaults", "", "Comma-separated vault aliases to search")
	return cmd
}

func runSearch(query string, topK int, domain string, trustState string, contentType string, tags []string, jsonOut bool, verbose bool) error {
	if strings.TrimSpace(query) == "" {
		return userError("Empty search query", "Provide a search term: same search \"your query\"")
	}
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	searchOpts := store.SearchOptions{
		TopK:        topK,
		Domain:      domain,
		TrustState:  trustState,
		ContentType: contentType,
		Tags:        tags,
	}

	// Detect lite mode (no vectors) and fall back to FTS5/keyword
	var results []store.SearchResult
	if !db.HasVectors() {
		if db.FTSAvailable() {
			results, err = db.FTS5Search(query, searchOpts)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
		// LIKE-based keyword fallback if FTS5 unavailable or returned nothing
		if results == nil {
			terms := store.ExtractSearchTerms(query)
			rawResults, err := db.KeywordSearch(terms, topK)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
			for _, rr := range rawResults {
				snippet := rr.Text
				if len(snippet) > 500 {
					snippet = snippet[:500]
				}
				results = append(results, store.SearchResult{
					Path: rr.Path, Title: rr.Title, Snippet: snippet,
					Domain: rr.Domain, Workstream: rr.Workstream,
					Tags: rr.Tags, ContentType: rr.ContentType, Score: 0.5,
					TrustState: rr.TrustState,
				})
			}
		}
		if !jsonOut && len(results) > 0 {
			fmt.Printf("  %s(keyword search — configure embeddings for semantic search: ollama/openai/openai-compatible)%s\n", cli.Dim, cli.Reset)
			if _, probeErr := newEmbedProvider(); probeErr == nil {
				fmt.Printf("  %sTip: Embedding provider detected! Run %ssame reindex%s to upgrade to semantic search.%s\n",
					cli.Dim, cli.Bold, cli.Reset+cli.Dim, cli.Reset)
			}
		}
	} else {
		client, err := newEmbedProvider()
		if err != nil {
			// Embedding provider unavailable — try FTS5 fallback, then LIKE-based
			if db.FTSAvailable() {
				results, _ = db.FTS5Search(query, searchOpts)
			}
			if results == nil {
				terms := store.ExtractSearchTerms(query)
				rawResults, kwErr := db.KeywordSearch(terms, topK)
				if kwErr == nil {
					for _, rr := range rawResults {
						snippet := rr.Text
						if len(snippet) > 500 {
							snippet = snippet[:500]
						}
						results = append(results, store.SearchResult{
							Path: rr.Path, Title: rr.Title, Snippet: snippet,
							Domain: rr.Domain, Workstream: rr.Workstream,
							Tags: rr.Tags, ContentType: rr.ContentType, Score: 0.5,
							TrustState: rr.TrustState,
						})
					}
				}
			}
			if results == nil {
				return fmt.Errorf("can't connect to embedding provider (ollama/openai/openai-compatible): %w", err)
			}
			if !jsonOut {
				fmt.Printf("  %s(keyword fallback — embedding provider unavailable)%s\n", cli.Dim, cli.Reset)
			}
		} else {
			if mismatchErr := db.CheckEmbeddingMeta(client.Name(), client.Model(), client.Dimensions()); mismatchErr != nil {
				return embedding.HumanizeError(mismatchErr)
			}

			queryVec, err := client.GetQueryEmbedding(query)
			if err != nil {
				return embedding.HumanizeError(fmt.Errorf("embed query: %w", err))
			}

			results, err = db.HybridSearch(queryVec, query, searchOpts)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
	}

	if len(results) == 0 {
		if jsonOut {
			fmt.Println("[]")
			return nil
		}
		noteCount, _ := db.NoteCount()
		if noteCount < 5 {
			fmt.Printf("\n  No results found. Your vault has only %d notes.\n", noteCount)
			fmt.Printf("  Add more markdown files and run %ssame reindex%s, or try %ssame seed list%s for starter content.\n\n",
				cli.Bold, cli.Reset, cli.Bold, cli.Reset)
		} else {
			fmt.Printf("\n  No results found. Try broader terms, or run %ssame reindex%s to update your vault.\n",
				cli.Bold, cli.Reset)
			fmt.Printf("  You can also try %ssame search --all%s to search across all vaults.\n\n",
				cli.Bold, cli.Reset)
		}
		return nil
	}

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
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
			fmt.Printf("   Relevance: %.0f%%  Distance: %.1f  Confidence: %.0f%%\n",
				r.Score*100, r.Distance, r.Confidence*100)
		} else {
			fmt.Printf("   Match: %s\n", formatRelevance(r.Score))
		}
		if trustLine := formatTrustState(r.TrustState); trustLine != "" {
			fmt.Printf("   %s\n", trustLine)
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
		if len(results) > 0 {
			fmt.Printf("  %sExplore related: same related %s%s\n", cli.Dim, results[0].Path, cli.Reset)
		}
		if len(results) < 3 {
			fmt.Printf("  %sTip: run 'same ask \"<your question>\"' for AI-powered answers with citations%s\n", cli.Dim, cli.Reset)
		}
	}

	// Reconsolidation: increment access counts for surfaced notes (fire-and-forget).
	if len(results) > 0 {
		paths := make([]string, len(results))
		for i, r := range results {
			paths[i] = r.Path
		}
		_ = db.IncrementAccessCount(paths)
	}

	return nil
}

func runFederatedSearch(query string, topK int, domain string, trustState string, contentType string, tags []string, jsonOut bool, verbose bool, allVaults bool, vaultsFlag string) error {
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
				fmt.Fprintf(os.Stderr, "Warning: vault %q has no index — run 'same reindex' in that vault\n", alias)
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
		TopK:        topK,
		Domain:      domain,
		TrustState:  trustState,
		ContentType: contentType,
		Tags:        tags,
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
		fmt.Printf("\n  No results found across %d vault(s).\n", len(vaultDBPaths))
		fmt.Printf("  %sTry a different query or run 'same reindex' in each vault.%s\n\n", cli.Dim, cli.Reset)
		return nil
	}

	if queryVec == nil {
		fmt.Printf("  %s(keyword search — configure embeddings for semantic search: ollama/openai/openai-compatible)%s\n", cli.Dim, cli.Reset)
	}

	for i, r := range results {
		typeTag := ""
		if r.ContentType != "" && r.ContentType != "note" {
			typeTag = fmt.Sprintf(" [%s]", r.ContentType)
		}

		fmt.Printf("\n%d. %s%s  %s[%s]%s\n", i+1, r.Title, typeTag, cli.Dim, r.Vault, cli.Reset)
		fmt.Printf("   %s\n", r.Path)
		if verbose {
			fmt.Printf("   Relevance: %.0f%%  Distance: %.1f  Confidence: %.0f%%\n",
				r.Score*100, r.Distance, r.Confidence*100)
		} else {
			fmt.Printf("   Match: %s\n", formatRelevance(r.Score))
		}
		if trustLine := formatTrustState(r.TrustState); trustLine != "" {
			fmt.Printf("   %s\n", trustLine)
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
		Use:     "related [note-path]",
		Short:   "Find notes related to a given note",
		Long:    "Find notes similar to a given note. SAME uses the note's embedding to find semantically related content in your vault.",
		Example: `  same related "architecture.md"`,
		Args:    cobra.ExactArgs(1),
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
		return dbOpenError(err)
	}
	defer db.Close()

	// Check for embedding model/dimension mismatch
	client, err := newEmbedProvider()
	if err != nil {
		return userError(
			"Finding related notes requires embeddings",
			"Configure an embedding provider (ollama/openai/openai-compatible) and run 'same reindex'.",
		)
	}
	if mismatchErr := db.CheckEmbeddingMeta(client.Name(), client.Model(), client.Dimensions()); mismatchErr != nil {
		return embedding.HumanizeError(mismatchErr)
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
		if trustLine := formatTrustState(r.TrustState); trustLine != "" {
			fmt.Printf("   %s\n", trustLine)
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

func staleCmd() *cobra.Command {
	var (
		topK    int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "stale",
		Short: "List notes with stale trust state",
		Long: `Show all notes marked as stale — their source material has changed
since the note was written. Equivalent to: same search --trust stale

Use this to find notes that may need review or updating.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStale(topK, jsonOut)
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 20, "Maximum number of results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runStale(topK int, jsonOut bool) error {
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	results, err := db.MetadataFilterSearch(store.SearchOptions{
		TopK:       topK,
		TrustState: "stale",
	})
	if err != nil {
		return fmt.Errorf("stale search: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Printf("\n  %sNo stale notes found. Your memory is up to date.%s\n\n", cli.Green, cli.Reset)
		return nil
	}

	fmt.Printf("\n  %s%d stale note(s)%s — source material has changed since these were written:\n", cli.Yellow, len(results), cli.Reset)
	for i, r := range results {
		typeTag := ""
		if r.ContentType != "" && r.ContentType != "note" {
			typeTag = fmt.Sprintf(" [%s]", r.ContentType)
		}

		fmt.Printf("\n%d. %s%s\n", i+1, r.Title, typeTag)
		fmt.Printf("   %s\n", r.Path)
		fmt.Printf("   Trust: %sstale%s\n", cli.Yellow, cli.Reset)

		snippet := r.Snippet
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", "")
		fmt.Printf("   %s\n", snippet)
	}
	fmt.Printf("\n  %sTip: Review and update these notes, then run 'same reindex' to refresh trust state.%s\n\n", cli.Dim, cli.Reset)

	return nil
}
