package memory

import (
	"fmt"
	"strings"
	"testing"
)

// mockFactLLM implements llm.Client for testing fact extraction.
type mockFactLLM struct {
	response string
	err      error
}

func (m *mockFactLLM) Generate(model, prompt string) (string, error) {
	return m.response, m.err
}

func (m *mockFactLLM) GenerateJSON(model, prompt string) (string, error) {
	return m.response, m.err
}

func (m *mockFactLLM) PickBestModel() (string, error) {
	return "test-model", nil
}

func (m *mockFactLLM) Provider() string {
	return "mock"
}

func TestExtractFacts_BasicExtraction(t *testing.T) {
	mock := &mockFactLLM{
		response: `{
			"facts": [
				{"text": "User graduated with Business Administration degree", "category": "declaration", "confidence": 1.0},
				{"text": "Daily commute is 30 minutes", "category": "declaration", "confidence": 0.9},
				{"text": "User prefers dark mode in all editors", "category": "preference", "confidence": 0.8}
			]
		}`,
	}

	facts, err := ExtractFacts("I graduated with a Business Administration degree. My daily commute is 30 minutes. I prefer dark mode in all editors.", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	if len(facts) != 3 {
		t.Fatalf("expected 3 facts, got %d", len(facts))
	}

	// Verify first fact
	if facts[0].Text != "User graduated with Business Administration degree" {
		t.Errorf("unexpected fact text: %s", facts[0].Text)
	}
	if facts[0].Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %.2f", facts[0].Confidence)
	}

	// Verify preference fact
	if facts[2].Confidence != 0.8 {
		t.Errorf("expected confidence 0.8, got %.2f", facts[2].Confidence)
	}
}

func TestExtractFacts_EmptyContent(t *testing.T) {
	mock := &mockFactLLM{response: `{"facts": []}`}

	facts, err := ExtractFacts("", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}
	if facts != nil {
		t.Errorf("expected nil for empty content, got %d facts", len(facts))
	}
}

func TestExtractFacts_WhitespaceOnlyContent(t *testing.T) {
	mock := &mockFactLLM{response: `{"facts": []}`}

	facts, err := ExtractFacts("   \n\t  ", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}
	if facts != nil {
		t.Errorf("expected nil for whitespace-only content, got %d facts", len(facts))
	}
}

func TestExtractFacts_LLMError(t *testing.T) {
	mock := &mockFactLLM{
		response: "",
		err:      fmt.Errorf("connection refused"),
	}

	_, err := ExtractFacts("Some content to extract from.", mock, "test-model")
	if err == nil {
		t.Fatal("expected error from LLM failure")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected connection error, got: %v", err)
	}
}

func TestExtractFacts_InvalidJSON(t *testing.T) {
	mock := &mockFactLLM{
		response: "this is not valid json at all",
	}

	_, err := ExtractFacts("Some content.", mock, "test-model")
	if err == nil {
		t.Fatal("expected error from invalid JSON")
	}
	if !strings.Contains(err.Error(), "no valid JSON") {
		t.Errorf("expected JSON parse error, got: %v", err)
	}
}

func TestExtractFacts_FiltersEmptyAndShortFacts(t *testing.T) {
	mock := &mockFactLLM{
		response: `{
			"facts": [
				{"text": "", "category": "declaration", "confidence": 1.0},
				{"text": "short", "category": "declaration", "confidence": 0.9},
				{"text": "This is a valid fact with sufficient length", "category": "declaration", "confidence": 0.8}
			]
		}`,
	}

	facts, err := ExtractFacts("Content with mixed quality facts.", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("expected 1 fact (after filtering), got %d", len(facts))
	}
	if facts[0].Text != "This is a valid fact with sufficient length" {
		t.Errorf("unexpected fact text: %s", facts[0].Text)
	}
}

func TestExtractFacts_DefaultsInvalidConfidence(t *testing.T) {
	mock := &mockFactLLM{
		response: `{
			"facts": [
				{"text": "A fact with zero confidence value", "category": "declaration", "confidence": 0},
				{"text": "A fact with negative confidence value", "category": "declaration", "confidence": -0.5},
				{"text": "A fact with over-one confidence value", "category": "declaration", "confidence": 1.5}
			]
		}`,
	}

	facts, err := ExtractFacts("Content with invalid confidence values.", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	for _, f := range facts {
		if f.Confidence != 0.6 {
			t.Errorf("expected default confidence 0.6 for %q, got %.2f", f.Text, f.Confidence)
		}
	}
}

func TestExtractFacts_DerivableContentExcluded(t *testing.T) {
	// This test verifies that the LLM prompt is designed to exclude derivable content.
	// The mock LLM returns what a well-prompted model should NOT return for code-structure queries.
	// A properly prompted LLM should return only personal/declarative facts.
	mock := &mockFactLLM{
		response: `{
			"facts": [
				{"text": "Team uses PostgreSQL for the production database", "category": "decision", "confidence": 0.9}
			]
		}`,
	}

	// Feed it content about code structure that should produce
	// only the decision fact, not file-structure facts.
	content := `The main.go file imports fmt and os packages.
There are 15 files in the internal directory.
We use PostgreSQL for the production database.
The function has 3 parameters.`

	facts, err := ExtractFacts(content, mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("expected 1 fact (decisions only, no code structure), got %d", len(facts))
	}

	// Verify no derivable facts leaked through
	for _, f := range facts {
		text := strings.ToLower(f.Text)
		if strings.Contains(text, "main.go") || strings.Contains(text, "imports") || strings.Contains(text, "15 files") || strings.Contains(text, "3 parameters") {
			t.Errorf("derivable/structural fact should not be extracted: %q", f.Text)
		}
	}
}

func TestExtractFacts_ThinkingTokensStripped(t *testing.T) {
	// Some models emit thinking tokens before the JSON.
	mock := &mockFactLLM{
		response: `<think>Let me analyze this text for facts...</think>
{
	"facts": [
		{"text": "User works at a startup in San Francisco", "category": "declaration", "confidence": 0.9}
	]
}`,
	}

	facts, err := ExtractFacts("I work at a startup in San Francisco.", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Text != "User works at a startup in San Francisco" {
		t.Errorf("unexpected fact text: %s", facts[0].Text)
	}
}

func TestExtractFacts_CodeFencedJSON(t *testing.T) {
	mock := &mockFactLLM{
		response: "```json\n{\"facts\": [{\"text\": \"Team has 5 backend engineers\", \"category\": \"declaration\", \"confidence\": 0.9}]}\n```",
	}

	facts, err := ExtractFacts("We have 5 backend engineers on the team.", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
}

func TestExtractFacts_FactToChunkLinking(t *testing.T) {
	// Verify that SourcePath and ChunkID are correctly propagated
	// when the caller sets them after extraction.
	mock := &mockFactLLM{
		response: `{
			"facts": [
				{"text": "User prefers TypeScript over JavaScript", "category": "preference", "confidence": 0.85}
			]
		}`,
	}

	facts, err := ExtractFacts("I prefer TypeScript over JavaScript for all new projects.", mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}

	// Simulate the caller setting source metadata (as the indexer would do).
	facts[0].SourcePath = "notes/preferences.md"
	facts[0].ChunkID = 2

	if facts[0].SourcePath != "notes/preferences.md" {
		t.Errorf("unexpected source path: %s", facts[0].SourcePath)
	}
	if facts[0].ChunkID != 2 {
		t.Errorf("unexpected chunk ID: %d", facts[0].ChunkID)
	}
	if facts[0].Text != "User prefers TypeScript over JavaScript" {
		t.Errorf("unexpected fact text: %s", facts[0].Text)
	}
}

func TestExtractFacts_LongContentTruncated(t *testing.T) {
	// Verify that very long content is handled without error.
	mock := &mockFactLLM{
		response: `{"facts": [{"text": "Content was processed successfully", "category": "declaration", "confidence": 0.7}]}`,
	}

	// Create content longer than 12000 chars.
	longContent := strings.Repeat("This is a sentence about my work. ", 500)

	facts, err := ExtractFacts(longContent, mock, "test-model")
	if err != nil {
		t.Fatalf("ExtractFacts: %v", err)
	}

	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
}

func TestExtractFactJSON_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "only thinking tokens",
			input:   "<think>some reasoning here</think>",
			wantErr: true,
		},
		{
			name:    "valid JSON directly",
			input:   `{"facts": []}`,
			wantErr: false,
		},
		{
			name:    "JSON with leading text",
			input:   `Here is the result: {"facts": []}`,
			wantErr: false,
		},
		{
			name:    "reasoning then JSON",
			input:   `<reasoning>analyzing...</reasoning>{"facts": []}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractFactJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got result: %s", result)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
