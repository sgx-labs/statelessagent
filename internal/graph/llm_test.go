package graph

import (
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
