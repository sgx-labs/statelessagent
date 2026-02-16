// Package ollama provides a client for Ollama LLM inference (generate/chat).
// Separate from the embedding package since embeddings and LLM generation
// use different models and have different retry/timeout characteristics.
package ollama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
)

// Client talks to a local Ollama instance for LLM generation.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient creates an Ollama LLM client using the configured URL.
func NewClient() (*Client, error) {
	baseURL, err := config.OllamaURL()
	if err != nil {
		return nil, err
	}
	return &Client{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		baseURL:    baseURL,
	}, nil
}

// NewClientWithURL creates an Ollama LLM client with a specific base URL.
// Used for testing. No localhost validation is performed.
func NewClientWithURL(baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    baseURL,
	}
}

// Model represents an Ollama model from /api/tags.
type Model struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type tagsResponse struct {
	Models []Model `json:"models"`
}

// embedModels are known embedding-only models that can't do generation.
var embedModels = map[string]bool{
	"nomic-embed-text":        true,
	"nomic-embed-text-v2-moe": true,
	"mxbai-embed-large":       true,
	"all-minilm":              true,
	"snowflake-arctic-embed":  true,
	"snowflake-arctic-embed2": true,
	"embeddinggemma":          true,
	"qwen3-embedding":         true,
	"bge-base-en":             true,
	"bge-large-en":            true,
	"bge-m3":                  true,
}

// ListChatModels returns available chat/instruct models (excludes embedding models).
func (c *Client) ListChatModels() ([]Model, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("connect to Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama returned %d", resp.StatusCode)
	}

	var tags tagsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10*1024*1024)).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	var chat []Model
	for _, m := range tags.Models {
		baseName := m.Name
		if idx := strings.Index(baseName, ":"); idx > 0 {
			baseName = baseName[:idx]
		}
		if embedModels[baseName] {
			continue
		}
		chat = append(chat, m)
	}
	return chat, nil
}

// preferredModels lists models in preference order (smallest/fastest first).
var preferredModels = []string{
	"llama3.2:1b", "llama3.2:3b", "llama3.2",
	"qwen2.5:3b", "qwen2.5:7b", "qwen2.5",
	"mistral", "gemma2", "phi3",
}

// PickBestModel selects the best available chat model.
// Prefers smaller models for speed. Returns empty string if none available.
func (c *Client) PickBestModel() (string, error) {
	models, err := c.ListChatModels()
	if err != nil {
		return "", err
	}
	if len(models) == 0 {
		return "", nil
	}

	available := make(map[string]bool, len(models))
	for _, m := range models {
		available[m.Name] = true
	}

	for _, pref := range preferredModels {
		if available[pref] {
			return pref, nil
		}
	}

	// Fall back to first available chat model
	return models[0].Name, nil
}

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Format string `json:"format,omitempty"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
}

// Generate sends a prompt to Ollama and returns the response.
func (c *Client) Generate(model, prompt string) (string, error) {
	return c.generate(model, prompt, "")
}

// GenerateJSON sends a prompt to Ollama and forces a JSON response.
func (c *Client) GenerateJSON(model, prompt string) (string, error) {
	return c.generate(model, prompt, "json")
}

func (c *Client) generate(model, prompt, format string) (string, error) {
	body, err := json.Marshal(generateRequest{
		Model:  model,
		Prompt: prompt,
		Format: format,
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("Ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result generateResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10*1024*1024)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return strings.TrimSpace(result.Response), nil
}
