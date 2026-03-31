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

type turnMarker struct {
	start int
	end   int
	role  string
}

// ShouldChunkByTurns returns true if the body contains enough conversational
// turn markers (User:/Assistant:/Human:/AI:) to warrant turn-level chunking.
func ShouldChunkByTurns(body string) bool {
	markers := turnMarkers(body)
	if len(markers) < 4 {
		return false
	}
	return alternatingPairCount(markers) >= 2
}

// ChunkByTurns splits conversational content by User/Assistant turn pairs.
// Each chunk contains a user message paired with its assistant response,
// making individual conversational facts independently searchable.
func ChunkByTurns(body string) []Chunk {
	markers := turnMarkers(body)
	if len(markers) == 0 {
		return []Chunk{{Heading: "(full)", Text: body}}
	}

	// Split body into segments at each turn marker.
	type segment struct {
		marker string // e.g. "User", "Assistant"
		text   string // full text including marker line
	}

	var segments []segment

	// Text before the first turn marker (preamble).
	if markers[0].start > 0 {
		preamble := strings.TrimSpace(body[:markers[0].start])
		if preamble != "" {
			segments = append(segments, segment{marker: "_preamble", text: preamble})
		}
	}

	for i, marker := range markers {
		end := len(body)
		if i+1 < len(markers) {
			end = markers[i+1].start
		}
		segText := strings.TrimSpace(body[marker.start:end])
		if segText != "" {
			segments = append(segments, segment{marker: marker.role, text: segText})
		}
	}

	// Group into user+assistant pairs. A "user" role starts a new pair;
	// subsequent non-user segments (assistant/AI responses) attach to it.
	var chunks []Chunk
	var currentPair strings.Builder
	var currentHeading string
	turnNum := 0

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
		} else if isAssistantRole(seg.marker) {
			// Assistant/AI response — append to current pair.
			if currentPair.Len() == 0 {
				continue
			}
			currentPair.WriteString("\n\n")
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

func turnMarkers(body string) []turnMarker {
	masked := maskFencedCodeBlocks(body)
	locs := turnPattern.FindAllStringIndex(masked, -1)
	markers := make([]turnMarker, 0, len(locs))
	for _, loc := range locs {
		markers = append(markers, turnMarker{
			start: loc[0],
			end:   loc[1],
			role:  markerRole(body[loc[0]:loc[1]]),
		})
	}
	return markers
}

func alternatingPairCount(markers []turnMarker) int {
	pairs := 0
	expectingUser := true
	for _, marker := range markers {
		switch {
		case expectingUser && isUserRole(marker.role):
			expectingUser = false
		case !expectingUser && isAssistantRole(marker.role):
			pairs++
			expectingUser = true
		case !expectingUser && isUserRole(marker.role):
			// Consecutive user turns restart the pending pair.
			expectingUser = false
		}
	}
	return pairs
}

func markerRole(markerText string) string {
	return strings.Trim(strings.TrimRight(markerText, ": \t"), "*")
}

func isUserRole(role string) bool {
	r := strings.ToLower(role)
	return r == "user" || r == "human"
}

func isAssistantRole(role string) bool {
	r := strings.ToLower(role)
	return r == "assistant" || r == "ai"
}

func maskFencedCodeBlocks(body string) string {
	if body == "" {
		return body
	}

	masked := []byte(body)
	inFence := false
	fenceMarker := ""

	for lineStart := 0; lineStart < len(body); {
		lineEnd := lineStart
		for lineEnd < len(body) && body[lineEnd] != '\n' {
			lineEnd++
		}
		if lineEnd < len(body) {
			lineEnd++
		}

		line := body[lineStart:lineEnd]
		trimmed := strings.TrimLeft(line, " \t")
		lineFence := ""
		switch {
		case strings.HasPrefix(trimmed, "```"):
			lineFence = "```"
		case strings.HasPrefix(trimmed, "~~~"):
			lineFence = "~~~"
		}

		maskLine := inFence
		if !inFence && lineFence != "" {
			inFence = true
			fenceMarker = lineFence
			maskLine = true
		} else if inFence {
			maskLine = true
			if lineFence != "" && strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
			}
		}

		if maskLine {
			for i := lineStart; i < lineEnd; i++ {
				if masked[i] != '\n' {
					masked[i] = ' '
				}
			}
		}

		lineStart = lineEnd
	}

	return string(masked)
}
