package graph

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// LLMClient abstracts a chat provider for testing.
type LLMClient interface {
	GenerateJSON(model, prompt string) (string, error)
}

// LLMExtractor uses an LLM to extract semantic graph data from text.
type LLMExtractor struct {
	client LLMClient
	model  string
}

// NewLLMExtractor creates a new extractor using the provided LLM client.
func NewLLMExtractor(client LLMClient, model string) *LLMExtractor {
	return &LLMExtractor{
		client: client,
		model:  model,
	}
}

// LLMNode represents a node extracted by the LLM.
type LLMNode struct {
	Type string `json:"type"` // "entity", "decision", "concept"
	Name string `json:"name"`
}

// LLMEdge represents an edge extracted by the LLM.
type LLMEdge struct {
	Source   string `json:"source"`   // Name of source node
	Target   string `json:"target"`   // Name of target node
	Relation string `json:"relation"` // "affects", "uses", "related_to"
}

// LLMResponse is the expected JSON structure from the LLM.
type LLMResponse struct {
	Nodes []LLMNode `json:"nodes"`
	Edges []LLMEdge `json:"edges"`
}

// reThinkTags matches <think>...</think> blocks (common in DeepSeek-R1, QwQ).
var reThinkTags = regexp.MustCompile(`(?si)<think>.*?</think>`)

// reReasoningTags matches <reasoning>...</reasoning> blocks.
var reReasoningTags = regexp.MustCompile(`(?si)<reasoning>.*?</reasoning>`)

// reCodeFenceJSON matches ```json ... ``` blocks.
var reCodeFenceJSON = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)```")

// extractJSONFromResponse strips thinking/reasoning tokens and extracts the
// first valid JSON object from a potentially noisy LLM response. This handles
// chain-of-thought models (DeepSeek-R1, QwQ, gpt-oss) that emit reasoning
// tokens before the actual JSON payload.
func extractJSONFromResponse(raw string) (string, error) {
	// 1. Strip <think>...</think> tags
	cleaned := reThinkTags.ReplaceAllString(raw, "")
	// 2. Strip <reasoning>...</reasoning> tags
	cleaned = reReasoningTags.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)

	// 3. Try direct unmarshal first (fast path for well-behaved models)
	if json.Valid([]byte(cleaned)) {
		return cleaned, nil
	}

	// 4. Try to extract JSON object: find first { and last }
	if start := strings.Index(cleaned, "{"); start >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > start {
			candidate := cleaned[start : end+1]
			if json.Valid([]byte(candidate)) {
				return candidate, nil
			}
		}
	}

	// 5. Try markdown code fences (```json ... ```)
	if matches := reCodeFenceJSON.FindStringSubmatch(cleaned); len(matches) > 1 {
		fenced := strings.TrimSpace(matches[1])
		if json.Valid([]byte(fenced)) {
			return fenced, nil
		}
		// Try { ... } within the fenced block
		if start := strings.Index(fenced, "{"); start >= 0 {
			if end := strings.LastIndex(fenced, "}"); end > start {
				candidate := fenced[start : end+1]
				if json.Valid([]byte(candidate)) {
					return candidate, nil
				}
			}
		}
	}

	// 6. Also try on the original raw input (in case stripping removed too much)
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			candidate := raw[start : end+1]
			if json.Valid([]byte(candidate)) {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("no valid JSON object found in response")
}

// Extract analyzes the text and returns structured graph data.
func (e *LLMExtractor) Extract(text string) (*LLMResponse, error) {
	if strings.TrimSpace(text) == "" {
		return &LLMResponse{}, nil
	}

	// Truncate text if too long to fit in context context (rough heuristic)
	if len(text) > 12000 {
		text = text[:12000]
	}

	prompt := fmt.Sprintf(`You are a knowledge graph extractor. Analyze the following text and extract key entities and relationships.
Return ONLY a JSON object with "nodes" and "edges" arrays.

Node Types:
- "decision": Key architectural or design decisions (e.g. "Use SQLite", "Adhere to MVVM")
- "entity": Libraries, technologies, external systems (e.g. "React", "AWS S3", "Redis")
- "concept": Key domain concepts (e.g. "UserAuth", "PaymentFlow")

Edge Relations:
- "affects": A decision affects an entity or concept
- "uses": An entity uses another entity
- "related_to": General relationship

Rules:
1. Normalize names (e.g. "Go lang" -> "Go", "postgresql" -> "PostgreSQL").
2. Keep decision names concise (3-5 words).
3. Do not extract generic terms like "code", "file", "system".
4. Ensure all edge sources and targets exist in the nodes array or are clearly implied.

Text to analyze:
%s

JSON Output:`, text)

	responseJSON, err := e.client.GenerateJSON(e.model, prompt)
	if err != nil {
		return nil, fmt.Errorf("llm generation: %w", err)
	}

	// Strip thinking/reasoning tokens and extract clean JSON.
	cleanJSON, err := extractJSONFromResponse(responseJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal llm response: %w\nResponse: %s", err, responseJSON)
	}

	var resp LLMResponse
	if err := json.Unmarshal([]byte(cleanJSON), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal llm response: %w\nResponse: %s", err, cleanJSON)
	}

	return &resp, nil
}
