package graph

import (
	"fmt"
	"path"
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

	// URL-like external references that should never become local file/note nodes.
	reDomainLikePrefix = regexp.MustCompile(`^[a-z0-9.-]+\.(?:com|io|org|ai)(?::\d+)?(?:/|$)`)

	// Placeholder/template tokens that should not become graph paths.
	rePlaceholderToken = regexp.MustCompile(`(^|[^a-z0-9])(vault_path|yyyy|mm|dd)([^a-z0-9]|$)`)
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
	if err := e.extractRegex(path, nID, content); err != nil {
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

func (e *Extractor) extractRegex(notePath string, nID int64, content string) error {
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

	for rawPath := range files {
		fPath, ok := normalizeGraphReferencePath(notePath, rawPath)
		if !ok {
			continue
		}

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

func normalizeGraphReferencePath(notePath, candidate string) (string, bool) {
	candidate = strings.TrimSpace(strings.ReplaceAll(candidate, "\\", "/"))
	if candidate == "" {
		return "", false
	}
	if isLikelyURLReference(candidate) {
		return "", false
	}

	// Reject absolute/home-style paths.
	if strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "~/") || strings.HasPrefix(candidate, "//") {
		return "", false
	}
	if len(candidate) >= 3 && ((candidate[0] >= 'A' && candidate[0] <= 'Z') || (candidate[0] >= 'a' && candidate[0] <= 'z')) &&
		candidate[1] == ':' && (candidate[2] == '/' || candidate[2] == '\\') {
		return "", false
	}

	clean := candidate
	if strings.HasPrefix(clean, "./") || strings.HasPrefix(clean, "../") {
		baseDir := path.Dir(strings.TrimSpace(strings.ReplaceAll(notePath, "\\", "/")))
		clean = path.Clean(path.Join(baseDir, clean))
	} else {
		clean = path.Clean(clean)
	}
	if clean == "." || clean == "" {
		return "", false
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	if hasFileComponentBeforeLeaf(clean) {
		return "", false
	}
	if isPlaceholderTemplatePath(clean) {
		return "", false
	}
	if isLikelyURLReference(clean) {
		return "", false
	}
	if isLikelyExternalReference(clean) {
		return "", false
	}

	return strings.TrimPrefix(clean, "./"), true
}

func hasFileComponentBeforeLeaf(ref string) bool {
	parts := strings.Split(ref, "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts[:len(parts)-1] {
		if looksLikeFileComponent(part) {
			return true
		}
	}
	return false
}

func looksLikeFileComponent(part string) bool {
	lower := strings.ToLower(strings.TrimSpace(part))
	if lower == "" {
		return false
	}
	for _, ext := range []string{".go", ".md", ".yaml", ".yml", ".toml", ".json", ".sql", ".sh"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func isPlaceholderTemplatePath(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	if lower == "" {
		return false
	}

	if strings.HasPrefix(lower, "_private/") || strings.Contains(lower, "/_private/") {
		return true
	}
	if strings.HasPrefix(lower, "test_vault/") || strings.Contains(lower, "/test_vault/") {
		return true
	}
	if strings.Contains(lower, "path/to/") {
		return true
	}
	if strings.Contains(lower, "yyyy-mm-dd") {
		return true
	}
	if strings.Contains(lower, "/foo.go") || lower == "foo.go" {
		return true
	}
	return rePlaceholderToken.MatchString(lower)
}

func isLikelyURLReference(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "://") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return true
	}
	return reDomainLikePrefix.MatchString(lower)
}

func isLikelyExternalReference(ref string) bool {
	lower := strings.ToLower(ref)
	if strings.Contains(lower, "/.windsurf/worktrees/") || strings.Contains(lower, "/.git/worktrees/") {
		return true
	}

	top := lower
	if idx := strings.IndexByte(lower, '/'); idx >= 0 {
		top = lower[:idx]
	}
	switch top {
	case "users", "home", "private", "var", "opt", "etc", "root", "volumes", "mnt", "usr", "tmp":
		return true
	}
	return false
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
	content = stripFencedCodeBlocks(content)

	var decisions []string
	for _, match := range reDecision1.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			if dText, ok := normalizeDecisionText(match[0], match[1]); ok {
				decisions = append(decisions, dText)
			}
		}
	}
	for _, match := range reDecision2.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			if dText, ok := normalizeDecisionText(match[0], match[1]); ok {
				decisions = append(decisions, dText)
			}
		}
	}

	seen := make(map[string]struct{}, len(decisions))
	for _, dText := range decisions {
		if _, ok := seen[dText]; ok {
			continue
		}
		seen[dText] = struct{}{}

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

func stripFencedCodeBlocks(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inFence := false

	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

func normalizeDecisionText(fullMatch, extracted string) (string, bool) {
	dText := strings.TrimSpace(extracted)
	dText = strings.Trim(dText, "\"'`")
	dText = strings.TrimSpace(dText)
	dText = strings.Trim(dText, ".,;:()[]{}")
	dText = strings.TrimSpace(dText)
	if len(dText) < 10 {
		return "", false
	}

	lower := strings.ToLower(dText)

	// Reject shell-command fragments.
	if strings.Contains(dText, "&&") || strings.Contains(dText, "|") || strings.Contains(dText, ">") {
		return "", false
	}

	// Reject regex/example fragments that should not become decisions.
	if strings.Contains(dText, "`") || strings.Contains(lower, "...") || strings.Contains(lower, `", "`) || strings.Contains(lower, "`, `") {
		return "", false
	}
	if strings.Contains(lower, "(?:") || strings.Contains(lower, `\s`) || strings.Contains(lower, `\w`) || strings.Contains(lower, `\d`) || strings.Contains(lower, "(?i)") {
		return "", false
	}
	if strings.HasPrefix(lower, "whether ") {
		return "", false
	}
	if strings.Contains(lower, "conversation mode detected") || strings.Contains(lower, "injected or skipped") {
		return "", false
	}
	if !hasDecisionIntent(lower) && !hasDecisionVerb(strings.ToLower(fullMatch)) {
		return "", false
	}

	return dText, true
}

func hasDecisionVerb(text string) bool {
	for _, marker := range []string{
		"decided",
		"chose",
		"chosen",
		"going with",
		"plan is to",
		"shipped",
		"picked",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func hasDecisionIntent(text string) bool {
	if text == "" {
		return false
	}
	for _, prefix := range []string{
		"use ",
		"keep ",
		"adopt ",
		"split ",
		"pick ",
		"picked ",
		"go with ",
		"going with ",
		"plan is to ",
		"ship ",
		"shipped ",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	if strings.HasPrefix(text, "use ") && strings.Contains(text, " for ") {
		return true
	}
	return false
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
