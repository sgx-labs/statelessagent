package graph

import (
	"fmt"
	"regexp"
	"strings"
)

// Regex patterns
var (
	// Go files: internal/pkg/file.go
	reGoFile = regexp.MustCompile(`\b((?:internal|cmd|pkg)/[\w/]+\.go)\b`)

	// Generic paths: path/to/file.ext
	reGenericFile = regexp.MustCompile(`\b([\w][\w.-]*/[\w.-]+(?:/[\w.-]+)*\.(?:go|md|yaml|yml|toml|json|sql|sh))\b`)

	// Decision patterns
	reDecision1 = regexp.MustCompile(`(?i)(?:decided|decision|chose|chosen):\s*(.+)`)
	reDecision2 = regexp.MustCompile(`(?i)we (?:decided|chose) to\s+(.+?)(?:\.|$)`)
)

type Extractor struct {
	db  *DB
	llm *LLMExtractor
}

func NewExtractor(db *DB) *Extractor {
	return &Extractor{db: db}
}

// SetLLM enables LLM-based extraction using the provided client and model.
func (e *Extractor) SetLLM(client LLMClient, model string) {
	e.llm = NewLLMExtractor(client, model)
}

// ExtractFromNote processes a single note and creates graph nodes/edges.
func (e *Extractor) ExtractFromNote(noteID int64, path, content, agent string) error {
	// 1. Ensure note node exists
	noteNode := &Node{
		Type:   NodeNote,
		Name:   path,
		NoteID: &noteID,
	}
	nID, err := e.db.UpsertNode(noteNode)
	if err != nil {
		return fmt.Errorf("upsert note node: %w", err)
	}

	// 2. Extract file references (Regex)
	if err := e.extractRegex(nID, content); err != nil {
		return err
	}

	// 3. Agent
	if agent != "" {
		if err := e.linkAgent(nID, agent); err != nil {
			return err
		}
	}

	// 4. Decisions (Regex)
	if err := e.extractDecisionsRegex(nID, content); err != nil {
		return err
	}

	// 5. LLM Extraction (if enabled)
	if e.llm != nil {
		if err := e.extractLLM(nID, content); err != nil {
			// Log error but don't fail the whole extraction?
			// For now, let's return error so caller knows.
			return fmt.Errorf("llm extraction: %w", err)
		}
	}

	return nil
}

func (e *Extractor) extractRegex(nID int64, content string) error {
	files := make(map[string]bool)

	// Find all Go files
	for _, match := range reGoFile.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			files[match[1]] = true
		}
	}

	// Find generic files
	for _, match := range reGenericFile.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			files[match[1]] = true
		}
	}

	for fPath := range files {
		nodeType := NodeFile
		if strings.HasSuffix(strings.ToLower(fPath), ".md") {
			// Markdown links represent note-to-note knowledge paths.
			nodeType = NodeNote
		}

		// Create file node
		fNode := &Node{
			Type: nodeType,
			Name: fPath,
		}
		fID, err := e.db.UpsertNode(fNode)
		if err != nil {
			return fmt.Errorf("upsert file node: %w", err)
		}

		// Create "references" edge: Note -> File
		edge := &Edge{
			SourceID:     nID,
			TargetID:     fID,
			Relationship: RelReferences,
		}
		if _, err := e.db.UpsertEdge(edge); err != nil {
			return fmt.Errorf("upsert file edge: %w", err)
		}
	}
	return nil
}

func (e *Extractor) linkAgent(nID int64, agent string) error {
	aNode := &Node{
		Type: NodeAgent,
		Name: agent,
	}
	aID, err := e.db.UpsertNode(aNode)
	if err != nil {
		return fmt.Errorf("upsert agent node: %w", err)
	}

	// Agent -> Note (produced)
	edge := &Edge{
		SourceID:     aID,
		TargetID:     nID,
		Relationship: RelProduced,
	}
	if _, err := e.db.UpsertEdge(edge); err != nil {
		return fmt.Errorf("upsert agent edge: %w", err)
	}
	return nil
}

func (e *Extractor) extractDecisionsRegex(nID int64, content string) error {
	var decisions []string
	for _, match := range reDecision1.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			decisions = append(decisions, strings.TrimSpace(match[1]))
		}
	}
	for _, match := range reDecision2.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			decisions = append(decisions, strings.TrimSpace(match[1]))
		}
	}

	for _, dText := range decisions {
		if len(dText) > 200 {
			dText = dText[:200] + "..."
		}

		dNode := &Node{
			Type: NodeDecision,
			Name: dText,
		}
		dID, err := e.db.UpsertNode(dNode)
		if err != nil {
			return fmt.Errorf("upsert decision node: %w", err)
		}

		edge := &Edge{
			SourceID:     dID,
			TargetID:     nID,
			Relationship: RelAffects,
		}
		if _, err := e.db.UpsertEdge(edge); err != nil {
			return fmt.Errorf("upsert decision edge: %w", err)
		}
	}
	return nil
}

func (e *Extractor) extractLLM(nID int64, content string) error {
	resp, err := e.llm.Extract(content)
	if err != nil {
		return err
	}

	nodeMap := make(map[string]int64)

	// Upsert Nodes
	for _, n := range resp.Nodes {
		node := &Node{
			Type: n.Type,
			Name: n.Name,
		}
		id, err := e.db.UpsertNode(node)
		if err != nil {
			return fmt.Errorf("upsert llm node %s: %w", n.Name, err)
		}
		nodeMap[n.Name] = id
	}

	// Upsert Edges
	for _, edge := range resp.Edges {
		srcID, ok1 := nodeMap[edge.Source]
		tgtID, ok2 := nodeMap[edge.Target]

		// If nodes not found in explicit list, try to find them in DB or create implicit ones?
		// For now, only link if both exist from this extraction or we look them up.
		// Let's try to look them up if missing.
		if !ok1 {
			// Try find/create
			// We don't know type easily if it wasn't in nodes list. Default to "entity"?
			// Or skip.
			// Let's skip for safety to avoid garbage.
			continue
		}
		if !ok2 {
			continue
		}

		dbEdge := &Edge{
			SourceID:     srcID,
			TargetID:     tgtID,
			Relationship: edge.Relation,
		}
		if _, err := e.db.UpsertEdge(dbEdge); err != nil {
			return fmt.Errorf("upsert llm edge: %w", err)
		}
	}

	// Also link extracted nodes to the Note?
	// E.g. entities mentioned in the note are "mentions"
	for _, id := range nodeMap {
		// Avoid linking if it's the note itself (unlikely)
		// Check if edge already exists? UpsertEdge handles conflict.
		// Link Note -> Entity (mentions)
		edge := &Edge{
			SourceID:     nID,
			TargetID:     id,
			Relationship: RelMentions,
		}
		if _, err := e.db.UpsertEdge(edge); err != nil {
			return fmt.Errorf("upsert mention edge: %w", err)
		}
	}

	return nil
}
