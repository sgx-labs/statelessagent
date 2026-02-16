// Package indexer walks the vault, parses notes, builds chunks, and indexes them.
package indexer

import (
	"strings"

	"github.com/adrg/frontmatter"
)

// NoteMeta holds parsed frontmatter fields.
type NoteMeta struct {
	Title       string   `yaml:"title"`
	Tags        []string `yaml:"tags"`
	Domain      string   `yaml:"domain"`
	Workstream  string   `yaml:"workstream"`
	Agent       string   `yaml:"agent"`
	ContentType string   `yaml:"content_type"`
	ReviewBy    string   `yaml:"review_by"`
	ReviewByAlt string   `yaml:"review-by"` // alternate key
}

// ParsedNote holds the parsed content of a markdown note.
type ParsedNote struct {
	Meta NoteMeta
	Body string
}

// ParseNote parses a markdown file's frontmatter and body.
func ParseNote(content string) ParsedNote {
	var meta NoteMeta
	body, err := frontmatter.Parse(strings.NewReader(content), &meta)
	if err != nil {
		// If frontmatter parsing fails, treat entire content as body
		return ParsedNote{Body: content}
	}

	// Use alternate review-by key if primary is empty
	if meta.ReviewBy == "" && meta.ReviewByAlt != "" {
		meta.ReviewBy = meta.ReviewByAlt
	}

	return ParsedNote{
		Meta: meta,
		Body: string(body),
	}
}
