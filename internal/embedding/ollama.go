package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Retry settings for Ollama HTTP requests.
const (
	ollamaMaxRetries = 3
	ollamaRetryBase  = 2 * time.Second // delays: 0s, 2s, 4s
)

// OllamaProvider generates embeddings via a local Ollama instance.
type OllamaProvider struct {
	httpClient *http.Client
	baseURL    string
	model      string
	dims       int
}

// newOllamaProvider creates an Ollama embedding provider.
// Returns an error if the base URL is invalid or non-localhost.
func newOllamaProvider(cfg ProviderConfig) (*OllamaProvider, error) {
	model := cfg.Model
	if model == "" {
		model = "nomic-embed-text"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	// Validate localhost-only for security
	if err := validateLocalhostOnly(baseURL); err != nil {
		return nil, err
	}

	dims := cfg.Dimensions
	if dims == 0 {
		dims = ollamaDefaultDims(model)
	}

	return &OllamaProvider{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    baseURL,
		model:      model,
		dims:       dims,
	}, nil
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

// httpError distinguishes client errors (4xx, don't retry) from server/network errors (retry).
type httpError struct {
	StatusCode int
	Body       string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("ollama returned %d: %s", e.StatusCode, e.Body)
}

func (e *httpError) isRetryable() bool {
	return e.StatusCode == 0 || e.StatusCode >= 500
}

// GetEmbedding returns an embedding vector for the given text.
// For nomic-embed-text, purpose maps to the search_document/search_query prefix.
// Retries on 5xx and network errors with exponential backoff (max 3 attempts).
func (p *OllamaProvider) GetEmbedding(text string, purpose string) ([]float32, error) {
	prefix := "search_document"
	if purpose == "query" {
		prefix = "search_query"
	}
	prompt := prefix + ": " + text

	var lastErr error
	for attempt := 0; attempt < ollamaMaxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * ollamaRetryBase
			fmt.Fprintf(os.Stderr, "same: ollama request failed, retrying in %s... (attempt %d/%d)\n",
				delay, attempt+1, ollamaMaxRetries)
			time.Sleep(delay)
		}

		result, err := p.doEmbedRequest(prompt)
		if err == nil {
			return result, nil
		}

		// If 500 with long text, try truncation instead of retry
		if he, ok := err.(*httpError); ok && he.StatusCode == http.StatusInternalServerError && len(text) > 3000 {
			truncated := text[:len(text)/2]
			return p.GetEmbedding(truncated, purpose)
		}

		// Don't retry 4xx errors
		if he, ok := err.(*httpError); ok && !he.isRetryable() {
			return nil, err
		}

		lastErr = err
	}
	return nil, fmt.Errorf("ollama request failed after %d attempts: %w", ollamaMaxRetries, lastErr)
}

// doEmbedRequest performs a single embedding HTTP request.
func (p *OllamaProvider) doEmbedRequest(prompt string) ([]float32, error) {
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
		return nil, &httpError{StatusCode: 0, Body: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &httpError{StatusCode: resp.StatusCode, Body: string(respBody)}
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

// validateLocalhostOnly returns an error if the URL does not point to localhost.
func validateLocalhostOnly(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid Ollama URL: %w", err)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("Ollama URL must point to localhost for security, got: %s", host)
	}
	return nil
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
