package graph

import (
	"encoding/json"
	"fmt"
	"testing"
)

// MockLLMClient for testing
type MockLLMClient struct {
	Response string
	Err      error
}

func (m *MockLLMClient) GenerateJSON(model, prompt string) (string, error) {
	return m.Response, m.Err
}

func TestLLMExtraction(t *testing.T) {
	db := setupTestDB(t)

	// Mock successful response
	mockJSON := `{
		"nodes": [
			{"type": "entity", "name": "Redis"},
			{"type": "concept", "name": "CachingLayer"}
		],
		"edges": [
			{"source": "CachingLayer", "target": "Redis", "relation": "uses"}
		]
	}`

	client := &MockLLMClient{Response: mockJSON}
	ext := NewExtractor(db)
	ext.SetLLM(client, "test-model")

	err := ext.ExtractFromNote(1, "test.md", "We use Redis for caching.", "AgentX")
	if err != nil {
		t.Fatalf("ExtractFromNote: %v", err)
	}

	// Verify Nodes
	redis, err := db.FindNode("entity", "Redis")
	if err != nil {
		t.Errorf("Redis node not found: %v", err)
	}
	if redis.Name != "Redis" {
		t.Errorf("expected Redis, got %s", redis.Name)
	}

	cache, err := db.FindNode("concept", "CachingLayer")
	if err != nil {
		t.Errorf("CachingLayer node not found: %v", err)
	}

	// Verify Edge: CachingLayer -> Redis
	edges, err := db.GetNeighbors(cache.ID, "uses", "forward")
	if err != nil {
		t.Errorf("GetNeighbors: %v", err)
	}
	found := false
	for _, n := range edges {
		if n.ID == redis.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Edge CachingLayer -> Redis not found")
	}

	// Verify Mention Edge: Note -> Redis
	noteNode, _ := db.FindNode(NodeNote, "test.md")
	mentions, err := db.GetNeighbors(noteNode.ID, RelMentions, "forward")
	if err != nil {
		t.Errorf("GetNeighbors mentions: %v", err)
	}
	foundRedis := false
	for _, n := range mentions {
		if n.ID == redis.ID {
			foundRedis = true
			break
		}
	}
	if !foundRedis {
		t.Errorf("Edge Note -> Redis (mentions) not found")
	}
}

func TestLLMExtraction_Failure(t *testing.T) {
	db := setupTestDB(t)

	client := &MockLLMClient{Err: fmt.Errorf("ollama down")}
	ext := NewExtractor(db)
	ext.SetLLM(client, "test-model")

	// Should return error
	err := ext.ExtractFromNote(1, "fail.md", "content", "")
	if err == nil {
		t.Error("expected error when LLM fails")
	}
}

func TestLLMExtraction_InvalidJSON(t *testing.T) {
	db := setupTestDB(t)

	client := &MockLLMClient{Response: "{ invalid json"}
	ext := NewExtractor(db)
	ext.SetLLM(client, "test-model")

	err := ext.ExtractFromNote(1, "invalid.md", "content", "")
	if err == nil {
		t.Error("expected error on invalid JSON")
	}
}

func TestExtractJSONFromResponse(t *testing.T) {
	validJSON := `{"nodes": [{"type": "entity", "name": "Go"}], "edges": []}`

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "clean JSON",
			input: validJSON,
		},
		{
			name:  "think tags wrapping JSON",
			input: `<think>Let me analyze this text carefully. I need to extract entities.</think>` + validJSON,
		},
		{
			name:  "reasoning tags wrapping JSON",
			input: `<reasoning>The user wants graph extraction.</reasoning>` + validJSON,
		},
		{
			name:  "think tags with newlines",
			input: "<think>\nI see several entities here.\nLet me think about the relationships.\n</think>\n\n" + validJSON,
		},
		{
			name:  "prose before JSON",
			input: "Here is the extracted graph data:\n" + validJSON,
		},
		{
			name:  "markdown code fence",
			input: "Here are the results:\n```json\n" + validJSON + "\n```",
		},
		{
			name:  "think tags plus code fence",
			input: "<think>Analyzing...</think>\n```json\n" + validJSON + "\n```",
		},
		{
			name:    "no JSON at all",
			input:   "I cannot extract any entities from this text.",
			wantErr: true,
		},
		{
			name:    "truly invalid JSON",
			input:   `{ "nodes": [broken`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSONFromResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got: %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Verify the extracted JSON is valid and contains expected data
			var resp LLMResponse
			if err := json.Unmarshal([]byte(got), &resp); err != nil {
				t.Fatalf("extracted JSON is invalid: %v\ngot: %s", err, got)
			}
			if len(resp.Nodes) != 1 || resp.Nodes[0].Name != "Go" {
				t.Errorf("unexpected parsed result: %+v", resp)
			}
		})
	}
}

func TestLLMExtraction_ThinkingModel(t *testing.T) {
	db := setupTestDB(t)

	// Simulate a thinking model response with <think> tags before JSON
	thinkingResponse := `<think>
The user wants me to extract entities from this text about Redis caching.
I can see Redis is an entity and CachingLayer is a concept.
Let me structure this as JSON.
</think>

{"nodes": [{"type": "entity", "name": "Redis"}, {"type": "concept", "name": "CachingLayer"}], "edges": [{"source": "CachingLayer", "target": "Redis", "relation": "uses"}]}`

	client := &MockLLMClient{Response: thinkingResponse}
	ext := NewExtractor(db)
	ext.SetLLM(client, "deepseek-r1")

	err := ext.ExtractFromNote(1, "thinking.md", "We use Redis for caching.", "")
	if err != nil {
		t.Fatalf("ExtractFromNote with thinking model: %v", err)
	}

	// Verify extraction worked despite thinking tokens
	redis, err := db.FindNode("entity", "Redis")
	if err != nil {
		t.Fatalf("Redis node not found after thinking-model extraction: %v", err)
	}
	if redis.Name != "Redis" {
		t.Errorf("expected Redis, got %s", redis.Name)
	}
}
