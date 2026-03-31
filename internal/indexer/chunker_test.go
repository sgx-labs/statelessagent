package indexer

import (
	"strings"
	"testing"
)

// --- ShouldChunkByTurns tests ---

func TestShouldChunkByTurns_Conversational(t *testing.T) {
	body := "**User:** What is your favorite color?\n\n**Assistant:** I like blue.\n\n**User:** Why blue?\n\n**Assistant:** It reminds me of the sky.\n"
	if !ShouldChunkByTurns(body) {
		t.Error("expected ShouldChunkByTurns to return true for conversational content")
	}
}

func TestShouldChunkByTurns_NonConversational(t *testing.T) {
	body := "## Overview\n\nThis is a normal document.\n\n## Design\n\nSome design notes.\n"
	if ShouldChunkByTurns(body) {
		t.Error("expected ShouldChunkByTurns to return false for non-conversational content")
	}
}

func TestShouldChunkByTurns_TooFewTurns(t *testing.T) {
	body := "**User:** Hello\n\n**Assistant:** Hi\n"
	if ShouldChunkByTurns(body) {
		t.Error("expected ShouldChunkByTurns to return false for only 2 turns")
	}
}

func TestShouldChunkByTurns_PlainColonFormat(t *testing.T) {
	body := "User: What is the capital of France?\nAssistant: Paris.\nUser: And Germany?\nAssistant: Berlin.\n"
	if !ShouldChunkByTurns(body) {
		t.Error("expected ShouldChunkByTurns to return true for plain User:/Assistant: format")
	}
}

func TestShouldChunkByTurns_HumanAIFormat(t *testing.T) {
	body := "Human: Tell me about Go\nAI: Go is a programming language.\nHuman: What about Rust?\nAI: Rust is a systems language.\n"
	if !ShouldChunkByTurns(body) {
		t.Error("expected ShouldChunkByTurns to return true for Human:/AI: format")
	}
}

// --- ChunkByTurns tests ---

func TestChunkByTurns_BasicConversation(t *testing.T) {
	body := "**User:** What is your favorite color?\n\n**Assistant:** I like blue.\n\n**User:** Why blue?\n\n**Assistant:** It reminds me of the sky.\n"
	chunks := ChunkByTurns(body)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks (one per turn pair), got %d", len(chunks))
	}

	// First chunk should contain the first user+assistant pair
	if !strings.Contains(chunks[0].Text, "favorite color") {
		t.Error("first chunk should contain 'favorite color'")
	}
	if !strings.Contains(chunks[0].Text, "I like blue") {
		t.Error("first chunk should contain the assistant response 'I like blue'")
	}

	// Second chunk should contain the second pair
	if !strings.Contains(chunks[1].Text, "Why blue") {
		t.Error("second chunk should contain 'Why blue'")
	}
	if !strings.Contains(chunks[1].Text, "reminds me of the sky") {
		t.Error("second chunk should contain 'reminds me of the sky'")
	}
}

func TestChunkByTurns_WithPreamble(t *testing.T) {
	body := "Session from 2026-03-15\nTopic: Life goals\n\n**User:** What should I study?\n\n**Assistant:** Consider your interests.\n\n**User:** I like math.\n\n**Assistant:** Try engineering.\n"
	chunks := ChunkByTurns(body)

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks (preamble + 2 turn pairs), got %d", len(chunks))
	}

	// First chunk should be the preamble
	if chunks[0].Heading != "(preamble)" {
		t.Errorf("expected preamble heading, got %q", chunks[0].Heading)
	}
	if !strings.Contains(chunks[0].Text, "Session from") {
		t.Error("preamble should contain session info")
	}
}

func TestChunkByTurns_SingleTurn(t *testing.T) {
	// With only one user message and one assistant response (2 markers),
	// ShouldChunkByTurns would return false so this tests the edge case
	// where ChunkByTurns is called with minimal input.
	body := "**User:** Hello\n\n**Assistant:** Hi there\n"
	chunks := ChunkByTurns(body)

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for single turn pair, got %d", len(chunks))
	}
}

func TestChunkByTurns_PlainFormat(t *testing.T) {
	body := "User: First question\nAssistant: First answer\nUser: Second question\nAssistant: Second answer\n"
	chunks := ChunkByTurns(body)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks for plain format, got %d", len(chunks))
	}

	if !strings.Contains(chunks[0].Text, "First question") {
		t.Error("first chunk should contain 'First question'")
	}
	if !strings.Contains(chunks[0].Text, "First answer") {
		t.Error("first chunk should contain 'First answer'")
	}
}

func TestChunkByTurns_MixedContent_UsesMoreChunks(t *testing.T) {
	// Content with both headings and turns — the caller should pick
	// whichever produces more chunks. This tests the chunk output.
	body := "## Session\n\n**User:** Question one\n\n**Assistant:** Answer one\n\n**User:** Question two\n\n**Assistant:** Answer two\n\n**User:** Question three\n\n**Assistant:** Answer three\n"

	turnChunks := ChunkByTurns(body)
	headingChunks := ChunkByHeadings(body)

	// Turn chunking should produce more granular results for conversations
	if len(turnChunks) < len(headingChunks) {
		t.Errorf("expected turn chunks (%d) >= heading chunks (%d) for conversational content",
			len(turnChunks), len(headingChunks))
	}
}

func TestChunkByTurns_EachChunkSearchable(t *testing.T) {
	body := "**User:** What degree did I study?\n\n**Assistant:** You studied Business Administration at UCLA.\n\n**User:** What is my cat's name?\n\n**Assistant:** Your cat is named Whiskers.\n\n**User:** What is my favorite restaurant?\n\n**Assistant:** You love Sushi Palace on Main Street.\n"

	chunks := ChunkByTurns(body)

	// Each fact should be in a separate chunk so it's independently searchable
	foundBusiness := false
	foundCat := false
	foundRestaurant := false
	for _, c := range chunks {
		if strings.Contains(c.Text, "Business Administration") {
			foundBusiness = true
		}
		if strings.Contains(c.Text, "Whiskers") {
			foundCat = true
		}
		if strings.Contains(c.Text, "Sushi Palace") {
			foundRestaurant = true
		}
	}

	if !foundBusiness {
		t.Error("expected a chunk containing 'Business Administration'")
	}
	if !foundCat {
		t.Error("expected a chunk containing 'Whiskers'")
	}
	if !foundRestaurant {
		t.Error("expected a chunk containing 'Sushi Palace'")
	}

	// No single chunk should contain all three facts
	for _, c := range chunks {
		has := 0
		if strings.Contains(c.Text, "Business Administration") {
			has++
		}
		if strings.Contains(c.Text, "Whiskers") {
			has++
		}
		if strings.Contains(c.Text, "Sushi Palace") {
			has++
		}
		if has > 1 {
			t.Errorf("chunk should not contain multiple unrelated facts, but contains %d", has)
		}
	}
}

func TestChunkByTurns_HeadingsProvided(t *testing.T) {
	body := "**User:** What is the weather like?\nI want to know about tomorrow.\n\n**Assistant:** It will be sunny tomorrow.\n\n**User:** Should I bring an umbrella?\n\n**Assistant:** No need.\n"

	chunks := ChunkByTurns(body)

	// Each chunk should have a non-empty heading
	for i, c := range chunks {
		if c.Heading == "" {
			t.Errorf("chunk %d has empty heading", i)
		}
	}
}

func TestChunkByTurns_EmptyBody(t *testing.T) {
	chunks := ChunkByTurns("")
	if len(chunks) != 1 {
		t.Fatalf("expected 1 fallback chunk for empty body, got %d", len(chunks))
	}
	if chunks[0].Heading != "(full)" {
		t.Errorf("expected (full) heading for empty body, got %q", chunks[0].Heading)
	}
}

// --- Existing ChunkByHeadings and ChunkBySize tests (regression) ---

func TestChunkByHeadingsBasic(t *testing.T) {
	body := "## Overview\n\nOverview content.\n\n## Design\n\nDesign content.\n"
	chunks := ChunkByHeadings(body)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
}

func TestChunkBySizeBasic(t *testing.T) {
	text := "Paragraph one.\n\nParagraph two.\n\nParagraph three."
	chunks := ChunkBySize(text, 30)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestChunkByHeadingsNoContent(t *testing.T) {
	body := "Just plain text without headings."
	chunks := ChunkByHeadings(body)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Heading != "(intro)" {
		t.Errorf("expected (intro) heading, got %q", chunks[0].Heading)
	}
}
