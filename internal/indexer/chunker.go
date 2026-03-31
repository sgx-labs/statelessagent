package indexer

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/config"
)

// Chunk represents a portion of a note for embedding.
type Chunk struct {
	Heading string
	Text    string
}

var (
	h2Split = regexp.MustCompile(`(?m)^## `)
	h3Split = regexp.MustCompile(`(?m)^### `)

	// turnPattern matches conversational turn markers like **User:**, **Assistant:**,
	// User:, Assistant:, Human:, AI: at the start of a line.
	turnPattern = regexp.MustCompile(`(?m)^\*{0,2}(?:User|Assistant|Human|AI)\*{0,2}\s*:`)
)

// ShouldChunkByTurns returns true if the body contains enough conversational
// turn markers (User:/Assistant:/Human:/AI:) to warrant turn-level chunking.
func ShouldChunkByTurns(body string) bool {
	return len(turnPattern.FindAllStringIndex(body, 4)) >= 3
}

// ChunkByTurns splits conversational content by User/Assistant turn pairs.
// Each chunk contains a user message paired with its assistant response,
// making individual conversational facts independently searchable.
func ChunkByTurns(body string) []Chunk {
	locs := turnPattern.FindAllStringIndex(body, -1)
	if len(locs) == 0 {
		return []Chunk{{Heading: "(full)", Text: body}}
	}

	// Split body into segments at each turn marker.
	type segment struct {
		marker string // e.g. "User", "Assistant"
		text   string // full text including marker line
	}

	var segments []segment

	// Text before the first turn marker (preamble).
	if locs[0][0] > 0 {
		preamble := strings.TrimSpace(body[:locs[0][0]])
		if preamble != "" {
			segments = append(segments, segment{marker: "_preamble", text: preamble})
		}
	}

	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		markerText := body[loc[0]:loc[1]]
		// Extract the role name from the marker (e.g. "User" from "**User:**")
		role := strings.Trim(strings.TrimRight(markerText, ": \t"), "*")
		segText := strings.TrimSpace(body[loc[0]:end])
		if segText != "" {
			segments = append(segments, segment{marker: role, text: segText})
		}
	}

	// Group into user+assistant pairs. A "user" role starts a new pair;
	// subsequent non-user segments (assistant/AI responses) attach to it.
	var chunks []Chunk
	var currentPair strings.Builder
	var currentHeading string
	turnNum := 0

	isUserRole := func(role string) bool {
		r := strings.ToLower(role)
		return r == "user" || r == "human"
	}

	flush := func() {
		if currentPair.Len() > 0 {
			heading := currentHeading
			if heading == "" {
				turnNum++
				heading = fmt.Sprintf("(turn %d)", turnNum)
			}
			chunks = append(chunks, Chunk{
				Heading: heading,
				Text:    strings.TrimSpace(currentPair.String()),
			})
			currentPair.Reset()
			currentHeading = ""
		}
	}

	for _, seg := range segments {
		if seg.marker == "_preamble" {
			// Preamble becomes its own chunk.
			chunks = append(chunks, Chunk{Heading: "(preamble)", Text: seg.text})
			continue
		}

		if isUserRole(seg.marker) {
			// Start of a new turn pair — flush previous.
			flush()
			currentPair.WriteString(seg.text)
			// Extract a short heading from the user message (first line after marker).
			lines := strings.SplitN(seg.text, "\n", 3)
			if len(lines) >= 2 {
				h := strings.TrimSpace(lines[1])
				if len(h) > 80 {
					h = h[:80]
				}
				currentHeading = h
			} else {
				// Single-line user message: use the text after the colon
				afterColon := turnPattern.ReplaceAllString(seg.text, "")
				h := strings.TrimSpace(afterColon)
				if len(h) > 80 {
					h = h[:80]
				}
				currentHeading = h
			}
		} else {
			// Assistant/AI response — append to current pair.
			if currentPair.Len() > 0 {
				currentPair.WriteString("\n\n")
			}
			currentPair.WriteString(seg.text)
		}
	}
	flush()

	if len(chunks) == 0 {
		return []Chunk{{Heading: "(full)", Text: body}}
	}
	return chunks
}

// ChunkByHeadings splits note body by H2 headings, with H3 sub-splitting for large sections.
func ChunkByHeadings(body string) []Chunk {
	parts := h2Split.Split(body, -1)
	var chunks []Chunk

	// First part is intro (before first H2)
	if strings.TrimSpace(parts[0]) != "" {
		chunks = append(chunks, Chunk{Heading: "(intro)", Text: strings.TrimSpace(parts[0])})
	}

	// Find H2 headings for labeling
	headingLocs := h2Split.FindAllStringIndex(body, -1)
	for i, part := range parts[1:] {
		_ = headingLocs // suppress warning
		lines := strings.SplitN(part, "\n", 2)
		heading := strings.TrimSpace(lines[0])
		text := ""
		if len(lines) > 1 {
			text = strings.TrimSpace(lines[1])
		}
		if text == "" {
			continue
		}

		fullText := "## " + heading + "\n" + text

		// If H2 section is too large, try splitting by H3
		if len(fullText) > config.MaxEmbedChars {
			h3Parts := h3Split.Split(fullText, -1)
			if len(h3Parts) > 1 {
				if strings.TrimSpace(h3Parts[0]) != "" {
					chunks = append(chunks, Chunk{
						Heading: heading,
						Text:    strings.TrimSpace(h3Parts[0]),
					})
				}
				for _, h3Part := range h3Parts[1:] {
					h3Lines := strings.SplitN(h3Part, "\n", 2)
					h3Heading := strings.TrimSpace(h3Lines[0])
					h3Text := ""
					if len(h3Lines) > 1 {
						h3Text = strings.TrimSpace(h3Lines[1])
					}
					if h3Text != "" {
						chunks = append(chunks, Chunk{
							Heading: heading + " > " + h3Heading,
							Text:    "### " + h3Heading + "\n" + h3Text,
						})
					}
				}
			} else {
				chunks = append(chunks, Chunk{Heading: heading, Text: fullText})
			}
		} else {
			chunks = append(chunks, Chunk{Heading: heading, Text: fullText})
		}

		_ = i
	}

	if len(chunks) == 0 {
		return []Chunk{{Heading: "(full)", Text: body}}
	}
	return chunks
}

// ChunkBySize splits text into chunks at paragraph boundaries.
func ChunkBySize(text string, maxChars int) []Chunk {
	if maxChars <= 0 {
		maxChars = config.MaxEmbedChars
	}
	paragraphs := strings.Split(text, "\n\n")
	var chunks []Chunk
	var current strings.Builder

	for _, para := range paragraphs {
		if current.Len()+len(para)+2 > maxChars && current.Len() > 0 {
			chunks = append(chunks, Chunk{
				Heading: partHeading(len(chunks) + 1),
				Text:    strings.TrimSpace(current.String()),
			})
			current.Reset()
			current.WriteString(para)
		} else {
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(para)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		chunks = append(chunks, Chunk{
			Heading: partHeading(len(chunks) + 1),
			Text:    strings.TrimSpace(current.String()),
		})
	}
	return chunks
}

func partHeading(n int) string {
	return fmt.Sprintf("(part %d)", n)
}
