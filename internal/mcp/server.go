// Package mcp implements the MCP server for SAME.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

var (
	db              *store.DB
	embedClient     embedding.Provider
	lastReindexTime time.Time
	reindexMu       sync.Mutex
	vaultRoot       string
)

const reindexCooldown = 60 * time.Second

// Version is set by the caller (main) before calling Serve.
var Version = "dev"

// Serve starts the MCP server on stdio.
func Serve() error {
	var err error
	// Propagate config-driven noise paths to the store package for ranking filters.
	store.NoisePaths = config.NoisePaths()

	db, err = store.Open()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	ec := config.EmbeddingProviderConfig()
	ollamaURL, err := config.OllamaURL()
	if err != nil {
		return fmt.Errorf("ollama URL: %w", err)
	}
	embedClient, err = embedding.NewProvider(embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ollamaURL,
		Dimensions: ec.Dimensions,
	})
	if err != nil {
		return fmt.Errorf("embedding provider: %w", err)
	}
	vaultRoot, _ = filepath.Abs(config.VaultPath())

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "same",
		Version: Version,
	}, nil)

	registerTools(server)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

func registerTools(server *mcp.Server) {
	// search_notes
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_notes",
		Description: "Search the user's knowledge base for relevant notes, decisions, and context. Use this when you need background on a topic, want to find prior decisions, or need to understand project architecture.\n\nArgs:\n  query: Natural language search query (e.g. 'authentication approach', 'database schema decisions')\n  top_k: Number of results (default 10, max 100)\n\nReturns ranked list of matching notes with titles, paths, and text snippets.",
	}, handleSearchNotes)

	// search_notes_filtered
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_notes_filtered",
		Description: "Search the user's knowledge base with metadata filters. Use this when you want to narrow results by domain (e.g. 'engineering'), workstream (e.g. 'api-redesign'), or tags.\n\nArgs:\n  query: Natural language search query\n  top_k: Number of results (default 10, max 100)\n  domain: Filter by domain (e.g. 'engineering', 'product')\n  workstream: Filter by workstream/project name\n  tags: Comma-separated tags to filter by\n\nReturns filtered ranked list.",
	}, handleSearchNotesFiltered)

	// get_note
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_note",
		Description: "Read the full content of a note. Use this after search_notes returns a relevant result and you need the complete text. Paths are relative to the vault root.\n\nArgs:\n  path: Relative path from vault root (as returned by search_notes)\n\nReturns full markdown text content.",
	}, handleGetNote)

	// find_similar_notes
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_similar_notes",
		Description: "Find notes that cover similar topics to a given note. Use this to discover related context, find notes that might conflict, or build a broader picture of a topic.\n\nArgs:\n  path: Relative path of the source note\n  top_k: Number of similar notes (default 5, max 100)\n\nReturns list of related notes ranked by similarity.",
	}, handleFindSimilar)

	// reindex
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reindex",
		Description: "Re-scan and re-index all markdown notes. Use this if the user has added or changed notes and search results seem stale. Incremental by default (only re-embeds changed files).\n\nArgs:\n  force: Re-embed all files regardless of changes (default false)\n\nReturns indexing statistics.",
	}, handleReindex)

	// index_stats
	mcp.AddTool(server, &mcp.Tool{
		Name:        "index_stats",
		Description: "Check the health and size of the note index. Use this to verify the index is up to date or to report stats to the user.\n\nReturns note count, chunk count, last indexed timestamp, embedding model info, and database size.",
	}, handleIndexStats)
}

// Tool input types

type searchInput struct {
	Query string `json:"query" jsonschema:"Natural language search query"`
	TopK  int    `json:"top_k" jsonschema:"Number of results (default 10, max 100)"`
}

type searchFilteredInput struct {
	Query      string `json:"query" jsonschema:"Natural language search query"`
	TopK       int    `json:"top_k" jsonschema:"Number of results (default 10, max 100)"`
	Domain     string `json:"domain,omitempty" jsonschema:"Filter by domain"`
	Workstream string `json:"workstream,omitempty" jsonschema:"Filter by workstream"`
	Tags       string `json:"tags,omitempty" jsonschema:"Comma-separated tags to filter by"`
}

type getInput struct {
	Path string `json:"path" jsonschema:"Relative path from vault root"`
}

type similarInput struct {
	Path string `json:"path" jsonschema:"Relative path of the source note"`
	TopK int    `json:"top_k" jsonschema:"Number of similar notes (default 5, max 100)"`
}

type reindexInput struct {
	Force bool `json:"force" jsonschema:"Re-embed all files regardless of changes"`
}

type emptyInput struct{}

// Tool handlers

func handleSearchNotes(ctx context.Context, req *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
	topK := clampTopK(input.TopK, 10)

	queryVec, err := embedClient.GetQueryEmbedding(input.Query)
	if err != nil {
		return textResult(fmt.Sprintf("Error embedding query: %v", err)), nil, nil
	}

	results, err := db.VectorSearch(queryVec, store.SearchOptions{TopK: topK})
	if err != nil {
		return textResult(fmt.Sprintf("Search error: %v", err)), nil, nil
	}
	results = filterPrivatePaths(results)
	if len(results) == 0 {
		return textResult("No results found. The index may be empty — try running reindex() first."), nil, nil
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleSearchNotesFiltered(ctx context.Context, req *mcp.CallToolRequest, input searchFilteredInput) (*mcp.CallToolResult, any, error) {
	topK := clampTopK(input.TopK, 10)

	queryVec, err := embedClient.GetQueryEmbedding(input.Query)
	if err != nil {
		return textResult(fmt.Sprintf("Error embedding query: %v", err)), nil, nil
	}

	var tags []string
	if input.Tags != "" {
		for _, t := range strings.Split(input.Tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}

	results, err := db.VectorSearch(queryVec, store.SearchOptions{
		TopK:       topK,
		Domain:     input.Domain,
		Workstream: input.Workstream,
		Tags:       tags,
	})
	if err != nil {
		return textResult(fmt.Sprintf("Search error: %v", err)), nil, nil
	}
	results = filterPrivatePaths(results)
	if len(results) == 0 {
		return textResult("No results found matching the filters."), nil, nil
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleGetNote(ctx context.Context, req *mcp.CallToolRequest, input getInput) (*mcp.CallToolResult, any, error) {
	safePath := safeVaultPath(input.Path)
	if safePath == "" {
		return textResult("Error: path must be a relative path within the vault."), nil, nil
	}

	content, err := os.ReadFile(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return textResult("File not found."), nil, nil
		}
		return textResult("Error reading file."), nil, nil
	}

	return textResult(string(content)), nil, nil
}

func handleFindSimilar(ctx context.Context, req *mcp.CallToolRequest, input similarInput) (*mcp.CallToolResult, any, error) {
	topK := clampTopK(input.TopK, 5)

	noteVec, err := db.GetNoteEmbedding(input.Path)
	if err != nil || noteVec == nil {
		return textResult(fmt.Sprintf("No similar notes found for: %s. Is the note in the index?", input.Path)), nil, nil
	}

	// Fetch extra results, excluding the source note
	allResults, err := db.VectorSearch(noteVec, store.SearchOptions{TopK: topK + 10})
	if err != nil {
		return textResult(fmt.Sprintf("Search error: %v", err)), nil, nil
	}

	allResults = filterPrivatePaths(allResults)
	var results []store.SearchResult
	for _, r := range allResults {
		if r.Path != input.Path {
			results = append(results, r)
		}
		if len(results) >= topK {
			break
		}
	}

	if len(results) == 0 {
		return textResult(fmt.Sprintf("No similar notes found for: %s.", input.Path)), nil, nil
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleReindex(ctx context.Context, req *mcp.CallToolRequest, input reindexInput) (*mcp.CallToolResult, any, error) {
	reindexMu.Lock()
	defer reindexMu.Unlock()

	if time.Since(lastReindexTime) < reindexCooldown {
		remaining := int(reindexCooldown.Seconds() - time.Since(lastReindexTime).Seconds())
		data, _ := json.Marshal(map[string]string{
			"error": fmt.Sprintf("Reindex cooldown active. Try again in %ds.", remaining),
		})
		return textResult(string(data)), nil, nil
	}
	lastReindexTime = time.Now()

	stats, err := indexer.Reindex(db, input.Force)
	if err != nil {
		return textResult(fmt.Sprintf("Reindex error: %v", err)), nil, nil
	}

	data, _ := json.MarshalIndent(stats, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleIndexStats(ctx context.Context, req *mcp.CallToolRequest, input emptyInput) (*mcp.CallToolResult, any, error) {
	stats := indexer.GetStats(db)
	data, _ := json.MarshalIndent(stats, "", "  ")
	return textResult(string(data)), nil, nil
}

// Helpers

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func clampTopK(topK, defaultVal int) int {
	if topK <= 0 {
		return defaultVal
	}
	if topK > 100 {
		return 100
	}
	return topK
}

// safeVaultPath resolves a relative path within the vault, blocking traversal attacks
// and access to _PRIVATE/ content.
func safeVaultPath(path string) string {
	if filepath.IsAbs(path) {
		return ""
	}
	// SECURITY: block access to _PRIVATE/ directory
	clean := filepath.ToSlash(filepath.Clean(path))
	if strings.HasPrefix(clean, "_PRIVATE/") || clean == "_PRIVATE" {
		return ""
	}
	full, err := filepath.Abs(filepath.Join(config.VaultPath(), filepath.FromSlash(path)))
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(full, vaultRoot+string(filepath.Separator)) && full != vaultRoot {
		return ""
	}
	return full
}

// filterPrivatePaths removes _PRIVATE/ results from search output (defense-in-depth).
func filterPrivatePaths(results []store.SearchResult) []store.SearchResult {
	filtered := results[:0]
	for _, r := range results {
		if !strings.HasPrefix(r.Path, "_PRIVATE/") && !strings.HasPrefix(r.Path, "_PRIVATE\\") {
			filtered = append(filtered, r)
		}
	}
	return filtered
}
