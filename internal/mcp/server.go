// Package mcp implements the MCP server for SAME.
package mcp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	yaml "go.yaml.in/yaml/v3"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/consolidate"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/guard"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

const maxNoteSize = 100 * 1024  // 100KB max note content via MCP
const maxQueryLen = 10_000      // 10K chars max for search queries
const maxReadSize = 1024 * 1024 // 1MB max for get_note reads

var (
	db              *store.DB
	embedClient     embedding.Provider
	lastReindexTime time.Time
	reindexMu       sync.Mutex
	vaultRoot       string
)

const reindexCooldown = 60 * time.Second
const writeRateLimit = 30                // max write operations per minute
const writeRateWindow = 60 * time.Second // rate limit window

// Write rate limiter — prevents rapid write abuse via prompt injection.
var (
	writeTimes []time.Time
	writeMu    sync.Mutex
)

func checkWriteRateLimit() bool {
	writeMu.Lock()
	defer writeMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-writeRateWindow)
	// Prune old entries
	valid := writeTimes[:0]
	for _, t := range writeTimes {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	writeTimes = valid
	if len(writeTimes) >= writeRateLimit {
		return false
	}
	writeTimes = append(writeTimes, now)
	return true
}

// Version is set by the caller (main) before calling Serve.
var Version = "dev"

// InitGlobals opens the vault database and initializes the package-level
// embedding client and vault root. Call once before using NewMCPServer.
// The caller is responsible for closing the returned *store.DB when done.
func InitGlobals() (*store.DB, error) {
	// Propagate config-driven noise paths to the store package for ranking filters.
	store.NoisePaths = config.NoisePaths()

	var err error
	db, err = store.Open()
	if err != nil {
		return nil, fmt.Errorf("SAME vault not initialized. Run 'same init' in your project directory first. (details: %v)", err)
	}

	ec := config.EmbeddingProviderConfig()
	provCfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
		SkipRetry:  !config.IsEmbeddingProviderExplicit(),
	}
	// For ollama provider, use the legacy [ollama] URL if no base_url is set
	if (provCfg.Provider == "ollama" || provCfg.Provider == "") && provCfg.BaseURL == "" {
		ollamaURL, urlErr := config.OllamaURL()
		if urlErr == nil {
			provCfg.BaseURL = ollamaURL
		}
	}
	embedClient, _ = embedding.NewProvider(provCfg)
	// embedClient may be nil if Ollama is not running — search handlers
	// fall back to FTS5/keyword search gracefully.

	// Fail fast on embedding dimension mismatch — prevents garbage search
	// results when the embedding model has changed since last reindex.
	if embedClient != nil {
		if mismatchErr := db.CheckEmbeddingMeta(embedClient.Name(), embedClient.Model(), embedClient.Dimensions()); mismatchErr != nil {
			fmt.Fprintf(os.Stderr, "same: warning: %v\n", mismatchErr)
			embedClient = nil
		}
	}

	vaultRoot, _ = filepath.Abs(config.VaultPath())

	return db, nil
}

// NewMCPServer creates a configured MCP server with all SAME tools registered.
// The caller must call initGlobals() first to initialize the database and
// embedding client. This allows the same server to be used with different
// transports (stdio, Streamable HTTP).
func NewMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "same",
		Version: Version,
	}, nil)

	registerTools(server)

	return server
}

// Serve starts the MCP server on stdio.
func Serve() error {
	openedDB, err := InitGlobals()
	if err != nil {
		return err
	}
	defer openedDB.Close()

	server := NewMCPServer()

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

// refreshEmbedClient re-reads the embedding config and replaces the global
// embedClient. Called after a successful reindex so that subsequent search
// queries use the current model and dimensions (e.g. after a model switch).
func refreshEmbedClient() {
	ec := config.EmbeddingProviderConfig()
	provCfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
		SkipRetry:  !config.IsEmbeddingProviderExplicit(),
	}
	if (provCfg.Provider == "ollama" || provCfg.Provider == "") && provCfg.BaseURL == "" {
		ollamaURL, urlErr := config.OllamaURL()
		if urlErr == nil {
			provCfg.BaseURL = ollamaURL
		}
	}
	if client, err := embedding.NewProvider(provCfg); err == nil {
		embedClient = client
	}
}

func registerTools(server *mcp.Server) {
	// Tool annotation helpers
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	boolPtr := func(b bool) *bool { return &b }
	writeNonDestructive := &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true}
	writeDestructive := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)}

	// search_notes
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_notes",
		Description: "Search the user's knowledge base for relevant notes, decisions, and context. Use this when you need background on a topic, want to find prior decisions, or need to understand project architecture.\n\nArgs:\n  query: Natural language search query (e.g. 'authentication approach', 'database schema decisions')\n  top_k: Number of results (default 10, max 100)\n\nReturns ranked list of matching notes with titles, paths, and text snippets.",
		Annotations: readOnly,
	}, handleSearchNotes)

	// search_notes_filtered
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_notes_filtered",
		Description: "Search the user's knowledge base with metadata filters. Use this when you want to narrow results by domain (e.g. 'engineering'), workstream (e.g. 'api-redesign'), tags, agent attribution, trust state, or content type.\n\nArgs:\n  query: Natural language search query\n  top_k: Number of results (default 10, max 100)\n  domain: Filter by domain (e.g. 'engineering', 'product')\n  workstream: Filter by workstream/project name\n  tags: Comma-separated tags to filter by\n  agent: Filter by agent attribution (e.g. 'codex', 'claude')\n  trust_state: Filter by trust state (validated, stale, contradicted, unknown)\n  content_type: Filter by content type (decision, handoff, note, research)\n\nReturns filtered ranked list.",
		Annotations: readOnly,
	}, handleSearchNotesFiltered)

	// get_note
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_note",
		Description: "Read the full content of a note. Use this after search_notes returns a relevant result and you need the complete text. Paths are relative to the vault root.\n\nArgs:\n  path: Relative path from vault root (as returned by search_notes)\n\nReturns full markdown text content.",
		Annotations: readOnly,
	}, handleGetNote)

	// find_similar_notes
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_similar_notes",
		Description: "Find notes that cover similar topics to a given note. Use this to discover related context, find notes that might conflict, or build a broader picture of a topic.\n\nArgs:\n  path: Relative path of the source note\n  top_k: Number of similar notes (default 5, max 100)\n\nReturns list of related notes ranked by similarity.",
		Annotations: readOnly,
	}, handleFindSimilar)

	// reindex
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reindex",
		Description: "Re-scan and re-index all markdown notes. Use this if the user has added or changed notes and search results seem stale. Incremental by default (only re-embeds changed files).\n\nArgs:\n  force: Re-embed all files regardless of changes (default false)\n\nReturns indexing statistics.",
		Annotations: writeDestructive,
	}, handleReindex)

	// index_stats
	mcp.AddTool(server, &mcp.Tool{
		Name:        "index_stats",
		Description: "Check the health and size of the note index. Use this to verify the index is up to date or to report stats to the user.\n\nReturns note count, chunk count, last indexed timestamp, embedding model info, and database size.\n\nIf the user reports problems, suggest they run `same doctor` for diagnostics. For bugs, direct them to: https://github.com/sgx-labs/statelessagent/issues",
		Annotations: readOnly,
	}, handleIndexStats)

	// save_note (write-side)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_note",
		Description: "Create or update a markdown note in the vault. The note is written to disk and indexed automatically.\n\nOptionally specify source files to enable provenance tracking — SAME will flag this note as stale if sources change.\n\nArgs:\n  path: Relative path within the vault (e.g. 'decisions/auth-approach.md')\n  content: Markdown content to write\n  append: If true, append to existing file instead of overwriting (default false)\n  agent: Optional writer attribution stored in frontmatter (e.g. 'codex')\n  sources: File paths that this note was derived from (optional)\n\nReturns confirmation with the saved path.",
		Annotations: writeDestructive,
	}, handleSaveNote)

	// save_decision (write-side)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_decision",
		Description: "Log a project decision. Appends to the decision log so future sessions can find it.\n\nArgs:\n  title: Short decision title (e.g. 'Use JWT for auth')\n  body: Full decision details — what was decided, why, alternatives considered\n  status: Decision status — 'accepted', 'proposed', or 'superseded' (default 'accepted')\n  agent: Optional writer attribution stored in frontmatter (e.g. 'codex')\n\nReturns confirmation.",
		Annotations: writeNonDestructive,
	}, handleSaveDecision)

	// create_handoff (write-side)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_handoff",
		Description: "Create a session handoff note so the next session picks up where this one left off. Write what you worked on, what's pending, and any blockers.\n\nArgs:\n  summary: What was accomplished this session\n  pending: What's left to do (optional)\n  blockers: Any blockers or open questions (optional)\n  agent: Optional writer attribution stored in frontmatter (e.g. 'codex')\n\nReturns path to the handoff note.",
		Annotations: writeNonDestructive,
	}, handleCreateHandoff)

	// recent_activity (read-side)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "recent_activity",
		Description: "Get recently modified notes. Use this to see what's changed recently or to orient yourself at the start of a session.\n\nArgs:\n  limit: Number of recent notes (default 10, max 50)\n\nReturns list of recently modified notes with titles and paths.",
		Annotations: readOnly,
	}, handleRecentActivity)

	// get_session_context (read-side)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_session_context",
		Description: "Get orientation context for a new session. Returns pinned notes, the latest handoff, and recent decisions — everything you need to pick up where the last session left off.\n\nReturns structured session context.",
		Annotations: readOnly,
	}, handleGetSessionContext)

	// search_across_vaults (federated read-side)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_across_vaults",
		Description: "Search across multiple registered vaults at once. Use this instead of search_notes when you need context from other projects or want a cross-project view. Vaults must be registered first via the CLI (`same vault add <name> <path>`).\n\nArgs:\n  query: Natural language search query\n  top_k: Number of results (default 10, max 100)\n  vaults: Comma-separated vault aliases to search. Omit to search all registered vaults. Unknown aliases are silently skipped.\n\nReturns ranked results with titles, paths, snippets, and source vault name.",
		Annotations: readOnly,
	}, handleSearchAcrossVaults)

	// mem_consolidate (autonomous memory management)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "mem_consolidate",
		Description: "Consolidate related notes in the vault. Merges duplicates, resolves contradictions, extracts key facts. Creates new knowledge files without modifying originals. Use this when the vault has many similar or overlapping notes.\n\nArgs:\n  dry_run: Preview what would be consolidated without writing files (default false)\n  threshold: Similarity threshold for grouping notes, 0.0-1.0 (default 0.75)\n\nReturns consolidation summary with groups found, facts extracted, and conflicts resolved. (experimental)",
		Annotations: writeDestructive,
	}, handleMemConsolidate)

	// mem_brief (autonomous memory management)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "mem_brief",
		Description: "Get an orientation briefing of what matters right now. Shows recent activity, open decisions, and key context. Use this at the start of a session to understand current project state.\n\nArgs:\n  max_items: Maximum items per section (default 5)\n\nReturns a concise briefing generated from vault contents. (experimental)",
		Annotations: readOnly,
	}, handleMemBrief)

	// mem_health (autonomous memory management)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "mem_health",
		Description: "Check the health of the memory vault. Returns a health score (0-100) and actionable recommendations. Use this to determine if the vault needs consolidation, reindexing, or cleanup.\n\nReturns health score, key metrics, and recommendations. (experimental)",
		Annotations: readOnly,
	}, handleMemHealth)

	// mem_forget (autonomous memory management)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "mem_forget",
		Description: "Suppress a memory so it won't be surfaced in normal search. The note is not deleted -- it's marked as suppressed and can be restored with mem_restore. Use this for outdated, incorrect, or irrelevant memories.\n\nArgs:\n  path: Path of the note to suppress (required)\n  reason: Why this memory is being suppressed (optional)\n  agent: Your agent identity (optional — if set, you can only suppress notes you created)\n\nReturns confirmation of suppression. (experimental)",
		Annotations: writeNonDestructive,
	}, handleMemForget)

	// mem_restore (undo mem_forget)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "mem_restore",
		Description: "Restore a previously suppressed memory so it appears in search again. Reverses the effect of mem_forget.\n\nArgs:\n  path: Path of the note to restore (required)\n\nReturns confirmation of restoration.",
		Annotations: writeNonDestructive,
	}, handleMemRestore)

	// mem_list_suppressed (show forgotten memories)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "mem_list_suppressed",
		Description: "List all memories that have been suppressed via mem_forget. Use this to see what's hidden before deciding what to restore.\n\nReturns a list of suppressed note paths.",
		Annotations: readOnly,
	}, handleMemListSuppressed)

	// save_kaizen (continuous improvement)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_kaizen",
		Description: "Log a friction point, bug, or improvement idea discovered during work. SAME tracks provenance — if the source files change later, the item is automatically flagged as potentially addressed.\n\nArgs:\n  description: What was observed (required)\n  area: Area of the codebase (e.g. 'indexer', 'config', 'hooks') (optional)\n  agent: Who observed it (optional)\n  sources: Related file paths for provenance tracking (optional)\n\nReturns confirmation with the file path.",
		Annotations: writeNonDestructive,
	}, handleSaveKaizen)
}

// Tool input types

type searchInput struct {
	Query string `json:"query" jsonschema:"Natural language search query"`
	TopK  int    `json:"top_k" jsonschema:"Number of results (default 10, max 100)"`
}

type searchFilteredInput struct {
	Query       string `json:"query" jsonschema:"Natural language search query"`
	TopK        int    `json:"top_k" jsonschema:"Number of results (default 10, max 100)"`
	Domain      string `json:"domain,omitempty" jsonschema:"Filter by domain"`
	Workstream  string `json:"workstream,omitempty" jsonschema:"Filter by workstream"`
	Tags        string `json:"tags,omitempty" jsonschema:"Comma-separated tags to filter by"`
	Agent       string `json:"agent,omitempty" jsonschema:"Filter by agent attribution"`
	TrustState  string `json:"trust_state,omitempty" jsonschema:"Filter by trust state (validated, stale, contradicted, unknown)"`
	ContentType string `json:"content_type,omitempty" jsonschema:"Filter by content type (decision, handoff, note, research)"`
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

type saveNoteInput struct {
	Path    string   `json:"path" jsonschema:"Relative path within the vault (e.g. decisions/auth.md)"`
	Content string   `json:"content" jsonschema:"Markdown content to write"`
	Append  bool     `json:"append" jsonschema:"Append to existing file instead of overwriting"`
	Agent   string   `json:"agent,omitempty" jsonschema:"Optional writer attribution (e.g. codex)"`
	Sources []string `json:"sources,omitempty" jsonschema:"File paths that this note was derived from or references. SAME tracks these to detect when source material changes, flagging the note as potentially stale."`
}

type saveDecisionInput struct {
	Title  string `json:"title" jsonschema:"Short decision title"`
	Body   string `json:"body" jsonschema:"Full decision details"`
	Status string `json:"status" jsonschema:"accepted, proposed, or superseded (default accepted)"`
	Agent  string `json:"agent,omitempty" jsonschema:"Optional writer attribution (e.g. codex)"`
}

type createHandoffInput struct {
	Summary  string `json:"summary" jsonschema:"What was accomplished this session"`
	Pending  string `json:"pending,omitempty" jsonschema:"What is left to do"`
	Blockers string `json:"blockers,omitempty" jsonschema:"Any blockers or open questions"`
	Agent    string `json:"agent,omitempty" jsonschema:"Optional writer attribution (e.g. codex)"`
}

type recentInput struct {
	Limit int `json:"limit" jsonschema:"Number of recent notes (default 10, max 50)"`
}

type searchAcrossVaultsInput struct {
	Query  string `json:"query" jsonschema:"Natural language search query"`
	TopK   int    `json:"top_k" jsonschema:"Number of results (default 10, max 100)"`
	Vaults string `json:"vaults,omitempty" jsonschema:"Comma-separated vault aliases (default: all)"`
}

type emptyInput struct{}

type memConsolidateInput struct {
	DryRun    bool    `json:"dry_run" jsonschema:"Preview what would be consolidated without writing files"`
	Threshold float64 `json:"threshold" jsonschema:"Similarity threshold for grouping notes (0.0-1.0)"`
}

type memBriefInput struct {
	MaxItems int `json:"max_items" jsonschema:"Maximum items per section (default 5)"`
}

type memForgetInput struct {
	Path   string `json:"path" jsonschema:"Path of the note to suppress"`
	Reason string `json:"reason,omitempty" jsonschema:"Why this memory is being suppressed"`
	Agent  string `json:"agent,omitempty" jsonschema:"Agent identity — if set, only notes created by this agent can be suppressed"`
}

type saveKaizenInput struct {
	Description string   `json:"description" jsonschema:"What was observed — friction, bug, or improvement idea"`
	Area        string   `json:"area,omitempty" jsonschema:"Area of the codebase (e.g. indexer, config, hooks)"`
	Agent       string   `json:"agent,omitempty" jsonschema:"Who observed it"`
	Sources     []string `json:"sources,omitempty" jsonschema:"Related file paths for provenance tracking"`
}

// Tool handlers

func handleSearchNotes(ctx context.Context, req *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Query) == "" {
		return errorResult("Error: query is required."), nil, nil
	}
	if len(input.Query) > maxQueryLen {
		return errorResult("Error: query too long (max 10,000 characters)."), nil, nil
	}
	topK := clampTopK(input.TopK, 10)
	opts := store.SearchOptions{TopK: topK}

	results, err := searchWithFallback(input.Query, opts)
	if err != nil {
		return errorResult("Search error. Try running reindex() first."), nil, nil
	}
	results = filterPrivatePaths(results)
	results = sanitizeResultSnippets(results)
	if len(results) == 0 {
		return errorResult("No results found. The index may be empty — try running reindex() first."), nil, nil
	}

	// Reconsolidation: increment access counts for surfaced notes (fire-and-forget).
	incrementAccessCounts(results)

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleSearchNotesFiltered(ctx context.Context, req *mcp.CallToolRequest, input searchFilteredInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Query) == "" {
		return errorResult("Error: query is required."), nil, nil
	}
	if len(input.Query) > maxQueryLen {
		return errorResult("Error: query too long (max 10,000 characters)."), nil, nil
	}
	agentFilter, err := normalizeAgent(input.Agent)
	if err != nil {
		return errorResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}
	topK := clampTopK(input.TopK, 10)

	var tags []string
	if input.Tags != "" {
		for _, t := range strings.Split(input.Tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}

	opts := store.SearchOptions{
		TopK:        topK,
		Domain:      input.Domain,
		Workstream:  input.Workstream,
		Agent:       agentFilter,
		Tags:        tags,
		TrustState:  input.TrustState,
		ContentType: input.ContentType,
	}

	results, err := searchWithFallback(input.Query, opts)
	if err != nil {
		return errorResult("Search error. Try running reindex() first."), nil, nil
	}
	results = filterPrivatePaths(results)
	results = sanitizeResultSnippets(results)
	if len(results) == 0 {
		return errorResult("No results found matching the filters."), nil, nil
	}

	// Reconsolidation: increment access counts for surfaced notes (fire-and-forget).
	incrementAccessCounts(results)

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleGetNote(ctx context.Context, req *mcp.CallToolRequest, input getInput) (*mcp.CallToolResult, any, error) {
	safePath := safeVaultPath(input.Path)
	if safePath == "" {
		return errorResult("Error: path must be a relative path within the vault."), nil, nil
	}

	// F04: Check file size before reading to prevent OOM on very large files
	info, err := os.Stat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResult("File not found."), nil, nil
		}
		return errorResult("Error reading file."), nil, nil
	}
	if info.Size() > maxReadSize {
		return errorResult(fmt.Sprintf("Error: file too large (%dKB). Maximum is %dKB.", info.Size()/1024, maxReadSize/1024)), nil, nil
	}

	content, err := os.ReadFile(safePath)
	if err != nil {
		return errorResult("Error reading file."), nil, nil
	}

	// SECURITY: Neutralize XML-like tags that could enable prompt injection
	// when the note content is returned to an AI agent via MCP.
	return textResult(neutralizeTags(string(content))), nil, nil
}

func handleFindSimilar(ctx context.Context, req *mcp.CallToolRequest, input similarInput) (*mcp.CallToolResult, any, error) {
	topK := clampTopK(input.TopK, 5)

	// Validate path through safeVaultPath to prevent probing _PRIVATE/ or dot-dirs
	if safeVaultPath(input.Path) == "" {
		return errorResult("Error: invalid note path."), nil, nil
	}

	if !db.HasVectors() {
		return errorResult("Similar notes requires semantic search (embeddings). Install Ollama and run reindex() to enable."), nil, nil
	}

	noteVec, err := db.GetNoteEmbedding(input.Path)
	if err != nil || noteVec == nil {
		return errorResult(fmt.Sprintf("No similar notes found for: %s. Is the note in the index?", input.Path)), nil, nil
	}

	// Fetch extra results, excluding the source note
	allResults, err := db.VectorSearch(noteVec, store.SearchOptions{TopK: topK + 10})
	if err != nil {
		return errorResult("Search error. Try running reindex() first."), nil, nil
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
		return errorResult(fmt.Sprintf("No similar notes found for: %s.", input.Path)), nil, nil
	}

	// Reconsolidation: increment access counts for surfaced notes (fire-and-forget).
	incrementAccessCounts(results)

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
		return errorResult(string(data)), nil, nil
	}
	lastReindexTime = time.Now()

	stats, err := indexer.Reindex(db, input.Force)
	if err != nil {
		// Mirror the CLI fallback: if embedding is unavailable, fall back to
		// keyword-only indexing so the vault is still searchable via FTS5/LIKE.
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "ollama") ||
			strings.Contains(errMsg, "connection") ||
			strings.Contains(errMsg, "refused") ||
			strings.Contains(errMsg, "embedding backend unavailable") ||
			strings.Contains(errMsg, "no embeddings generated") ||
			strings.Contains(errMsg, "keyword-only mode") ||
			strings.Contains(errMsg, `provider is "none"`) {
			stats, err = indexer.ReindexLite(context.Background(), db, input.Force, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "same: mcp: reindex keyword-only fallback: %v\n", err)
				return errorResult("Reindex failed (keyword-only fallback). Run 'same doctor' for diagnostics."), nil, nil
			}
			// Return stats with a note about lite mode
			stats.Timestamp = "keyword-only (embedding unavailable)"
		} else {
			fmt.Fprintf(os.Stderr, "same: mcp: reindex: %v\n", err)
			return errorResult("Reindex error. Run 'same doctor' for diagnostics."), nil, nil
		}
	}

	// Refresh the global embedClient so subsequent searches use the
	// current model/dimensions after a model change + reindex.
	refreshEmbedClient()

	data, _ := json.MarshalIndent(stats, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleIndexStats(ctx context.Context, req *mcp.CallToolRequest, input emptyInput) (*mcp.CallToolResult, any, error) {
	stats := indexer.GetStats(db)
	data, _ := json.MarshalIndent(stats, "", "  ")
	return textResult(string(data)), nil, nil
}

// Write-side handlers

func handleSaveNote(ctx context.Context, req *mcp.CallToolRequest, input saveNoteInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Path) == "" {
		return errorResult("Error: path is required."), nil, nil
	}
	if strings.TrimSpace(input.Content) == "" {
		return errorResult("Error: content is required."), nil, nil
	}
	if len(input.Content) > maxNoteSize {
		return errorResult("Error: content exceeds 100KB limit."), nil, nil
	}
	agent, err := normalizeAgent(input.Agent)
	if err != nil {
		return errorResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}

	// S21: Only allow .md files to be saved via MCP
	if !strings.HasSuffix(strings.ToLower(input.Path), ".md") {
		return errorResult("Error: only .md (markdown) files can be saved via MCP."), nil, nil
	}

	// Guard check: scan content for credentials before writing.
	// Warn (don't block) so users know they're storing sensitive data.
	guardWarnings := guard.ScanContent(input.Content)
	var credWarning string
	if len(guardWarnings) > 0 {
		var types []string
		seen := make(map[string]bool)
		for _, w := range guardWarnings {
			name := w.Pattern.Name
			if !seen[name] {
				seen[name] = true
				types = append(types, name)
			}
		}
		credWarning = fmt.Sprintf("Warning: note contains potential credentials (%s). Consider removing before sharing.", strings.Join(types, ", "))
	}

	safePath := safeVaultPath(input.Path)
	if safePath == "" {
		return errorResult("Error: path must be a relative path within the vault. Cannot write to _PRIVATE/."), nil, nil
	}
	relPath, relErr := store.NormalizeClaimPath(input.Path)
	if relErr != nil {
		return errorResult("Error: path must stay within the vault. Use a relative path like 'notes/topic.md'."), nil, nil
	}
	if firstSegment, _, found := strings.Cut(relPath, "/"); found {
		if strings.EqualFold(firstSegment, "imports") {
			return errorResult("Error: save_note cannot write to imports/. Use same import for imported content."), nil, nil
		}
	} else if strings.EqualFold(relPath, "imports") {
		return errorResult("Error: save_note cannot write to imports/. Use same import for imported content."), nil, nil
	}
	if !checkWriteRateLimit() {
		return errorResult("Error: too many write operations. Try again in a minute."), nil, nil
	}

	// S11: Prepend a provenance header so readers know this was MCP-generated.
	// This helps mitigate stored prompt injection by clearly marking
	// machine-written content when it is later surfaced to an agent.
	mcpHeader := "<!-- Note saved via SAME MCP tool. Review before trusting. -->"

	// Ensure parent directory exists
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errorResult("Error: could not create destination directory. Check vault write permissions."), nil, nil
	}

	if input.Append {
		_, statErr := os.Stat(safePath)
		if os.IsNotExist(statErr) {
			content := input.Content
			if agent != "" {
				content = upsertAgentFrontmatter(content, agent)
				content = injectProvenanceHeader(content, mcpHeader)
			} else {
				content = mcpHeader + "\n" + content
			}
			if err := os.WriteFile(safePath, []byte(content), 0o600); err != nil {
				return errorResult("Error: could not write note file. Check vault permissions and available disk space."), nil, nil
			}
		} else {
			if statErr != nil {
				return errorResult("Error: could not open note for appending. Check file permissions and lock state."), nil, nil
			}
			if agent != "" {
				existing, readErr := os.ReadFile(safePath)
				if readErr == nil {
					updated := upsertAgentFrontmatter(string(existing), agent)
					if updated != string(existing) {
						if writeErr := os.WriteFile(safePath, []byte(updated), 0o600); writeErr != nil {
							return errorResult("Error: could not update note metadata. The note was not modified; check file permissions."), nil, nil
						}
					}
				}
			}

			f, err := os.OpenFile(safePath, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return errorResult("Error: could not open note for appending. Check file permissions and lock state."), nil, nil
			}
			// F14: Add provenance marker for appended MCP content
			_, err = f.WriteString("\n<!-- Appended via SAME MCP tool -->\n" + input.Content)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				return errorResult("Error: could not append note content. Check vault permissions and available disk space."), nil, nil
			}
		}
	} else {
		content := input.Content
		if agent != "" {
			content = upsertAgentFrontmatter(content, agent)
			content = injectProvenanceHeader(content, mcpHeader)
		} else {
			content = mcpHeader + "\n" + content
		}
		if err := os.WriteFile(safePath, []byte(content), 0o600); err != nil {
			return errorResult("Error: could not write note file. Check vault permissions and available disk space."), nil, nil
		}
	}

	// S7: Index only the saved file instead of triggering a full vault reindex.
	// This avoids O(n) work per save_note call, preventing DoS on large vaults.
	if err := indexer.IndexSingleFile(db, safePath, relPath, vaultRoot, embedClient); err != nil {
		// Non-fatal: the note was saved, just not indexed yet
		return textResult(fmt.Sprintf("Saved: %s (index update failed — run reindex to fix)", input.Path)), nil, nil
	}

	// Record provenance sources if provided by the caller.
	// Don't fail the save if source recording fails — log a warning and continue.
	recordProvenanceSources(relPath, input.Sources)

	// Contradiction detection: find similar existing notes and check for contradictions.
	// This is best-effort — if embedding provider is unavailable, skip silently.
	contradictions := detectAndRecordContradictions(relPath, input.Content)

	message := fmt.Sprintf("Saved: %s", input.Path)
	if len(contradictions) > 0 {
		message += fmt.Sprintf(" (%d contradiction(s) detected — older notes flagged)", len(contradictions))
	}
	if agent != "" {
		if readClaims, claimErr := db.GetActiveReadClaimsForPath(relPath, agent); claimErr == nil && len(readClaims) > 0 {
			seen := make(map[string]bool, len(readClaims))
			var readers []string
			for _, c := range readClaims {
				if !seen[c.Agent] {
					seen[c.Agent] = true
					readers = append(readers, c.Agent)
				}
			}
			message += fmt.Sprintf(" (warning: read-claims by %s on %s — check for breakage)", strings.Join(readers, ", "), relPath)
		}
	}
	if credWarning != "" {
		message += "\n" + credWarning
	}
	return textResult(message), nil, nil
}

// recordProvenanceSources records explicitly-provided source files and
// recently-injected context notes as provenance for the saved note.
func recordProvenanceSources(notePath string, explicitSources []string) {
	var sources []store.NoteSource

	// 1. Record explicitly provided source file paths.
	for _, srcPath := range explicitSources {
		srcPath = strings.TrimSpace(srcPath)
		if srcPath == "" {
			continue
		}
		// SECURITY: validate path to prevent traversal (e.g. "../../etc/passwd")
		if safeVaultPath(srcPath) == "" {
			continue
		}
		hash := ""
		fullPath := filepath.Join(vaultRoot, srcPath)
		if content, err := os.ReadFile(fullPath); err == nil {
			h := sha256.Sum256(content)
			hash = fmt.Sprintf("%x", h)
		}
		sources = append(sources, store.NoteSource{
			SourcePath: srcPath,
			SourceType: "file",
			SourceHash: hash,
		})
	}

	// 2. Auto-detect recently injected note paths from context_usage (last 60s).
	// These are notes that were surfaced to the agent shortly before this save,
	// indicating the saved note is likely derived from them.
	if recentPaths, err := getRecentInjectedNotePaths(60); err == nil {
		seen := make(map[string]bool, len(sources))
		for _, s := range sources {
			seen[s.SourcePath] = true
		}
		for _, p := range recentPaths {
			if seen[p] || p == notePath {
				continue
			}
			hash := ""
			fullPath := filepath.Join(vaultRoot, p)
			if content, err := os.ReadFile(fullPath); err == nil {
				h := sha256.Sum256(content)
				hash = fmt.Sprintf("%x", h)
			}
			sources = append(sources, store.NoteSource{
				SourcePath: p,
				SourceType: "note",
				SourceHash: hash,
			})
		}
	}

	if len(sources) == 0 {
		return
	}

	if err := db.RecordSources(notePath, sources); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to record provenance sources for %s: %v\n", notePath, err)
	}
}

// getRecentInjectedNotePaths returns note paths (.md files) that were injected
// into agent context within the last `seconds` seconds.
func getRecentInjectedNotePaths(seconds int) ([]string, error) {
	rows, err := db.Conn().Query(
		`SELECT injected_paths FROM context_usage
		 WHERE timestamp > datetime('now', ?)
		 ORDER BY timestamp DESC`,
		fmt.Sprintf("-%d seconds", seconds),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]bool)
	var paths []string
	for rows.Next() {
		var pathsJSON string
		if err := rows.Scan(&pathsJSON); err != nil {
			continue
		}
		var injected []string
		if err := json.Unmarshal([]byte(pathsJSON), &injected); err != nil {
			continue
		}
		for _, p := range injected {
			p = strings.TrimSpace(p)
			if p != "" && strings.HasSuffix(strings.ToLower(p), ".md") && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return paths, rows.Err()
}

// detectAndRecordContradictions searches for notes similar to the newly saved
// content and runs contradiction detection. If contradictions are found, the
// OLD notes are marked as contradicted with the appropriate type. Returns the
// list of detected contradictions (may be empty). This is best-effort — errors
// are logged to stderr, not propagated.
func detectAndRecordContradictions(notePath string, content string) []memory.ContradictionResult {
	if embedClient == nil || db == nil {
		return nil
	}

	// Get an embedding of the new content for similarity search
	vec, err := embedClient.GetDocumentEmbedding(content)
	if err != nil || len(vec) == 0 {
		return nil
	}

	// Search for similar notes (fetch extra to find good candidates)
	results, err := db.VectorSearchRaw(vec, 20)
	if err != nil || len(results) == 0 {
		return nil
	}

	// Convert to contradiction candidates, computing a similarity score
	// from the distance. VectorSearchRaw returns distance (lower = more similar),
	// so we convert to similarity (higher = more similar).
	const distCeiling = 20.0
	var candidates []memory.ContradictionCandidate
	for _, r := range results {
		if r.Path == notePath {
			continue // skip the note we just saved
		}
		similarity := 1.0 - (r.Distance / distCeiling)
		if similarity < 0 {
			similarity = 0
		}
		candidates = append(candidates, memory.ContradictionCandidate{
			Path:   r.Path,
			Text:   r.Text,
			Score:  similarity,
			Title:  r.Title,
			Tags:   r.Tags,
			Domain: r.Domain,
		})
	}

	contradictions := memory.DetectContradictions(content, notePath, candidates)
	if len(contradictions) == 0 {
		return nil
	}

	// Record contradictions: mark old notes as contradicted with type
	for _, c := range contradictions {
		if err := db.SetContradicted(c.OldNotePath, string(c.Type)); err != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to set contradiction for %s: %v\n", c.OldNotePath, err)
		}
	}

	return contradictions
}

func handleSaveDecision(ctx context.Context, req *mcp.CallToolRequest, input saveDecisionInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Title) == "" {
		return errorResult("Error: title is required."), nil, nil
	}
	if strings.TrimSpace(input.Body) == "" {
		return errorResult("Error: body is required."), nil, nil
	}
	if len(input.Title)+len(input.Body) > maxNoteSize {
		return errorResult(fmt.Sprintf("Error: decision content too large (max %dKB).", maxNoteSize/1024)), nil, nil
	}
	agent, err := normalizeAgent(input.Agent)
	if err != nil {
		return errorResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}

	status := input.Status
	if status == "" {
		status = "accepted"
	}
	if status != "accepted" && status != "proposed" && status != "superseded" {
		return errorResult("Error: status must be 'accepted', 'proposed', or 'superseded'."), nil, nil
	}

	// Build decision entry
	now := time.Now().Format("2006-01-02")
	displayStatus := strings.ToUpper(status[:1]) + status[1:]
	// F10: Sanitize title to prevent markdown/newline injection
	safeTitle := strings.ReplaceAll(input.Title, "\n", " ")
	safeTitle = strings.ReplaceAll(safeTitle, "\r", " ")

	entry := fmt.Sprintf("\n## Decision: %s\n**Date:** %s\n**Status:** %s\n\n%s\n",
		safeTitle, now, displayStatus, input.Body)
	if agent != "" {
		entry = fmt.Sprintf("\n## Decision: %s\n**Date:** %s\n**Status:** %s\n**Agent:** %s\n\n%s\n",
			safeTitle, now, displayStatus, agent, input.Body)
	}

	// Get decision log path from config
	cfg, _ := config.LoadConfig()
	logName := "decisions.md"
	if cfg != nil && cfg.Vault.DecisionLog != "" {
		logName = cfg.Vault.DecisionLog
	}

	safePath := safeVaultPath(logName)
	if safePath == "" {
		return errorResult("Error: decision log path is invalid. Set `vault.decision_log` to a relative file under the vault."), nil, nil
	}
	if !checkWriteRateLimit() {
		return errorResult("Error: too many write operations. Try again in a minute."), nil, nil
	}
	// Only set file-level agent frontmatter when creating a new file.
	// On append, each decision entry carries its own inline **Agent:** attribution,
	// so we must NOT rewrite file-level agent (which would reattribute prior entries).
	if agent != "" {
		if _, readErr := os.ReadFile(safePath); os.IsNotExist(readErr) {
			initial := upsertAgentFrontmatter("", agent)
			if writeErr := os.WriteFile(safePath, []byte(initial), 0o600); writeErr != nil {
				return errorResult("Error: could not initialize decision log metadata. Check vault permissions."), nil, nil
			}
		}
	}

	// Append to decision log
	f, err := os.OpenFile(safePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return errorResult("Error: could not open decision log for writing. Check file permissions."), nil, nil
	}
	_, err = f.WriteString(entry)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errorResult("Error: could not write to decision log. Check available disk space and permissions."), nil, nil
	}

	// Index only the decision log file instead of a full vault reindex.
	// This avoids O(n) work per call, preventing DoS on large vaults.
	relPath := filepath.ToSlash(logName)
	_ = indexer.IndexSingleFile(db, safePath, relPath, vaultRoot, embedClient) // best-effort indexing

	return textResult(fmt.Sprintf("Decision logged: %s (%s)", input.Title, status)), nil, nil
}

func handleCreateHandoff(ctx context.Context, req *mcp.CallToolRequest, input createHandoffInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Summary) == "" {
		return errorResult("Error: summary is required."), nil, nil
	}
	totalSize := len(input.Summary) + len(input.Pending) + len(input.Blockers)
	if totalSize > maxNoteSize {
		return errorResult(fmt.Sprintf("Error: handoff content too large (max %dKB).", maxNoteSize/1024)), nil, nil
	}
	agent, err := normalizeAgent(input.Agent)
	if err != nil {
		return errorResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}

	// Get handoff dir from config
	cfg, _ := config.LoadConfig()
	handoffDir := "sessions"
	if cfg != nil && cfg.Vault.HandoffDir != "" {
		handoffDir = cfg.Vault.HandoffDir
	}

	now := time.Now()
	// Use time-based suffix to match auto-handoff naming convention
	filename := fmt.Sprintf("%s-%s-handoff.md", now.Format("2006-01-02"), now.Format("150405"))
	relPath := filepath.Join(handoffDir, filename)

	safePath := safeVaultPath(relPath)
	if safePath == "" {
		return errorResult("Error: handoff path is invalid. Set `vault.handoff_dir` to a relative directory under the vault."), nil, nil
	}
	if !checkWriteRateLimit() {
		return errorResult("Error: too many write operations. Try again in a minute."), nil, nil
	}

	// Build handoff content
	var buf strings.Builder
	if agent != "" {
		buf.WriteString(fmt.Sprintf("---\nagent: %q\n---\n\n", agent))
	}
	buf.WriteString(fmt.Sprintf("# Session Handoff — %s\n\n", now.Format("2006-01-02")))
	buf.WriteString("## What we worked on\n")
	buf.WriteString(input.Summary)
	buf.WriteString("\n")
	if input.Pending != "" {
		buf.WriteString("\n## Pending\n")
		buf.WriteString(input.Pending)
		buf.WriteString("\n")
	}
	if input.Blockers != "" {
		buf.WriteString("\n## Blockers\n")
		buf.WriteString(input.Blockers)
		buf.WriteString("\n")
	}

	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errorResult("Error: could not create handoff directory. Check vault write permissions."), nil, nil
	}
	if err := os.WriteFile(safePath, []byte(buf.String()), 0o600); err != nil {
		return errorResult("Error: could not write handoff note. Check vault permissions and available disk space."), nil, nil
	}

	// Index only the handoff file instead of a full vault reindex.
	_ = indexer.IndexSingleFile(db, safePath, filepath.ToSlash(relPath), vaultRoot, embedClient) // best-effort indexing

	return textResult(fmt.Sprintf("Handoff saved: %s", relPath)), nil, nil
}

func handleRecentActivity(ctx context.Context, req *mcp.CallToolRequest, input recentInput) (*mcp.CallToolResult, any, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	notes, err := db.RecentNotes(limit)
	if err != nil {
		return errorResult("Error fetching recent notes. Try running reindex() first."), nil, nil
	}
	if len(notes) == 0 {
		return errorResult("No notes found. The index may be empty — try running reindex() first."), nil, nil
	}

	entries := make([]map[string]string, 0, len(notes))
	for _, n := range notes {
		// SECURITY: Filter _PRIVATE/ paths (defense-in-depth; DB query may not filter)
		upper := strings.ToUpper(n.Path)
		if strings.HasPrefix(upper, "_PRIVATE/") || strings.HasPrefix(upper, "_PRIVATE\\") {
			continue
		}
		entries = append(entries, map[string]string{
			"path":     n.Path,
			"title":    n.Title,
			"modified": formatTimestamp(n.Modified),
		})
	}

	data, _ := json.MarshalIndent(entries, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleGetSessionContext(ctx context.Context, req *mcp.CallToolRequest, input emptyInput) (*mcp.CallToolResult, any, error) {
	result := map[string]any{}

	// Pinned notes
	pinned, err := db.GetPinnedNotes()
	if err == nil && len(pinned) > 0 {
		pinnedList := make([]map[string]string, 0, len(pinned))
		for _, p := range pinned {
			text := p.Text
			if len(text) > 500 {
				text = text[:500] + "..."
			}
			// SECURITY: Neutralize injection tags in pinned note text
			pinnedList = append(pinnedList, map[string]string{
				"path":  p.Path,
				"title": p.Title,
				"text":  neutralizeTags(text),
			})
		}
		result["pinned_notes"] = pinnedList
	}

	// Latest handoff
	handoff, err := db.GetLatestHandoff()
	if err == nil && handoff != nil {
		text := handoff.Text
		if len(text) > 1000 {
			text = text[:1000] + "..."
		}
		// SECURITY: Neutralize injection tags in handoff text
		result["latest_handoff"] = map[string]string{
			"path":     handoff.Path,
			"title":    handoff.Title,
			"text":     neutralizeTags(text),
			"modified": formatTimestamp(handoff.Modified),
		}
	}

	// Recent notes
	recent, err := db.RecentNotes(5)
	if err == nil && len(recent) > 0 {
		recentList := make([]map[string]string, 0, len(recent))
		for _, r := range recent {
			upper := strings.ToUpper(r.Path)
			if strings.HasPrefix(upper, "_PRIVATE/") || strings.HasPrefix(upper, "_PRIVATE\\") {
				continue
			}
			recentList = append(recentList, map[string]string{
				"path":     r.Path,
				"title":    r.Title,
				"modified": formatTimestamp(r.Modified),
			})
		}
		result["recent_notes"] = recentList
	}

	// Active multi-agent claims
	_, _ = db.PurgeExpiredClaims()
	if claims, claimErr := db.ListActiveClaims(); claimErr == nil && len(claims) > 0 {
		claimList := make([]map[string]any, 0, len(claims))
		for _, c := range claims {
			claimList = append(claimList, map[string]any{
				"path":       c.Path,
				"agent":      c.Agent,
				"type":       c.Type,
				"claimed_at": time.Unix(c.ClaimedAt, 0).UTC().Format(time.RFC3339),
				"expires_at": time.Unix(c.ExpiresAt, 0).UTC().Format(time.RFC3339),
			})
		}
		result["active_claims"] = claimList
	}

	// Git context (best-effort; omitted when unavailable)
	if git := collectGitContext(vaultRoot); git != nil {
		result["git"] = git
	}

	// Stats
	stats := indexer.GetStats(db)
	result["stats"] = stats

	data, _ := json.MarshalIndent(result, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleSearchAcrossVaults(ctx context.Context, req *mcp.CallToolRequest, input searchAcrossVaultsInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Query) == "" {
		return errorResult("Error: query is required."), nil, nil
	}
	if len(input.Query) > maxQueryLen {
		return errorResult("Error: query too long (max 10,000 characters)."), nil, nil
	}

	topK := clampTopK(input.TopK, 10)

	// Resolve vault DB paths
	reg := config.LoadRegistry()
	vaultDBPaths := make(map[string]string)

	if input.Vaults == "" {
		// Search all registered vaults
		for alias, vaultPath := range reg.Vaults {
			dbPath := filepath.Join(vaultPath, ".same", "data", "vault.db")
			if _, err := os.Stat(dbPath); err == nil {
				vaultDBPaths[alias] = dbPath
			}
		}
	} else {
		for _, alias := range strings.Split(input.Vaults, ",") {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			// F13: Only resolve via registry map, not filesystem probing.
			// ResolveVault falls through to os.Stat which would allow
			// searching arbitrary paths not in the vault registry.
			resolved, ok := reg.Vaults[alias]
			if !ok || resolved == "" {
				continue
			}
			dbPath := filepath.Join(resolved, ".same", "data", "vault.db")
			if _, err := os.Stat(dbPath); err == nil {
				vaultDBPaths[alias] = dbPath
			}
		}
	}

	if len(vaultDBPaths) == 0 {
		return errorResult("No searchable vaults found. Register vaults with 'same vault add <name> <path>'."), nil, nil
	}

	// Try to get query embedding
	var queryVec []float32
	if embedClient != nil {
		queryVec, _ = embedClient.GetQueryEmbedding(input.Query)
	}

	results, err := store.FederatedSearch(vaultDBPaths, queryVec, input.Query, store.SearchOptions{TopK: topK})
	if err != nil {
		return errorResult("Federated search error."), nil, nil
	}

	// SECURITY: Filter _PRIVATE/ paths from results. VectorSearch doesn't
	// filter at the SQL level, so we must filter here for defense-in-depth.
	filtered := make([]store.FederatedResult, 0, len(results))
	for _, r := range results {
		upper := strings.ToUpper(r.Path)
		if !strings.HasPrefix(upper, "_PRIVATE/") && !strings.HasPrefix(upper, "_PRIVATE\\") {
			filtered = append(filtered, r)
		}
	}
	results = filtered

	results = sanitizeFederatedSnippets(results)

	if len(results) == 0 {
		return errorResult(fmt.Sprintf("No results found across %d vault(s).", len(vaultDBPaths))), nil, nil
	}

	// Reconsolidation: increment access counts for results from the current vault (fire-and-forget).
	// Results from other vaults are not incremented since those DBs are already closed.
	if len(results) > 0 {
		var localPaths []string
		for _, r := range results {
			localPaths = append(localPaths, r.Path)
		}
		_ = db.IncrementAccessCount(localPaths)
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
}

// --- Autonomous memory management handlers ---

func handleMemConsolidate(ctx context.Context, req *mcp.CallToolRequest, input memConsolidateInput) (*mcp.CallToolResult, any, error) {
	if !checkWriteRateLimit() && !input.DryRun {
		return errorResult("Error: too many write operations. Try again in a minute."), nil, nil
	}

	noteCount, _ := db.NoteCount()
	if noteCount == 0 {
		return textResult("Your vault is empty. Store some notes first before consolidating."), nil, nil
	}

	chat, err := llm.NewClient()
	if err != nil {
		return errorResult("Consolidation requires an LLM provider. Run 'same init' to configure one, or set SAME_CHAT_PROVIDER."), nil, nil
	}

	model, err := chat.PickBestModel()
	if err != nil || model == "" {
		return errorResult("No chat model available. Ensure your LLM provider has at least one model installed."), nil, nil
	}

	threshold := input.Threshold
	if threshold <= 0 || threshold > 1.0 {
		threshold = 0.75
	}

	vaultPath, _ := filepath.Abs(config.VaultPath())

	// embedClient (package-level) may be used as consolidate.EmbedProvider.
	// The consolidate engine accepts nil and falls back to keyword-based grouping.
	var ep consolidate.EmbedProvider
	if embedClient != nil {
		ep = embedClient
	}

	engine := consolidate.NewEngine(db, chat, ep, model, vaultPath, threshold)
	result, err := engine.Run(input.DryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: mcp: consolidation: %v\n", err)
		return errorResult("Consolidation error. Check LLM provider connectivity and try again."), nil, nil
	}

	// Build formatted text output
	var b strings.Builder
	if result.DryRun {
		b.WriteString("=== Consolidation Preview (dry run) ===\n\n")
	} else {
		b.WriteString("=== Consolidation Complete ===\n\n")
	}
	fmt.Fprintf(&b, "Groups found: %d\n", result.GroupsFound)
	fmt.Fprintf(&b, "Notes processed: %d\n", result.NotesProcessed)
	fmt.Fprintf(&b, "Facts extracted: %d\n", result.FactsExtracted)
	fmt.Fprintf(&b, "Conflicts found: %d\n", result.ConflictsFound)
	if !result.DryRun {
		fmt.Fprintf(&b, "Knowledge files created: %d\n", result.NotesCreated)
	}

	for i, g := range result.Groups {
		fmt.Fprintf(&b, "\n--- Group %d: %s ---\n", i+1, g.Theme)
		fmt.Fprintf(&b, "Sources: %d notes\n", len(g.SourceNotes))
		for _, src := range g.SourceNotes {
			fmt.Fprintf(&b, "  - %s (%s)\n", src.Path, src.Title)
		}
		if len(g.Facts) > 0 {
			b.WriteString("Key facts:\n")
			for _, fact := range g.Facts {
				fmt.Fprintf(&b, "  - %s\n", fact)
			}
		}
		if len(g.Conflicts) > 0 {
			b.WriteString("Conflicts:\n")
			for _, c := range g.Conflicts {
				fmt.Fprintf(&b, "  - %s\n", c.Fact1)
			}
		}
		if g.OutputPath != "" {
			fmt.Fprintf(&b, "Output: %s\n", g.OutputPath)
		}
	}

	return textResult(neutralizeTags(b.String())), nil, nil
}

// briefNote holds a note record gathered for MCP briefing context.
type mcpBriefNote struct {
	Path        string
	Title       string
	Text        string
	Modified    float64
	ContentType string
	Confidence  float64
	AccessCount int
}

func handleMemBrief(ctx context.Context, req *mcp.CallToolRequest, input memBriefInput) (*mcp.CallToolResult, any, error) {
	maxItems := input.MaxItems
	if maxItems <= 0 {
		maxItems = 5
	}
	if maxItems > 50 {
		maxItems = 50
	}

	noteCount, _ := db.NoteCount()
	if noteCount == 0 {
		return textResult("Your vault is empty. Store some notes first with save_note or run 'same demo' to add sample data."), nil, nil
	}

	conn := db.Conn()

	recentNotes := queryMCPBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence
		 FROM vault_notes
		 WHERE chunk_id = 0 AND path NOT LIKE '_PRIVATE/%' AND COALESCE(suppressed, 0) = 0
		 ORDER BY modified DESC
		 LIMIT 20`)

	sessionNotes := queryMCPBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence
		 FROM vault_notes
		 WHERE chunk_id = 0 AND (content_type = 'session' OR path LIKE 'sessions/%' OR path LIKE '%session%')
			AND COALESCE(suppressed, 0) = 0
		 ORDER BY modified DESC
		 LIMIT ?`, maxItems)

	decisionNotes := queryMCPBriefNotes(conn,
		`SELECT path, title, text, modified, content_type, confidence
		 FROM vault_notes
		 WHERE chunk_id = 0 AND (content_type = 'decision' OR path LIKE '%decision%')
			AND COALESCE(suppressed, 0) = 0
		 ORDER BY modified DESC
		 LIMIT ?`, maxItems)

	highConfNotes := queryMCPBriefNotesWithAccess(conn,
		`SELECT path, title, text, confidence, access_count
		 FROM vault_notes
		 WHERE chunk_id = 0 AND confidence > 0.7 AND path NOT LIKE '_PRIVATE/%'
			AND COALESCE(suppressed, 0) = 0
		 ORDER BY confidence DESC, access_count DESC
		 LIMIT 10`)

	totalGathered := len(recentNotes) + len(sessionNotes) + len(decisionNotes) + len(highConfNotes)
	if totalGathered == 0 {
		return textResult(fmt.Sprintf("No notes found for briefing. Your vault has %d notes but none match briefing criteria. Try adding session logs or decision records.", noteCount)), nil, nil
	}

	chat, err := llm.NewClient()
	if err != nil {
		return errorResult("Brief requires an LLM provider. Run 'same init' to configure one, or set SAME_CHAT_PROVIDER."), nil, nil
	}

	model, err := chat.PickBestModel()
	if err != nil || model == "" {
		return errorResult("No chat model available. Ensure your LLM provider has at least one model installed."), nil, nil
	}

	prompt := buildMCPBriefPrompt(recentNotes, sessionNotes, decisionNotes, highConfNotes)
	answer, err := chat.Generate(model, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: mcp: briefing generation: %v\n", err)
		return errorResult("Briefing generation failed. Check LLM provider connectivity and try again."), nil, nil
	}

	// Add sources summary
	var b strings.Builder
	b.WriteString(answer)
	fmt.Fprintf(&b, "\n\n---\nBased on %d notes (%d sessions, %d decisions, %d knowledge).",
		totalGathered, len(sessionNotes), len(decisionNotes), len(highConfNotes))

	return textResult(neutralizeTags(b.String())), nil, nil
}

func handleMemHealth(ctx context.Context, req *mcp.CallToolRequest, input emptyInput) (*mcp.CallToolResult, any, error) {
	conn := db.Conn()

	// Total notes
	var totalNotes int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0`,
	).Scan(&totalNotes); err != nil {
		fmt.Fprintf(os.Stderr, "same: mcp: mem_health query: %v\n", err)
		return errorResult("Error querying vault health. The database may be corrupted — run 'same doctor'."), nil, nil
	}

	if totalNotes == 0 {
		return textResult("Your vault is empty. Store some notes first with save_note or run 'same demo' to get started."), nil, nil
	}

	// Embedding coverage
	var embeddedCount int
	err := conn.QueryRow(
		`SELECT COUNT(DISTINCT vn.id) FROM vault_notes vn
		 INNER JOIN vault_notes_vec vnv ON vn.id = vnv.note_id
		 WHERE vn.chunk_id = 0`,
	).Scan(&embeddedCount)
	if err != nil {
		embeddedCount = 0
	}

	// Content type distribution
	type contentTypeStat struct {
		name  string
		count int
	}
	var contentTypes []contentTypeStat
	ctRows, err := conn.Query(
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
	_ = conn.QueryRow(
		`SELECT AVG(confidence) FROM vault_notes WHERE chunk_id = 0 AND confidence > 0`,
	).Scan(&avgConfidence)

	// Stale notes
	thirtyDaysAgo := float64(time.Now().Add(-30 * 24 * time.Hour).Unix())
	var staleCount int
	_ = conn.QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND access_count = 0 AND modified < ?`,
		thirtyDaysAgo,
	).Scan(&staleCount)

	// Recent activity
	sevenDaysAgo := float64(time.Now().Add(-7 * 24 * time.Hour).Unix())
	var recentCount int
	_ = conn.QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0 AND modified > ?`,
		sevenDaysAgo,
	).Scan(&recentCount)

	// Knowledge notes
	var knowledgeCount int
	_ = conn.QueryRow(
		`SELECT COUNT(*) FROM vault_notes
		 WHERE chunk_id = 0 AND (path LIKE 'knowledge/%' OR content_type = 'knowledge')`,
	).Scan(&knowledgeCount)

	// Accessed notes
	var accessedCount int
	_ = conn.QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0 AND access_count > 0`,
	).Scan(&accessedCount)

	// Never accessed
	var neverAccessedCount int
	_ = conn.QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0 AND access_count = 0`,
	).Scan(&neverAccessedCount)

	// Suppressed notes
	var suppressedCount int
	_ = conn.QueryRow(
		`SELECT COUNT(*) FROM vault_notes WHERE chunk_id = 0 AND COALESCE(suppressed, 0) = 1`,
	).Scan(&suppressedCount)

	// Compute health score (same formula as health_cmd.go)
	score := computeMCPHealthScore(totalNotes, embeddedCount, knowledgeCount, recentCount, accessedCount)

	// Build formatted output
	var b strings.Builder
	fmt.Fprintf(&b, "=== Vault Health ===\n\n")
	fmt.Fprintf(&b, "Score: %d/100 (%s)\n\n", score, healthLabel(score))

	fmt.Fprintf(&b, "--- Overview ---\n")
	fmt.Fprintf(&b, "Total notes: %d\n", totalNotes)
	if totalNotes > 0 {
		embedPct := embeddedCount * 100 / totalNotes
		fmt.Fprintf(&b, "Embedded: %d/%d (%d%%)\n", embeddedCount, totalNotes, embedPct)
	}
	fmt.Fprintf(&b, "Knowledge notes: %d\n", knowledgeCount)
	if suppressedCount > 0 {
		fmt.Fprintf(&b, "Suppressed: %d\n", suppressedCount)
	}

	if len(contentTypes) > 0 {
		fmt.Fprintf(&b, "\n--- Content Types ---\n")
		for _, ct := range contentTypes {
			pct := ct.count * 100 / totalNotes
			fmt.Fprintf(&b, "%s: %d (%d%%)\n", ct.name, ct.count, pct)
		}
	}

	fmt.Fprintf(&b, "\n--- Activity ---\n")
	fmt.Fprintf(&b, "Active (7 days): %d notes\n", recentCount)
	fmt.Fprintf(&b, "Stale (30+ days): %d notes\n", staleCount)
	fmt.Fprintf(&b, "Never accessed: %d notes\n", neverAccessedCount)
	if avgConfidence.Valid {
		fmt.Fprintf(&b, "Avg confidence: %.2f\n", avgConfidence.Float64)
	}

	// Recommendations
	var recs []string
	if staleCount > 0 {
		recs = append(recs, fmt.Sprintf("Run mem_consolidate to organize %d stale notes", staleCount))
	}
	noEmbeddings := totalNotes - embeddedCount
	if noEmbeddings > 0 {
		recs = append(recs, fmt.Sprintf("%d notes have no embeddings -- run reindex()", noEmbeddings))
	}
	if neverAccessedCount > 0 {
		recs = append(recs, fmt.Sprintf("Consider reviewing %d never-accessed notes", neverAccessedCount))
	}
	if knowledgeCount == 0 && totalNotes >= 5 {
		recs = append(recs, "Run mem_consolidate to create knowledge summaries")
	}

	if len(recs) > 0 {
		fmt.Fprintf(&b, "\n--- Recommendations ---\n")
		for _, r := range recs {
			fmt.Fprintf(&b, "- %s\n", r)
		}
	}

	return textResult(b.String()), nil, nil
}

func handleMemForget(ctx context.Context, req *mcp.CallToolRequest, input memForgetInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Path) == "" {
		return errorResult("Error: path is required."), nil, nil
	}
	if !checkWriteRateLimit() {
		return errorResult("Error: too many write operations. Try again in a minute."), nil, nil
	}

	// Validate the path exists in the database
	notes, err := db.GetNoteByPath(input.Path)
	if err != nil || len(notes) == 0 {
		return errorResult(fmt.Sprintf("No note found at path: %s", input.Path)), nil, nil
	}

	// Agent ownership check: if caller identifies as an agent, they can only
	// suppress notes they created. Vault owners (no agent param) can suppress anything.
	callerAgent, agentErr := normalizeAgent(input.Agent)
	if agentErr != nil {
		return errorResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}
	if callerAgent != "" {
		noteAgent := notes[0].Agent
		if noteAgent != "" && noteAgent != callerAgent {
			return errorResult(fmt.Sprintf(
				"Error: cannot suppress a note created by agent %q. "+
					"Only the creating agent or vault owner can suppress notes.", noteAgent)), nil, nil
		}
	}

	affected, err := db.SuppressNote(input.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: mcp: mem_forget suppress: %v\n", err)
		return errorResult("Error suppressing note. The database may be unavailable — try again."), nil, nil
	}
	if affected == 0 {
		return errorResult(fmt.Sprintf("No note found at path: %s", input.Path)), nil, nil
	}

	message := fmt.Sprintf("Suppressed: %s (%d chunks)", input.Path, affected)
	if input.Reason != "" {
		message += fmt.Sprintf("\nReason: %s", input.Reason)
	}
	message += "\nThe note still exists on disk but will not appear in search results."
	message += "\nTo undo, use mem_restore with the same path."

	return textResult(message), nil, nil
}

// memRestoreInput is the input for the mem_restore tool.
type memRestoreInput struct {
	Path string `json:"path" jsonschema:"Path of the suppressed note to restore"`
}

func handleMemRestore(ctx context.Context, req *mcp.CallToolRequest, input memRestoreInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Path) == "" {
		return errorResult("Error: path is required."), nil, nil
	}

	affected, err := db.UnsuppressNote(input.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: mcp: mem_restore unsuppress: %v\n", err)
		return errorResult("Error restoring note. The database may be unavailable — try again."), nil, nil
	}
	if affected == 0 {
		return errorResult(fmt.Sprintf("No suppressed note found at path: %s", input.Path)), nil, nil
	}

	return textResult(fmt.Sprintf("Restored: %s (%d chunks). The note will now appear in search results.", input.Path, affected)), nil, nil
}

func handleMemListSuppressed(ctx context.Context, req *mcp.CallToolRequest, input emptyInput) (*mcp.CallToolResult, any, error) {
	paths, err := db.ListSuppressed()
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: mcp: mem_list_suppressed: %v\n", err)
		return errorResult("Error listing suppressed notes."), nil, nil
	}

	if len(paths) == 0 {
		return textResult("No suppressed notes found."), nil, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Suppressed notes (%d):\n", len(paths)))
	for _, p := range paths {
		b.WriteString(fmt.Sprintf("  - %s\n", p))
	}
	b.WriteString("\nUse mem_restore to restore any of these notes.")
	return textResult(b.String()), nil, nil
}

func handleSaveKaizen(ctx context.Context, req *mcp.CallToolRequest, input saveKaizenInput) (*mcp.CallToolResult, any, error) {
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return errorResult("Error: description is required."), nil, nil
	}
	if len(description) > maxNoteSize {
		return errorResult("Error: description exceeds 100KB limit."), nil, nil
	}
	if !checkWriteRateLimit() {
		return errorResult("Error: too many write operations. Try again in a minute."), nil, nil
	}

	agent, err := normalizeAgent(input.Agent)
	if err != nil {
		return errorResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}

	// Build filename
	date := time.Now().Format("2006-01-02")
	slug := kaizenSlugify(description)
	if slug == "" {
		slug = "item"
	}
	relPath := fmt.Sprintf("kaizen/%s-%s.md", date, slug)
	fullPath := filepath.Join(vaultRoot, relPath)

	// Ensure kaizen directory exists
	kaizenDir := filepath.Join(vaultRoot, "kaizen")
	if err := os.MkdirAll(kaizenDir, 0o755); err != nil {
		return errorResult("Error: could not create kaizen directory. Check vault write permissions."), nil, nil
	}

	// Build content using YAML marshaler for injection safety
	type kaizenFrontmatter struct {
		Title       string   `yaml:"title"`
		ContentType string   `yaml:"content_type"`
		Status      string   `yaml:"status"`
		Agent       string   `yaml:"agent,omitempty"`
		Area        string   `yaml:"area,omitempty"`
		Tags        []string `yaml:"tags"`
	}
	fm := kaizenFrontmatter{
		Title:       description,
		ContentType: "kaizen",
		Status:      "open",
		Agent:       agent,
		Area:        strings.TrimSpace(input.Area),
		Tags:        []string{"kaizen"},
	}
	fmBytes, fmErr := yaml.Marshal(fm)
	if fmErr != nil {
		return errorResult("Error: could not build kaizen note."), nil, nil
	}

	var content strings.Builder
	mcpHeader := "<!-- Note saved via SAME MCP tool. Review before trusting. -->\n"
	content.WriteString(mcpHeader)
	content.WriteString("---\n")
	content.Write(fmBytes)
	content.WriteString("---\n\n")
	content.WriteString(description)
	content.WriteString("\n")

	// Write file
	if err := os.WriteFile(fullPath, []byte(content.String()), 0o600); err != nil {
		return errorResult("Error: could not write kaizen note. Check vault permissions and available disk space."), nil, nil
	}

	// Index the file
	if err := indexer.IndexSingleFile(db, fullPath, relPath, vaultRoot, embedClient); err != nil {
		return textResult(fmt.Sprintf("Saved: %s (index update failed — run reindex to fix)", relPath)), nil, nil
	}

	// Record provenance sources if provided
	recordProvenanceSources(relPath, input.Sources)

	return textResult(fmt.Sprintf("Saved: %s", relPath)), nil, nil
}

// kaizenSlugify converts a description into a filesystem-safe slug.
func kaizenSlugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_' || r == '/' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	result := strings.TrimRight(b.String(), "-")
	if len(result) > 60 {
		result = result[:60]
		result = strings.TrimRight(result, "-")
	}
	return result
}

// --- MCP brief helpers ---

func queryMCPBriefNotes(conn *sql.DB, query string, args ...interface{}) []mcpBriefNote {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notes []mcpBriefNote
	for rows.Next() {
		var n mcpBriefNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Text, &n.Modified, &n.ContentType, &n.Confidence); err != nil {
			continue
		}
		notes = append(notes, n)
	}
	return notes
}

func queryMCPBriefNotesWithAccess(conn *sql.DB, query string, args ...interface{}) []mcpBriefNote {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notes []mcpBriefNote
	for rows.Next() {
		var n mcpBriefNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Text, &n.Confidence, &n.AccessCount); err != nil {
			continue
		}
		notes = append(notes, n)
	}
	return notes
}

func buildMCPBriefPrompt(recent, sessions, decisions, highConf []mcpBriefNote) string {
	var b strings.Builder

	b.WriteString(`You are a briefing engine for a personal knowledge vault. Given the following vault contents, produce a concise orientation briefing.

RULES:
- Be extremely concise -- this is a briefing, not a report
- Lead with what's most actionable RIGHT NOW
- Flag any open decisions that need attention
- Note any contradictions or conflicts between notes
- Use bullet points, not paragraphs
- Maximum 15 lines total
- Do NOT add information beyond what's in the notes

`)

	b.WriteString("RECENT ACTIVITY:\n")
	if len(recent) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range recent {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s: %s\n", n.Path, n.Title, snippet)
		}
	}

	b.WriteString("\nSESSIONS:\n")
	if len(sessions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range sessions {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s: %s\n", n.Path, n.Title, snippet)
		}
	}

	b.WriteString("\nOPEN DECISIONS:\n")
	if len(decisions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range decisions {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s: %s\n", n.Path, n.Title, snippet)
		}
	}

	b.WriteString("\nHIGH-CONFIDENCE KNOWLEDGE:\n")
	if len(highConf) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, n := range highConf {
			snippet := n.Text
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			fmt.Fprintf(&b, "- [%s] %s (confidence: %.0f%%): %s\n", n.Path, n.Title, n.Confidence*100, snippet)
		}
	}

	b.WriteString("\nProduce the briefing now.")

	return b.String()
}

func computeMCPHealthScore(total, embedded, knowledge, recent, accessed int) int {
	if total == 0 {
		return 0
	}
	embedScore := float64(embedded) / float64(total) * 25.0
	knowledgeScore := float64(knowledge) / float64(total) * 10.0 * 25.0
	if knowledgeScore > 25.0 {
		knowledgeScore = 25.0
	}
	freshnessScore := float64(recent) / float64(total) * 25.0
	if freshnessScore > 25.0 {
		freshnessScore = 25.0
	}
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

func healthLabel(score int) string {
	switch {
	case score >= 80:
		return "Excellent"
	case score >= 60:
		return "Good"
	case score >= 40:
		return "Fair"
	default:
		return "Needs attention"
	}
}

// Helpers

func formatTimestamp(ts float64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02 15:04")
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

// incrementAccessCounts increments the access_count for all surfaced search
// results. Called fire-and-forget after search results are returned so that
// frequently-useful notes strengthen over time (reconsolidation).
func incrementAccessCounts(results []store.SearchResult) {
	if len(results) == 0 || db == nil {
		return
	}
	paths := make([]string, len(results))
	for i, r := range results {
		paths[i] = r.Path
	}
	_ = db.IncrementAccessCount(paths)
}

// sanitizeResultSnippets neutralizes XML-like tags in search result snippets
// that could enable prompt injection when returned to an AI agent via MCP.
// This mirrors the tag list in hooks/text_processing.go sanitizeContextTags().
func sanitizeResultSnippets(results []store.SearchResult) []store.SearchResult {
	for i := range results {
		results[i].Snippet = neutralizeTags(results[i].Snippet)
	}
	return results
}

// searchWithFallback tries HybridSearch (vector+keyword), then FTS5, then
// pure keyword search. This mirrors the graceful degradation in FederatedSearch
// and ensures MCP search works even when Ollama is unavailable.
func searchWithFallback(query string, opts store.SearchOptions) ([]store.SearchResult, error) {
	// Infer content-type boosts from query keywords
	if opts.QueryTypeBoosts == nil {
		opts.QueryTypeBoosts = memory.InferQueryTypeBoost(query)
	}

	// Auto-detect metadata queries (trust/confidence/provenance) and apply
	// trust_state filter if the caller didn't already set one.
	metaHints := memory.InferMetadataFilters(query)
	if opts.TrustState == "" && metaHints.TrustState != "" {
		opts.TrustState = metaHints.TrustState
	}

	// Try vector+keyword hybrid search first
	var queryVec []float32
	if embedClient != nil && db.HasVectors() {
		if mismatchErr := db.CheckEmbeddingMeta(embedClient.Name(), embedClient.Model(), embedClient.Dimensions()); mismatchErr != nil {
			fmt.Fprintf(os.Stderr, "same: warning: %v\n", mismatchErr)
			embedClient = nil
		}
	}
	if embedClient != nil {
		queryVec, _ = embedClient.GetQueryEmbedding(query)
	}

	var results []store.SearchResult
	var err error

	if queryVec != nil && db.HasVectors() {
		results, err = db.HybridSearch(queryVec, query, opts)
	} else if db.FTSAvailable() {
		// Fall back to FTS5 full-text search
		results, err = db.FTS5Search(query, opts)
	} else {
		// Final fallback: keyword search on title/text
		terms := store.ExtractSearchTerms(query)
		if len(terms) == 0 {
			return nil, nil
		}
		raw, kwErr := db.KeywordSearch(terms, opts.TopK)
		if kwErr != nil {
			return nil, kwErr
		}
		for _, r := range raw {
			if !matchesSearchOptions(r, opts) {
				continue
			}
			results = append(results, store.RawToSearchResult(r, 0.5))
		}
	}

	if err != nil {
		return nil, err
	}

	// For metadata queries, supplement with MetadataFilterSearch to catch
	// notes that match by metadata even if they didn't match by content.
	if metaHints.IsMetadataQuery {
		metaResults, metaErr := db.MetadataFilterSearch(store.SearchOptions{
			TopK:        opts.TopK,
			Domain:      opts.Domain,
			Workstream:  opts.Workstream,
			Agent:       opts.Agent,
			TrustState:  opts.TrustState,
			ContentType: opts.ContentType,
			Tags:        opts.Tags,
		})
		if metaErr == nil && len(metaResults) > 0 {
			seen := make(map[string]bool, len(metaResults))
			var merged []store.SearchResult
			for _, mr := range metaResults {
				seen[mr.Path] = true
				merged = append(merged, mr)
			}
			for _, r := range results {
				if seen[r.Path] {
					continue
				}
				merged = append(merged, r)
			}
			if len(merged) > opts.TopK {
				merged = merged[:opts.TopK]
			}
			results = merged
		}
	}

	return results, nil
}

func sanitizeFederatedSnippets(results []store.FederatedResult) []store.FederatedResult {
	for i := range results {
		results[i].Snippet = neutralizeTags(results[i].Snippet)
	}
	return results
}

func matchesSearchOptions(r store.RawSearchResult, opts store.SearchOptions) bool {
	if opts.Domain != "" && !strings.EqualFold(r.Domain, opts.Domain) {
		return false
	}
	if opts.Workstream != "" && !strings.EqualFold(r.Workstream, opts.Workstream) {
		return false
	}
	if opts.Agent != "" && !strings.EqualFold(r.Agent, opts.Agent) {
		return false
	}
	if opts.TrustState != "" && !strings.EqualFold(r.TrustState, opts.TrustState) {
		return false
	}
	if opts.ContentType != "" && !strings.EqualFold(r.ContentType, opts.ContentType) {
		return false
	}
	if len(opts.Tags) == 0 {
		return true
	}

	noteTags := store.ParseTags(r.Tags)
	if len(noteTags) == 0 {
		return false
	}
	noteSet := make(map[string]bool, len(noteTags))
	for _, t := range noteTags {
		noteSet[strings.ToLower(strings.TrimSpace(t))] = true
	}
	for _, required := range opts.Tags {
		if noteSet[strings.ToLower(strings.TrimSpace(required))] {
			return true
		}
	}
	return false
}

func normalizeAgent(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", nil
	}
	if strings.ContainsRune(clean, 0) || strings.Contains(clean, "\n") || strings.Contains(clean, "\r") {
		return "", fmt.Errorf("invalid agent")
	}
	for _, r := range clean {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid agent")
		}
	}
	if len(clean) > 128 {
		return "", fmt.Errorf("agent too long")
	}
	return clean, nil
}

func hasWindowsDrivePrefix(path string) bool {
	if len(path) < 3 {
		return false
	}
	ch := path[0]
	isLetter := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
	return isLetter && path[1] == ':' && path[2] == '/'
}

func upsertAgentFrontmatter(content, agent string) string {
	if agent == "" {
		return content
	}
	if strings.HasPrefix(content, "---\n") {
		rest := content[len("---\n"):]
		idx := strings.Index(rest, "\n---")
		if idx >= 0 {
			block := rest[:idx]
			tail := rest[idx+len("\n---"):]
			lines := strings.Split(block, "\n")
			updated := false
			for i, line := range lines {
				trimmed := strings.TrimSpace(strings.ToLower(line))
				if strings.HasPrefix(trimmed, "agent:") {
					lines[i] = fmt.Sprintf("agent: %q", agent)
					updated = true
					break
				}
			}
			if !updated {
				lines = append(lines, fmt.Sprintf("agent: %q", agent))
			}
			block = strings.Join(lines, "\n")
			if strings.HasPrefix(tail, "\n") {
				return "---\n" + block + "\n---" + tail
			}
			return "---\n" + block + "\n---\n" + tail
		}
	}
	return fmt.Sprintf("---\nagent: %q\n---\n\n%s", agent, content)
}

func injectProvenanceHeader(content, header string) string {
	if strings.HasPrefix(content, "---\n") {
		rest := content[len("---\n"):]
		idx := strings.Index(rest, "\n---")
		if idx >= 0 {
			headLen := len("---\n") + idx + len("\n---")
			head := content[:headLen]
			tail := content[headLen:]
			tail = strings.TrimPrefix(tail, "\n")
			return head + "\n\n" + header + "\n\n" + tail
		}
	}
	return header + "\n" + content
}

// neutralizeTags replaces potentially dangerous XML tags and LLM-specific
// injection delimiters with bracket equivalents.
func neutralizeTags(text string) string {
	tags := []string{
		"vault-context", "plugin-context", "session-bootstrap",
		"vault-handoff", "vault-decisions", "same-diagnostic",
		"system-reminder", "system", "instructions",
		"tool_result", "tool_use", "important",
	}

	// LLM-specific injection patterns (Llama/Mistral [INST], <<SYS>>, XML CDATA).
	type literalPattern struct {
		pattern     string
		replacement string
	}
	llmPatterns := []literalPattern{
		{"[inst]", "[[inst]]"},
		{"[/inst]", "[[/inst]]"},
		{"<<sys>>", "[[sys]]"},
		{"<</sys>>", "[[/sys]]"},
		{"<![cdata[", "[CDATA["},
		{"]]>", "]]&gt;"},
	}

	lower := strings.ToLower(text)
	var result strings.Builder
	result.Grow(len(text))
	i := 0
	for i < len(text) {
		matched := false

		// Check LLM-specific literal patterns first
		for _, lp := range llmPatterns {
			if i+len(lp.pattern) <= len(text) && lower[i:i+len(lp.pattern)] == lp.pattern {
				result.WriteString(lp.replacement)
				i += len(lp.pattern)
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		for _, tag := range tags {
			closeTag := "</" + tag + ">"
			openTag := "<" + tag + ">"
			openTagAttr := "<" + tag + " " // tag with attributes
			selfClose := "<" + tag + "/>"
			if i+len(closeTag) <= len(text) && lower[i:i+len(closeTag)] == closeTag {
				result.WriteString("[/" + tag + "]")
				i += len(closeTag)
				matched = true
				break
			}
			if i+len(selfClose) <= len(text) && lower[i:i+len(selfClose)] == selfClose {
				result.WriteString("[" + tag + "/]")
				i += len(selfClose)
				matched = true
				break
			}
			if i+len(openTag) <= len(text) && lower[i:i+len(openTag)] == openTag {
				result.WriteString("[" + tag + "]")
				i += len(openTag)
				matched = true
				break
			}
			if i+len(openTagAttr) <= len(text) && lower[i:i+len(openTagAttr)] == openTagAttr {
				result.WriteString("[" + tag + " ")
				i += len(openTagAttr)
				matched = true
				break
			}
		}
		if !matched {
			result.WriteByte(text[i])
			i++
		}
	}
	return result.String()
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

// safeVaultPath resolves a relative path within the vault, blocking traversal attacks,
// access to _PRIVATE/ content, writes to dot-directories (.same/, .git/, etc.),
// and symlink escapes from the vault boundary.
func safeVaultPath(path string) string {
	// SECURITY: reject paths containing null bytes (can bypass C-level path checks)
	if strings.ContainsRune(path, 0) {
		return ""
	}
	// SECURITY: reject URL-encoded traversal patterns before any normalization.
	// Catches %2e%2e%2f (../), %2e%2e/ (..) and other encoded traversal attempts.
	lowerPath := strings.ToLower(path)
	if strings.Contains(lowerPath, "%2e") || strings.Contains(lowerPath, "%2f") || strings.Contains(lowerPath, "%5c") || strings.Contains(lowerPath, "%00") {
		return ""
	}
	// SECURITY: reject Unicode fullwidth characters that could normalize to
	// traversal sequences. Fullwidth period (U+FF0E) and fullwidth solidus
	// (U+FF0F) can NFKC-normalize to '.' and '/' respectively.
	for _, r := range path {
		if r == '\uff0e' || r == '\uff0f' || r == '\uff3c' { // fullwidth . / and \
			return ""
		}
	}
	// SECURITY: normalize backslashes before any checks so traversal patterns
	// like "..\" are caught on all platforms.
	normalizedInput := strings.ReplaceAll(path, "\\", "/")
	if hasWindowsDrivePrefix(normalizedInput) {
		return ""
	}
	if filepath.IsAbs(normalizedInput) {
		return ""
	}
	// SECURITY: block access to _PRIVATE/ directory (case-insensitive for macOS)
	clean := filepath.ToSlash(filepath.Clean(normalizedInput))
	upper := strings.ToUpper(clean)
	if strings.HasPrefix(upper, "_PRIVATE/") || upper == "_PRIVATE" {
		return ""
	}
	// SECURITY: block access to dot-directories and dot-files in any path segment.
	// Prevents writes to hidden locations like .same/, .git/, .obsidian/, or notes/.hidden/.
	for _, part := range strings.Split(clean, "/") {
		if strings.HasPrefix(part, ".") {
			return ""
		}
	}
	full, err := filepath.Abs(filepath.Join(config.VaultPath(), filepath.FromSlash(normalizedInput)))
	if err != nil {
		return ""
	}
	if !pathWithin(vaultRoot, full) {
		return ""
	}

	// SECURITY: resolve symlinks and verify the real path is still within the vault.
	// A symlink inside the vault could point to an arbitrary location outside it.
	resolvedVault, err := filepath.EvalSymlinks(vaultRoot)
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		// If the file doesn't exist yet (e.g., save_note creating new dirs),
		// walk up the path until we find an existing ancestor and verify it's
		// still within the vault boundary.
		ancestor := full
		for {
			ancestor = filepath.Dir(ancestor)
			if ancestor == "." || ancestor == string(filepath.Separator) {
				return ""
			}
			resolvedAncestor, aerr := filepath.EvalSymlinks(ancestor)
			if aerr != nil {
				continue
			}
			if !pathWithin(resolvedVault, resolvedAncestor) {
				return ""
			}
			return full
		}
	}
	if !pathWithin(resolvedVault, resolved) {
		return ""
	}
	return full
}

func pathWithin(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

// filterPrivatePaths removes _PRIVATE/ results from search output (defense-in-depth).
// Uses case-insensitive comparison for macOS compatibility.
func filterPrivatePaths(results []store.SearchResult) []store.SearchResult {
	filtered := results[:0]
	for _, r := range results {
		upper := strings.ToUpper(r.Path)
		if !strings.HasPrefix(upper, "_PRIVATE/") && !strings.HasPrefix(upper, "_PRIVATE\\") {
			filtered = append(filtered, r)
		}
	}
	return filtered
}
