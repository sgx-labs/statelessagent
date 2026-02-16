package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgx-labs/statelessagent/internal/cli"
	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/ollama"
	"github.com/sgx-labs/statelessagent/internal/store"
)

func askCmd() *cobra.Command {
	var model string
	var topK int
	cmd := &cobra.Command{
		Use:   "ask [question]",
		Short: "Ask a question and get answers from your notes",
		Long: `Ask a natural language question and get an answer synthesized from your
indexed notes using a local LLM via Ollama.

Requires a chat model installed in Ollama (e.g., llama3.2, mistral, qwen2.5).
SAME will auto-detect the best available model.

Examples:
  same ask "what did we decide about authentication?"
  same ask "how does the deployment process work?"
  same ask "what are our coding standards?" --model mistral`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAsk(args[0], model, topK)
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Ollama model to use (auto-detected if empty)")
	cmd.Flags().IntVar(&topK, "top-k", 5, "Number of notes to use as context")
	return cmd
}

func runAsk(question, model string, topK int) error {
	if strings.TrimSpace(question) == "" {
		return userError("Empty question", "Ask something: same ask \"what did we decide about auth?\"")
	}
	// 1. Open database
	db, err := store.Open()
	if err != nil {
		return config.ErrNoDatabase
	}
	defer db.Close()

	fmt.Printf("\n  %s⦿%s Searching your notes...\n", cli.Cyan, cli.Reset)

	// 2. Search — vector if available, FTS5 fallback, LIKE-based last resort
	var results []store.SearchResult
	if db.HasVectors() {
		embedClient, err := newEmbedProvider()
		if err != nil {
			// Ollama down — try FTS5, then LIKE-based keyword
			if db.FTSAvailable() {
				results, _ = db.FTS5Search(question, store.SearchOptions{TopK: topK})
			}
			if results == nil {
				terms := store.ExtractSearchTerms(question)
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
						})
					}
				}
			}
			if len(results) == 0 {
				return fmt.Errorf("can't connect to embedding provider — is Ollama running? (%w)", err)
			}
		} else {
			queryVec, err := embedClient.GetQueryEmbedding(question)
			if err != nil {
				return fmt.Errorf("embed query: %w", err)
			}
			results, err = db.VectorSearch(queryVec, store.SearchOptions{TopK: topK})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
	} else {
		// No vectors — try FTS5, then LIKE-based keyword
		if db.FTSAvailable() {
			results, err = db.FTS5Search(question, store.SearchOptions{TopK: topK})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
		if results == nil {
			terms := store.ExtractSearchTerms(question)
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
					})
				}
			}
		}
	}

	if len(results) == 0 {
		fmt.Printf("\n  No relevant notes found. Try indexing your notes first: same reindex\n\n")
		return nil
	}

	// 3. Connect to Ollama LLM
	llm, err := ollama.NewClient()
	if err != nil {
		return userError(
			"Ollama is not running",
			"same ask requires Ollama for answers. Start Ollama and try again, or install from: https://ollama.com",
		)
	}

	// 4. Pick model
	if model == "" {
		model, err = llm.PickBestModel()
		if err != nil {
			return fmt.Errorf("can't list Ollama models: %w", err)
		}
		if model == "" {
			return userError(
				"No chat model found in Ollama",
				"Install one with: ollama pull llama3.2",
			)
		}
	}

	fmt.Printf("  %s⦿%s Thinking with %s (%d sources)...\n", cli.Cyan, cli.Reset, model, len(results))

	// 6. Build context from search results
	var context strings.Builder
	for i, r := range results {
		context.WriteString(fmt.Sprintf("--- Source %d: %s (%s) ---\n", i+1, r.Title, r.Path))
		snippet := r.Snippet
		if len(snippet) > 1000 {
			snippet = snippet[:1000]
		}
		context.WriteString(snippet)
		context.WriteString("\n\n")
	}

	// 7. Build prompt
	prompt := fmt.Sprintf(`You are a helpful assistant that answers questions using ONLY the provided notes.
If the notes don't contain enough information to answer, say so honestly.
Always cite which source(s) you used.

NOTES:
%s
QUESTION: %s

Answer concisely, citing sources by name:`, context.String(), question)

	// 8. Generate answer
	answer, err := llm.Generate(model, prompt)
	if err != nil {
		return fmt.Errorf("generate answer: %w", err)
	}

	// 9. Display answer
	fmt.Printf("\n  %s─── Answer ───────────────────────────────%s\n\n", cli.Cyan, cli.Reset)
	// Indent each line of the answer
	for _, line := range strings.Split(answer, "\n") {
		fmt.Printf("  %s\n", line)
	}

	// 10. Show sources
	fmt.Printf("\n  %s─── Sources ──────────────────────────────%s\n\n", cli.Dim, cli.Reset)
	for i, r := range results {
		fmt.Printf("  %d. %s %s(%s)%s\n", i+1, r.Title, cli.Dim, r.Path, cli.Reset)
	}
	fmt.Println()

	return nil
}
