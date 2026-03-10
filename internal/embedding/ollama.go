package embedding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
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
func (p *OllamaProvider) Model() string   { return p.model }
func (p *OllamaProvider) Dimensions() int { return p.dims }

// ollamaBatchSize is the maximum number of texts per /api/embed request.
// Keeps memory usage reasonable for large vaults.
const ollamaBatchSize = 50

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// httpError distinguishes client errors (4xx, don't retry) from server/network errors (retry).
type httpError struct {
	StatusCode int
	Body       string
	Reason     string // classified reason: "connection_refused", "permission_denied", "timeout", "dns_failure", "network_error"
}

func (e *httpError) Error() string {
	if e.StatusCode == 0 && e.Reason != "" {
		return fmt.Sprintf("ollama: %s (%s)", e.Reason, e.Body)
	}
	return fmt.Sprintf("ollama returned %d: %s", e.StatusCode, e.Body)
}

func (e *httpError) isRetryable() bool {
	// Permission denied is not retryable (sandbox policy)
	if e.Reason == "permission_denied" {
		return false
	}
	return e.StatusCode == 0 || e.StatusCode >= 500
}

// classifyNetworkError examines a network error to produce a human-readable reason.
func classifyNetworkError(err error) string {
	if err == nil {
		return "unknown"
	}

	// Check for net.OpError with specific context
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return "timeout"
		}
	}

	// Check for DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_failure"
	}

	// String-based fallback for wrapped errors
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "permission denied"):
		return "permission_denied"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return "timeout"
	case strings.Contains(msg, "no such host"):
		return "dns_failure"
	}

	return "network_error"
}

// GetEmbedding returns an embedding vector for the given text.
// For nomic-embed-text, purpose maps to the search_document/search_query prefix.
// Retries on 5xx and network errors with exponential backoff (max 3 attempts).
func (p *OllamaProvider) GetEmbedding(text string, purpose string) ([]float32, error) {
	return p.getEmbeddingWithDepth(text, purpose, 0)
}

// ollamaMaxTruncationDepth limits how many times GetEmbedding can recursively
// truncate text on 500 errors. Prevents unbounded recursion with large inputs.
const ollamaMaxTruncationDepth = 3

func (p *OllamaProvider) getEmbeddingWithDepth(text string, purpose string, depth int) ([]float32, error) {
	prefix := "search_document"
	if purpose == "query" {
		prefix = "search_query"
	}
	prompt := prefix + ": " + text

	var lastErr error
	for attempt := 0; attempt < ollamaMaxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * ollamaRetryBase
			reason := ""
			if he, ok := lastErr.(*httpError); ok && he.Reason != "" {
				reason = fmt.Sprintf(" [%s]", he.Reason)
			}
			fmt.Fprintf(os.Stderr, "same: ollama request failed%s, retrying in %s... (attempt %d/%d)\n",
				reason, delay, attempt+1, ollamaMaxRetries)
			time.Sleep(delay)
		}

		results, err := p.doBatchEmbedRequest([]string{prompt})
		if err == nil {
			if len(results) == 0 || len(results[0]) == 0 {
				return nil, fmt.Errorf("empty embedding returned")
			}
			return results[0], nil
		}

		// If 500 with long text, try truncation instead of retry (bounded depth)
		if he, ok := err.(*httpError); ok && he.StatusCode == http.StatusInternalServerError && len(text) > 3000 && depth < ollamaMaxTruncationDepth {
			truncated := text[:len(text)/2]
			return p.getEmbeddingWithDepth(truncated, purpose, depth+1)
		}

		// Don't retry 4xx errors
		if he, ok := err.(*httpError); ok && !he.isRetryable() {
			return nil, err
		}

		lastErr = err
	}
	return nil, humanizeHTTPError(fmt.Errorf("ollama request failed after %d attempts: %w", ollamaMaxRetries, lastErr))
}

// doBatchEmbedRequest sends a batch of texts to the /api/embed endpoint.
func (p *OllamaProvider) doBatchEmbedRequest(inputs []string) ([][]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{
		Model: p.model,
		Input: inputs,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := p.httpClient.Post(
		strings.TrimRight(p.baseURL, "/")+"/api/embed",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		reason := classifyNetworkError(err)
		return nil, &httpError{StatusCode: 0, Body: sanitizeOllamaError(err.Error()), Reason: reason}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, &httpError{StatusCode: resp.StatusCode, Body: sanitizeOllamaError(string(respBody))}
	}

	// Limit successful response body to 100MB to prevent a malicious endpoint
	// from sending an unbounded response that exhausts memory.
	var result ollamaEmbedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 100*1024*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(inputs), len(result.Embeddings))
	}

	for i, emb := range result.Embeddings {
		if err := validateEmbedding(emb, p.dims); err != nil {
			return nil, fmt.Errorf("embedding %d: %w", i, err)
		}
	}

	return result.Embeddings, nil
}

func (p *OllamaProvider) GetDocumentEmbedding(text string) ([]float32, error) {
	return p.GetEmbedding(text, "document")
}

// GetDocumentEmbeddings returns embeddings for multiple texts using batch requests.
// Texts are split into batches of ollamaBatchSize and sent to the /api/embed endpoint.
func (p *OllamaProvider) GetDocumentEmbeddings(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Prepend document prefix to each text
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = "search_document: " + t
	}

	allEmbeddings := make([][]float32, 0, len(texts))

	for start := 0; start < len(prefixed); start += ollamaBatchSize {
		end := start + ollamaBatchSize
		if end > len(prefixed) {
			end = len(prefixed)
		}
		batch := prefixed[start:end]

		var lastErr error
		var results [][]float32
		for attempt := 0; attempt < ollamaMaxRetries; attempt++ {
			if attempt > 0 {
				delay := time.Duration(attempt) * ollamaRetryBase
				reason := ""
				if he, ok := lastErr.(*httpError); ok && he.Reason != "" {
					reason = fmt.Sprintf(" [%s]", he.Reason)
				}
				fmt.Fprintf(os.Stderr, "same: ollama batch request failed%s, retrying in %s... (attempt %d/%d)\n",
					reason, delay, attempt+1, ollamaMaxRetries)
				time.Sleep(delay)
			}

			var err error
			results, err = p.doBatchEmbedRequest(batch)
			if err == nil {
				break
			}

			if he, ok := err.(*httpError); ok && !he.isRetryable() {
				return nil, err
			}
			lastErr = err
			results = nil
		}
		if results == nil {
			return nil, humanizeHTTPError(fmt.Errorf("ollama batch request failed after %d attempts: %w", ollamaMaxRetries, lastErr))
		}

		allEmbeddings = append(allEmbeddings, results...)
	}

	return allEmbeddings, nil
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

// sanitizeOllamaError strips internal details (file paths, stack traces, raw
// error bodies) from Ollama error messages to prevent information leakage.
func sanitizeOllamaError(msg string) string {
	// Truncate overly long error bodies that may contain internal details
	const maxLen = 256
	if len(msg) > maxLen {
		msg = msg[:maxLen] + "... (truncated)"
	}
	return msg
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
	case "snowflake-arctic-embed2":
		return 1024
	case "embeddinggemma":
		return 768
	case "qwen3-embedding":
		return 1024
	case "nomic-embed-text-v2-moe":
		return 768
	case "bge-m3":
		return 1024
	default:
		return 768
	}
}
