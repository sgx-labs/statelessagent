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
	provCfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
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
	vaultRoot, _ = filepath.Abs(config.VaultPath())

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "same",
		Version: Version,
	}, nil)

	registerTools(server)

	return server.Run(context.Background(), &mcp.StdioTransport{})
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
		Description: "Search the user's knowledge base with metadata filters. Use this when you want to narrow results by domain (e.g. 'engineering'), workstream (e.g. 'api-redesign'), tags, or agent attribution.\n\nArgs:\n  query: Natural language search query\n  top_k: Number of results (default 10, max 100)\n  domain: Filter by domain (e.g. 'engineering', 'product')\n  workstream: Filter by workstream/project name\n  tags: Comma-separated tags to filter by\n  agent: Filter by agent attribution (e.g. 'codex', 'claude')\n\nReturns filtered ranked list.",
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
		Description: "Create or update a markdown note in the vault. The note is written to disk and indexed automatically.\n\nArgs:\n  path: Relative path within the vault (e.g. 'decisions/auth-approach.md')\n  content: Markdown content to write\n  append: If true, append to existing file instead of overwriting (default false)\n  agent: Optional writer attribution stored in frontmatter (e.g. 'codex')\n\nReturns confirmation with the saved path.",
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
	Agent      string `json:"agent,omitempty" jsonschema:"Filter by agent attribution"`
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
	Path    string `json:"path" jsonschema:"Relative path within the vault (e.g. decisions/auth.md)"`
	Content string `json:"content" jsonschema:"Markdown content to write"`
	Append  bool   `json:"append" jsonschema:"Append to existing file instead of overwriting"`
	Agent   string `json:"agent,omitempty" jsonschema:"Optional writer attribution (e.g. codex)"`
}

type saveDecisionInput struct {
	Title  string `json:"title" jsonschema:"Short decision title"`
	Body   string `json:"body" jsonschema:"Full decision details"`
	Status string `json:"status" jsonschema:"accepted, proposed, or superseded (default accepted)"`
	Agent  string `json:"agent,omitempty" jsonschema:"Optional writer attribution (e.g. codex)"`
}

type createHandoffInput struct {
	Summary  string `json:"summary" jsonschema:"What was accomplished this session"`
	Pending  string `json:"pending" jsonschema:"What is left to do"`
	Blockers string `json:"blockers" jsonschema:"Any blockers or open questions"`
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

// Tool handlers

func handleSearchNotes(ctx context.Context, req *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Query) == "" {
		return textResult("Error: query is required."), nil, nil
	}
	if len(input.Query) > maxQueryLen {
		return textResult("Error: query too long (max 10,000 characters)."), nil, nil
	}
	topK := clampTopK(input.TopK, 10)
	opts := store.SearchOptions{TopK: topK}

	results, err := searchWithFallback(input.Query, opts)
	if err != nil {
		return textResult("Search error. Try running reindex() first."), nil, nil
	}
	results = filterPrivatePaths(results)
	results = sanitizeResultSnippets(results)
	if len(results) == 0 {
		return textResult("No results found. The index may be empty — try running reindex() first."), nil, nil
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
}

func handleSearchNotesFiltered(ctx context.Context, req *mcp.CallToolRequest, input searchFilteredInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Query) == "" {
		return textResult("Error: query is required."), nil, nil
	}
	if len(input.Query) > maxQueryLen {
		return textResult("Error: query too long (max 10,000 characters)."), nil, nil
	}
	agentFilter, err := normalizeAgent(input.Agent)
	if err != nil {
		return textResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
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
		TopK:       topK,
		Domain:     input.Domain,
		Workstream: input.Workstream,
		Agent:      agentFilter,
		Tags:       tags,
	}

	results, err := searchWithFallback(input.Query, opts)
	if err != nil {
		return textResult("Search error. Try running reindex() first."), nil, nil
	}
	results = filterPrivatePaths(results)
	results = sanitizeResultSnippets(results)
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

	// F04: Check file size before reading to prevent OOM on very large files
	info, err := os.Stat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return textResult("File not found."), nil, nil
		}
		return textResult("Error reading file."), nil, nil
	}
	if info.Size() > maxReadSize {
		return textResult(fmt.Sprintf("Error: file too large (%dKB). Maximum is %dKB.", info.Size()/1024, maxReadSize/1024)), nil, nil
	}

	content, err := os.ReadFile(safePath)
	if err != nil {
		return textResult("Error reading file."), nil, nil
	}

	// SECURITY: Neutralize XML-like tags that could enable prompt injection
	// when the note content is returned to an AI agent via MCP.
	return textResult(neutralizeTags(string(content))), nil, nil
}

func handleFindSimilar(ctx context.Context, req *mcp.CallToolRequest, input similarInput) (*mcp.CallToolResult, any, error) {
	topK := clampTopK(input.TopK, 5)

	// Validate path through safeVaultPath to prevent probing _PRIVATE/ or dot-dirs
	if safeVaultPath(input.Path) == "" {
		return textResult("Error: invalid note path."), nil, nil
	}

	if !db.HasVectors() {
		return textResult("Similar notes requires semantic search (embeddings). Install Ollama and run reindex() to enable."), nil, nil
	}

	noteVec, err := db.GetNoteEmbedding(input.Path)
	if err != nil || noteVec == nil {
		return textResult(fmt.Sprintf("No similar notes found for: %s. Is the note in the index?", input.Path)), nil, nil
	}

	// Fetch extra results, excluding the source note
	allResults, err := db.VectorSearch(noteVec, store.SearchOptions{TopK: topK + 10})
	if err != nil {
		return textResult("Search error. Try running reindex() first."), nil, nil
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
		return textResult("Reindex error. Check that the vault path is accessible and Ollama is running."), nil, nil
	}

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
		return textResult("Error: path is required."), nil, nil
	}
	if strings.TrimSpace(input.Content) == "" {
		return textResult("Error: content is required."), nil, nil
	}
	if len(input.Content) > maxNoteSize {
		return textResult("Error: content exceeds 100KB limit."), nil, nil
	}
	agent, err := normalizeAgent(input.Agent)
	if err != nil {
		return textResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}

	// S21: Only allow .md files to be saved via MCP
	if !strings.HasSuffix(strings.ToLower(input.Path), ".md") {
		return textResult("Error: only .md (markdown) files can be saved via MCP."), nil, nil
	}

	safePath := safeVaultPath(input.Path)
	if safePath == "" {
		return textResult("Error: path must be a relative path within the vault. Cannot write to _PRIVATE/."), nil, nil
	}
	relPath, relErr := store.NormalizeClaimPath(input.Path)
	if relErr != nil {
		return textResult("Error: path must stay within the vault. Use a relative path like 'notes/topic.md'."), nil, nil
	}
	if !checkWriteRateLimit() {
		return textResult("Error: too many write operations. Try again in a minute."), nil, nil
	}

	// S11: Prepend a provenance header so readers know this was MCP-generated.
	// This helps mitigate stored prompt injection by clearly marking
	// machine-written content when it is later surfaced to an agent.
	mcpHeader := "<!-- Note saved via SAME MCP tool. Review before trusting. -->"

	// Ensure parent directory exists
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return textResult("Error: could not create destination directory. Check vault write permissions."), nil, nil
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
				return textResult("Error: could not write note file. Check vault permissions and available disk space."), nil, nil
			}
		} else {
			if statErr != nil {
				return textResult("Error: could not open note for appending. Check file permissions and lock state."), nil, nil
			}
			if agent != "" {
				existing, readErr := os.ReadFile(safePath)
				if readErr == nil {
					updated := upsertAgentFrontmatter(string(existing), agent)
					if updated != string(existing) {
						if writeErr := os.WriteFile(safePath, []byte(updated), 0o600); writeErr != nil {
							return textResult("Error: could not update note metadata. The note was not modified; check file permissions."), nil, nil
						}
					}
				}
			}

				f, err := os.OpenFile(safePath, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					return textResult("Error: could not open note for appending. Check file permissions and lock state."), nil, nil
				}
				// F14: Add provenance marker for appended MCP content
				_, err = f.WriteString("\n<!-- Appended via SAME MCP tool -->\n" + input.Content)
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					return textResult("Error: could not append note content. Check vault permissions and available disk space."), nil, nil
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
			return textResult("Error: could not write note file. Check vault permissions and available disk space."), nil, nil
		}
	}

	// S7: Index only the saved file instead of triggering a full vault reindex.
	// This avoids O(n) work per save_note call, preventing DoS on large vaults.
	if err := indexer.IndexSingleFile(db, safePath, relPath, vaultRoot, embedClient); err != nil {
		// Non-fatal: the note was saved, just not indexed yet
		return textResult(fmt.Sprintf("Saved: %s (index update failed — run reindex to fix)", input.Path)), nil, nil
	}

	message := fmt.Sprintf("Saved: %s", input.Path)
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
	return textResult(message), nil, nil
}

func handleSaveDecision(ctx context.Context, req *mcp.CallToolRequest, input saveDecisionInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Title) == "" {
		return textResult("Error: title is required."), nil, nil
	}
	if strings.TrimSpace(input.Body) == "" {
		return textResult("Error: body is required."), nil, nil
	}
	if len(input.Title)+len(input.Body) > maxNoteSize {
		return textResult(fmt.Sprintf("Error: decision content too large (max %dKB).", maxNoteSize/1024)), nil, nil
	}
	agent, err := normalizeAgent(input.Agent)
	if err != nil {
		return textResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
	}

	status := input.Status
	if status == "" {
		status = "accepted"
	}
	if status != "accepted" && status != "proposed" && status != "superseded" {
		return textResult("Error: status must be 'accepted', 'proposed', or 'superseded'."), nil, nil
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
		return textResult("Error: decision log path is invalid. Set `vault.decision_log` to a relative file under the vault."), nil, nil
	}
	if !checkWriteRateLimit() {
		return textResult("Error: too many write operations. Try again in a minute."), nil, nil
	}
	if agent != "" {
		if existing, readErr := os.ReadFile(safePath); readErr == nil {
			updated := upsertAgentFrontmatter(string(existing), agent)
			if updated != string(existing) {
				if writeErr := os.WriteFile(safePath, []byte(updated), 0o600); writeErr != nil {
					return textResult("Error: could not update decision log metadata. Check file permissions."), nil, nil
				}
			}
		} else if os.IsNotExist(readErr) {
			initial := upsertAgentFrontmatter("", agent)
			if writeErr := os.WriteFile(safePath, []byte(initial), 0o600); writeErr != nil {
				return textResult("Error: could not initialize decision log metadata. Check vault permissions."), nil, nil
			}
		}
	}

	// Append to decision log
	f, err := os.OpenFile(safePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return textResult("Error: could not open decision log for writing. Check file permissions."), nil, nil
	}
	_, err = f.WriteString(entry)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return textResult("Error: could not write to decision log. Check available disk space and permissions."), nil, nil
	}

	// Index only the decision log file instead of a full vault reindex.
	// This avoids O(n) work per call, preventing DoS on large vaults.
	relPath := filepath.ToSlash(logName)
	if err := indexer.IndexSingleFile(db, safePath, relPath, vaultRoot, embedClient); err != nil {
		// Non-fatal: the decision was saved, just not indexed yet
	}

	return textResult(fmt.Sprintf("Decision logged: %s (%s)", input.Title, status)), nil, nil
}

func handleCreateHandoff(ctx context.Context, req *mcp.CallToolRequest, input createHandoffInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Summary) == "" {
		return textResult("Error: summary is required."), nil, nil
	}
	totalSize := len(input.Summary) + len(input.Pending) + len(input.Blockers)
	if totalSize > maxNoteSize {
		return textResult(fmt.Sprintf("Error: handoff content too large (max %dKB).", maxNoteSize/1024)), nil, nil
	}
	agent, err := normalizeAgent(input.Agent)
	if err != nil {
		return textResult("Error: invalid agent value. Use 1-128 visible characters without newlines."), nil, nil
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
		return textResult("Error: handoff path is invalid. Set `vault.handoff_dir` to a relative directory under the vault."), nil, nil
	}
	if !checkWriteRateLimit() {
		return textResult("Error: too many write operations. Try again in a minute."), nil, nil
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
		return textResult("Error: could not create handoff directory. Check vault write permissions."), nil, nil
	}
	if err := os.WriteFile(safePath, []byte(buf.String()), 0o600); err != nil {
		return textResult("Error: could not write handoff note. Check vault permissions and available disk space."), nil, nil
	}

	// Index only the handoff file instead of a full vault reindex.
	if err := indexer.IndexSingleFile(db, safePath, filepath.ToSlash(relPath), vaultRoot, embedClient); err != nil {
		// Non-fatal: the handoff was saved, just not indexed yet
	}

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
		return textResult("Error fetching recent notes. Try running reindex() first."), nil, nil
	}
	if len(notes) == 0 {
		return textResult("No notes found. The index may be empty — try running reindex() first."), nil, nil
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
		return textResult("Error: query is required."), nil, nil
	}
	if len(input.Query) > maxQueryLen {
		return textResult("Error: query too long (max 10,000 characters)."), nil, nil
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
		return textResult("No searchable vaults found. Register vaults with 'same vault add <name> <path>'."), nil, nil
	}

	// Try to get query embedding
	var queryVec []float32
	if embedClient != nil {
		queryVec, _ = embedClient.GetQueryEmbedding(input.Query)
	}

	results, err := store.FederatedSearch(vaultDBPaths, queryVec, input.Query, store.SearchOptions{TopK: topK})
	if err != nil {
		return textResult("Federated search error."), nil, nil
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
		return textResult(fmt.Sprintf("No results found across %d vault(s).", len(vaultDBPaths))), nil, nil
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return textResult(string(data)), nil, nil
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
	// Try vector+keyword hybrid search first
	var queryVec []float32
	if embedClient != nil {
		queryVec, _ = embedClient.GetQueryEmbedding(query)
	}
	if queryVec != nil && db.HasVectors() {
		return db.HybridSearch(queryVec, query, opts)
	}

	// Fall back to FTS5 full-text search
	if db.FTSAvailable() {
		return db.FTS5Search(query, opts)
	}

	// Final fallback: keyword search on title/text
	terms := store.ExtractSearchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	raw, err := db.KeywordSearch(terms, opts.TopK)
	if err != nil {
		return nil, err
	}
	var results []store.SearchResult
	for _, r := range raw {
		if !matchesSearchOptions(r, opts) {
			continue
		}
		snippet := r.Text
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		results = append(results, store.SearchResult{
			Path:         r.Path,
			Title:        r.Title,
			ChunkHeading: r.Heading,
			Score:        0.5,
			Snippet:      snippet,
			Domain:       r.Domain,
			Workstream:   r.Workstream,
			Agent:        r.Agent,
			Tags:         r.Tags,
			ContentType:  r.ContentType,
			Confidence:   r.Confidence,
		})
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
	if !strings.HasPrefix(full, vaultRoot+string(filepath.Separator)) && full != vaultRoot {
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
			if !strings.HasPrefix(resolvedAncestor, resolvedVault+string(filepath.Separator)) && resolvedAncestor != resolvedVault {
				return ""
			}
			return full
		}
	}
	if !strings.HasPrefix(resolved, resolvedVault+string(filepath.Separator)) && resolved != resolvedVault {
		return ""
	}
	return full
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
