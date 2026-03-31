// Package consolidate provides the knowledge consolidation engine.
// It finds clusters of related notes, extracts key facts using an LLM,
// detects and resolves contradictions, and writes consolidated knowledge files.
package consolidate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// EmbedProvider is the subset of embedding.Provider needed for similarity grouping.
// When nil, the engine falls back to keyword-based grouping.
type EmbedProvider interface {
	GetQueryEmbedding(text string) ([]float32, error)
}

// Result holds the output of a consolidation run.
type Result struct {
	GroupsFound    int
	GroupsSkipped  int
	NotesProcessed int
	NotesCreated   int
	ConflictsFound int
	FactsExtracted int
	DryRun         bool
	Groups         []Group
}

// Group represents a cluster of related notes that can be consolidated.
type Group struct {
	Theme       string
	SourceNotes []SourceNote
	Output      string
	OutputPath  string
	Facts       []string
	Conflicts   []Conflict
}

// SourceNote is a note that belongs to a consolidation group.
type SourceNote struct {
	Path     string
	Title    string
	Modified float64
	Snippet  string
}

// Conflict represents a contradiction between two facts from different sources.
type Conflict struct {
	Fact1      string
	Source1    string
	Fact2      string
	Source2    string
	Resolution string
}

// Engine orchestrates the consolidation process.
type Engine struct {
	db        *store.DB
	chat      llm.Client
	embed     EmbedProvider
	model     string
	vaultPath string
	threshold float64
}

// NewEngine creates a new consolidation engine.
// embedClient may be nil; the engine will fall back to keyword-based grouping.
func NewEngine(db *store.DB, chat llm.Client, embedClient EmbedProvider, model, vaultPath string, threshold float64) *Engine {
	if threshold <= 0 {
		threshold = 0.75
	}
	return &Engine{
		db:        db,
		chat:      chat,
		embed:     embedClient,
		model:     model,
		vaultPath: vaultPath,
		threshold: threshold,
	}
}

// Run executes the consolidation pipeline. When dryRun is true, groups are
// identified and the LLM is called, but no files are written to disk.
func (e *Engine) Run(dryRun bool) (*Result, error) {
	result := &Result{DryRun: dryRun}

	// 1. Load all root-chunk notes from the database.
	fmt.Fprintf(os.Stderr, "same: consolidate: loading notes...\n")
	notes, err := e.loadNotes()
	if err != nil {
		return nil, fmt.Errorf("load notes: %w", err)
	}
	if len(notes) == 0 {
		fmt.Fprintf(os.Stderr, "same: consolidate: no notes found\n")
		return result, nil
	}
	fmt.Fprintf(os.Stderr, "same: consolidate: loaded %d notes\n", len(notes))

	// 2. Group similar notes.
	fmt.Fprintf(os.Stderr, "same: consolidate: grouping notes (threshold %.2f)...\n", e.threshold)
	groups, err := GroupNotes(e.db, notes, e.threshold)
	if err != nil {
		return nil, fmt.Errorf("group notes: %w", err)
	}
	result.GroupsFound = len(groups)
	if len(groups) == 0 {
		fmt.Fprintf(os.Stderr, "same: consolidate: no groups found (notes may be too dissimilar)\n")
		return result, nil
	}
	fmt.Fprintf(os.Stderr, "same: consolidate: found %d groups\n", len(groups))

	// 3. For each group, call the LLM to consolidate.
	knowledgeDir := filepath.Join(e.vaultPath, "knowledge")
	for i, group := range groups {
		groupStart := time.Now()
		fmt.Fprintf(os.Stderr, "same: consolidate: processing group %d/%d (%d notes)...\n",
			i+1, len(groups), len(group))

		g, err := e.consolidateGroup(group)
		elapsed := time.Since(groupStart)
		if err != nil {
			fmt.Fprintf(os.Stderr, "same: consolidate: group %d: LLM error: %v (skipping, %.1fs)\n", i+1, err, elapsed.Seconds())
			result.GroupsSkipped++
			continue
		}

		fmt.Fprintf(os.Stderr, "same: consolidate: group %d/%d done (%.1fs)\n", i+1, len(groups), elapsed.Seconds())

		result.NotesProcessed += len(g.SourceNotes)
		result.FactsExtracted += len(g.Facts)
		result.ConflictsFound += len(g.Conflicts)

		// Determine output path.
		slug := slugify(g.Theme)
		if slug == "" {
			slug = fmt.Sprintf("group-%d", i+1)
		}
		g.OutputPath = filepath.Join("knowledge", slug+".md")

		// 4. Write consolidated note (unless dry-run).
		if !dryRun {
			absPath := filepath.Join(e.vaultPath, g.OutputPath)
			if err := writeConsolidatedNote(knowledgeDir, absPath, g.Output); err != nil {
				fmt.Fprintf(os.Stderr, "same: consolidate: write %s: %v (skipping)\n", g.OutputPath, err)
				continue
			}
			result.NotesCreated++
		}

		result.Groups = append(result.Groups, g)
	}

	return result, nil
}

// loadNotes fetches all root-chunk notes, excluding _PRIVATE/ and knowledge/.
func (e *Engine) loadNotes() ([]NoteData, error) {
	rows, err := e.db.Conn().Query(`
		SELECT id, path, title, text, modified, content_type, confidence, tags
		FROM vault_notes
		WHERE chunk_id = 0
		  AND path NOT LIKE '_PRIVATE/%'
		  AND path NOT LIKE 'knowledge/%'
		  AND COALESCE(suppressed, 0) = 0
		ORDER BY modified DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []NoteData
	for rows.Next() {
		var n NoteData
		if err := rows.Scan(&n.ID, &n.Path, &n.Title, &n.Text, &n.Modified,
			&n.ContentType, &n.Confidence, &n.Tags); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// consolidateGroup calls the LLM to merge a group of related notes.
func (e *Engine) consolidateGroup(noteGroup []NoteData) (Group, error) {
	// Build source notes for display.
	sources := make([]SourceNote, len(noteGroup))
	for i, n := range noteGroup {
		snippet := n.Text
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		sources[i] = SourceNote{
			Path:     n.Path,
			Title:    n.Title,
			Modified: n.Modified,
			Snippet:  snippet,
		}
	}

	// Build the LLM prompt.
	prompt := buildConsolidationPrompt(noteGroup)

	// Call the LLM.
	output, err := e.chat.Generate(e.model, prompt)
	if err != nil {
		return Group{}, fmt.Errorf("LLM generate: %w", err)
	}

	// Parse theme, facts, and conflicts from the LLM output.
	theme, facts, conflicts := parseConsolidationOutput(output)

	return Group{
		Theme:       theme,
		SourceNotes: sources,
		Output:      output,
		Facts:       facts,
		Conflicts:   conflicts,
	}, nil
}

// writeConsolidatedNote writes a consolidated note to disk.
// Content is sanitized to prevent prompt injection via crafted LLM output.
func writeConsolidatedNote(knowledgeDir, absPath, content string) error {
	if err := os.MkdirAll(knowledgeDir, 0o700); err != nil {
		return fmt.Errorf("create knowledge dir: %w", err)
	}
	// Sanitize LLM output: strip structural tags that could be used
	// for prompt injection if the consolidated note is later surfaced
	// as agent context.
	content = sanitizeConsolidatedOutput(content)
	return os.WriteFile(absPath, []byte(content), 0o600)
}

// sanitizeConsolidatedOutput strips dangerous structural tags from LLM output
// before writing to the vault. This prevents a compromised or prompt-injected
// LLM from producing notes that contain instruction-override payloads.
func sanitizeConsolidatedOutput(text string) string {
	// Strip XML-style wrapper tags used by SAME's context system and common
	// LLM system-level tags that could be injected via prompt injection.
	dangerousTags := []string{
		// SAME internal context tags
		"same-context", "session-bootstrap", "vault-handoff",
		"vault-decisions", "vault-staleness", "vault-source-divergence",
		"same-diagnostic", "same-guidance",
		// MCP / LLM system-level tags
		"system", "system-reminder", "system-prompt", "system_prompt",
		"tool_result", "tool_use", "tool_call",
		"function_call", "function_result",
		// Agent instruction tags
		"instructions", "assistant_instructions", "user_instructions",
		"context", "hidden", "internal",
		// Anthropic-specific tags
		"antml:thinking", "antml:invoke", "antml:function_calls",
	}
	for _, tag := range dangerousTags {
		text = strings.ReplaceAll(text, "<"+tag+">", "")
		text = strings.ReplaceAll(text, "</"+tag+">", "")
		text = strings.ReplaceAll(text, "<"+tag+" ", "")
	}
	return text
}

// buildConsolidationPrompt constructs the LLM prompt for a group of notes.
func buildConsolidationPrompt(notes []NoteData) string {
	var b strings.Builder

	b.WriteString(`You are a knowledge consolidation engine. Given multiple related notes from a personal knowledge vault, extract and merge the key information.

RULES:
- Extract discrete facts, decisions, preferences, and patterns
- If notes contradict each other, keep the most recent information and flag the conflict
- Preserve specific details (names, versions, dates, commands, code snippets)
- Do NOT add information that isn't in the source notes
- Do NOT use flowery language or filler
- Write in concise, direct markdown

OUTPUT FORMAT:
---
title: [Topic/Theme]
content_type: knowledge
confidence: [0.0-1.0 based on consistency of sources]
sources:
`)

	for _, n := range notes {
		fmt.Fprintf(&b, "  - %s\n", n.Path)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(&b, "consolidated_at: %s\n", now)
	b.WriteString(`---

## [Theme]

### Key Facts
- fact 1
- fact 2

### Decisions
- decision 1 (from: source path)

### Conflicts Detected
- [describe contradiction and resolution]

SOURCE NOTES:
`)

	for _, n := range notes {
		fmt.Fprintf(&b, "\n--- %s (%s) ---\n", n.Path, n.Title)
		b.WriteString(n.Text)
		b.WriteString("\n")
	}

	return b.String()
}

// parseConsolidationOutput extracts the theme, facts, and conflicts from LLM output.
func parseConsolidationOutput(output string) (string, []string, []Conflict) {
	theme := "consolidated"
	var facts []string
	var conflicts []Conflict

	lines := strings.Split(output, "\n")

	inFrontmatter := false
	inFacts := false
	inConflicts := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			inFrontmatter = false
			continue
		}

		if inFrontmatter {
			if strings.HasPrefix(trimmed, "title:") {
				theme = strings.TrimSpace(strings.TrimPrefix(trimmed, "title:"))
				theme = strings.Trim(theme, `"'`)
			}
			continue
		}

		// Track section headers.
		lowerTrimmed := strings.ToLower(trimmed)
		if strings.HasPrefix(lowerTrimmed, "### key facts") || strings.HasPrefix(lowerTrimmed, "### facts") {
			inFacts = true
			inConflicts = false
			continue
		}
		if strings.HasPrefix(lowerTrimmed, "### conflicts") {
			inConflicts = true
			inFacts = false
			continue
		}
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
			inFacts = false
			inConflicts = false
			continue
		}

		// Collect facts.
		if inFacts && strings.HasPrefix(trimmed, "- ") {
			fact := strings.TrimPrefix(trimmed, "- ")
			if fact != "" && fact != "fact 1" && fact != "fact 2" {
				facts = append(facts, fact)
			}
		}

		// Collect conflicts.
		if inConflicts && strings.HasPrefix(trimmed, "- ") {
			desc := strings.TrimPrefix(trimmed, "- ")
			if desc != "" && !strings.Contains(desc, "[describe contradiction") {
				conflicts = append(conflicts, Conflict{
					Fact1:      desc,
					Resolution: "See consolidated note for resolution",
				})
			}
		}
	}

	return theme, facts, conflicts
}

// slugify converts a theme string into a filesystem-safe slug.
func slugify(s string) string {
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
	if len(result) > 80 {
		result = result[:80]
		result = strings.TrimRight(result, "-")
	}
	return result
}
