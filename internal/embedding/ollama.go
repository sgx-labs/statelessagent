package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// OllamaProvider generates embeddings via a local Ollama instance.
type OllamaProvider struct {
	httpClient *http.Client
	baseURL    string
	model      string
	dims       int
}

// newOllamaProvider creates an Ollama embedding provider.
func newOllamaProvider(cfg ProviderConfig) *OllamaProvider {
	model := cfg.Model
	if model == "" {
		model = "nomic-embed-text"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	// Validate localhost-only for security
	validateLocalhostOnly(baseURL)

	dims := cfg.Dimensions
	if dims == 0 {
		dims = ollamaDefaultDims(model)
	}

	return &OllamaProvider{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    baseURL,
		model:      model,
		dims:       dims,
	}
}

func (p *OllamaProvider) Name() string    { return "ollama" }
func (p *OllamaProvider) Dimensions() int { return p.dims }

type ollamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbeddingResponse struct {
	Embedding []float32 `json:"embedding"`
}

// GetEmbedding returns an embedding vector for the given text.
// For nomic-embed-text, purpose maps to the search_document/search_query prefix.
func (p *OllamaProvider) GetEmbedding(text string, purpose string) ([]float32, error) {
	prefix := "search_document"
	if purpose == "query" {
		prefix = "search_query"
	}

	prompt := prefix + ": " + text

	body, err := json.Marshal(ollamaEmbeddingRequest{
		Model:  p.model,
		Prompt: prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := p.httpClient.Post(
		p.baseURL+"/api/embeddings",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	// If 500 (likely context overflow), retry with truncated text
	if resp.StatusCode == http.StatusInternalServerError && len(text) > 3000 {
		resp.Body.Close()
		truncated := text[:len(text)/2]
		return p.GetEmbedding(truncated, purpose)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}

	return result.Embedding, nil
}

func (p *OllamaProvider) GetDocumentEmbedding(text string) ([]float32, error) {
	return p.GetEmbedding(text, "document")
}

func (p *OllamaProvider) GetQueryEmbedding(text string) ([]float32, error) {
	return p.GetEmbedding(text, "query")
}

// validateLocalhostOnly panics if the URL does not point to localhost.
func validateLocalhostOnly(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(fmt.Sprintf("invalid Ollama URL: %v", err))
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		panic(fmt.Sprintf("Ollama URL must point to localhost for security. Got: %s", host))
	}
}

// ollamaDefaultDims returns the default embedding dimensions for known Ollama models.
func ollamaDefaultDims(model string) int {
	switch model {
	case "nomic-embed-text":
		return 768
	case "mxbai-embed-large":
		return 1024
	case "all-minilm":
		return 384
	case "snowflake-arctic-embed":
		return 1024
	default:
		return 768
	}
}
