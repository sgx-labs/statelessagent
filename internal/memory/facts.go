package memory

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/llm"
)

// Fact represents an atomic piece of knowledge extracted from a source chunk.
// Facts are the search layer in a dual-layer memory architecture: searches hit
// facts (precision), then return linked source chunks to the LLM (recall).
type Fact struct {
	Text       string  `json:"text"`
	SourcePath string  `json:"source_path"`
	ChunkID    int     `json:"chunk_id"`
	Confidence float64 `json:"confidence"`
}

// factExtractionResponse is the expected JSON structure from the LLM.
type factExtractionResponse struct {
	Facts []extractedFact `json:"facts"`
}

// extractedFact is a single fact as returned by the LLM.
type extractedFact struct {
	Text       string  `json:"text"`
	Category   string  `json:"category"` // "declaration", "preference", "decision", "relationship"
	Confidence float64 `json:"confidence"`
}

// reThinkTags matches <think>...</think> blocks (common in DeepSeek-R1, QwQ).
var reFactThinkTags = regexp.MustCompile(`(?si)<think>.*?</think>`)

// reReasoningTags matches <reasoning>...</reasoning> blocks.
var reFactReasoningTags = regexp.MustCompile(`(?si)<reasoning>.*?</reasoning>`)

// reCodeFenceJSON matches ```json ... ``` blocks.
var reFactCodeFenceJSON = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)```")

const factExtractionPrompt = `You are a fact extraction engine for a personal knowledge base. Extract atomic, self-contained facts from the following text.

EXTRACT these types of facts:
- First-person declarations ("I graduated with a Business Administration degree")
- Preferences ("I prefer dark mode", "We use PostgreSQL for production")
- Decisions and choices ("We chose React over Vue", "Decided to use microservices")
- Relationships and associations ("Alice is the team lead", "The API connects to Redis")
- Quantitative facts ("Daily commute is 30 minutes", "Team has 5 engineers")
- Temporal facts ("Started this job in 2024", "Sprint ends on Friday")

DO NOT extract:
- Code structure or file organization ("main.go imports fmt")
- Derivable information ("This file has 200 lines")
- Generic programming patterns ("Uses error handling")
- Markdown formatting or document structure
- Speculative or hypothetical statements ("We could use Redis")
- Questions or requests

Rules:
1. Each fact must be self-contained — understandable without the source text.
2. Preserve the original meaning and specificity. Do not generalize.
3. One fact per atomic claim. Split compound statements.
4. Assign confidence: 1.0 for explicit statements, 0.8 for strong implications, 0.6 for inferences.
5. Categorize each fact: "declaration", "preference", "decision", or "relationship".

Return ONLY a JSON object with a "facts" array. Each fact has "text", "category", and "confidence" fields.

Text to extract from:
%s

JSON Output:`

// ExtractFacts uses an LLM to extract atomic facts from text content.
// The returned facts are linked to the source path and chunk ID for
// provenance tracking. This is the extraction half of the dual-layer
// memory architecture.
func ExtractFacts(content string, chatClient llm.Client, model string) ([]Fact, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}

	// Truncate very long content to fit LLM context window.
	if len(content) > 12000 {
		content = content[:12000]
	}

	prompt := fmt.Sprintf(factExtractionPrompt, content)

	responseJSON, err := chatClient.GenerateJSON(model, prompt)
	if err != nil {
		return nil, fmt.Errorf("fact extraction llm call: %w", err)
	}

	cleanJSON, err := extractFactJSON(responseJSON)
	if err != nil {
		return nil, fmt.Errorf("parse fact extraction response: %w\nResponse: %s", err, responseJSON)
	}

	var resp factExtractionResponse
	if err := json.Unmarshal([]byte(cleanJSON), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal fact extraction response: %w\nResponse: %s", err, cleanJSON)
	}

	// Convert to public Fact type, filtering empty/invalid entries.
	var facts []Fact
	for _, ef := range resp.Facts {
		text := strings.TrimSpace(ef.Text)
		if text == "" {
			continue
		}
		// Skip very short facts that are likely noise.
		if len(text) < 10 {
			continue
		}
		confidence := ef.Confidence
		if confidence <= 0 || confidence > 1.0 {
			confidence = 0.6 // default for unspecified
		}
		facts = append(facts, Fact{
			Text:       text,
			Confidence: confidence,
		})
	}

	return facts, nil
}

// extractFactJSON strips thinking/reasoning tokens and extracts the first valid
// JSON object from a potentially noisy LLM response. Mirrors the pattern from
// graph/llm.go but is kept separate to avoid cross-package coupling.
func extractFactJSON(raw string) (string, error) {
	// Strip thinking blocks
	cleaned := reFactThinkTags.ReplaceAllString(raw, "")
	cleaned = reFactReasoningTags.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)

	// Fast path: direct parse
	if json.Valid([]byte(cleaned)) {
		return cleaned, nil
	}

	// Try to extract JSON object: first { to last }
	if start := strings.Index(cleaned, "{"); start >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > start {
			candidate := cleaned[start : end+1]
			if json.Valid([]byte(candidate)) {
				return candidate, nil
			}
		}
	}

	// Try markdown code fences
	if matches := reFactCodeFenceJSON.FindStringSubmatch(cleaned); len(matches) > 1 {
		fenced := strings.TrimSpace(matches[1])
		if json.Valid([]byte(fenced)) {
			return fenced, nil
		}
		if start := strings.Index(fenced, "{"); start >= 0 {
			if end := strings.LastIndex(fenced, "}"); end > start {
				candidate := fenced[start : end+1]
				if json.Valid([]byte(candidate)) {
					return candidate, nil
				}
			}
		}
	}

	// Fallback: try raw input
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			candidate := raw[start : end+1]
			if json.Valid([]byte(candidate)) {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("no valid JSON object found in fact extraction response")
}
