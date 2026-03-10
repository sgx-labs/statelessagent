package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Retry settings and batch limits for OpenAI HTTP requests.
const (
	openaiMaxRetries = 3
	openaiRetryBase  = 2 * time.Second // delays: 0s, 2s, 4s
	openaiBatchSize  = 50              // max texts per batch request
)

// OpenAIProvider generates embeddings via the OpenAI API or any
// OpenAI-compatible endpoint (llama.cpp, VLLM, LM Studio, etc.).
type OpenAIProvider struct {
	httpClient *http.Client
	baseURL    string
	model      string
	apiKey     string
	dims       int
	name       string // "openai" or "openai-compatible"
}

// newOpenAIProvider creates an OpenAI or OpenAI-compatible embedding provider.
// API key is required for api.openai.com but optional for local/custom endpoints
// (llama.cpp, VLLM, LM Studio, etc.).
func newOpenAIProvider(cfg ProviderConfig) (*OpenAIProvider, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	// API key is only required for the real OpenAI API
	isOpenAI := baseURL == "https://api.openai.com"
	if isOpenAI && cfg.APIKey == "" {
		return nil, fmt.Errorf("openai embedding provider requires an API key (set SAME_EMBED_API_KEY or embedding.api_key in config)")
	}

	model := cfg.Model
	if model == "" {
		if isOpenAI {
			model = "text-embedding-3-small"
		} else {
			return nil, fmt.Errorf("openai-compatible provider requires a model name (set SAME_EMBED_MODEL or embedding.model in config)")
		}
	}

	dims := cfg.Dimensions
	if dims == 0 {
		if isOpenAI {
			dims = openaiDefaultDims(model)
		}
		// For local servers, dims=0 means accept whatever the server returns
	}

	name := "openai"
	if !isOpenAI {
		name = "openai-compatible"
		// Warn if embedding requests will leave localhost
		if u, err := url.Parse(baseURL); err == nil {
			host := u.Hostname()
			if host != "localhost" && host != "127.0.0.1" && host != "::1" {
				fmt.Fprintf(os.Stderr, "same: warning: embedding requests will be sent to remote server (%s)\n", u.Host)
			}
		}
	}

	return &OpenAIProvider{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		model:      model,
		apiKey:     cfg.APIKey,
		dims:       dims,
		name:       name,
	}, nil
}

func (p *OpenAIProvider) Name() string    { return p.name }
func (p *OpenAIProvider) Model() string   { return p.model }
func (p *OpenAIProvider) Dimensions() int { return p.dims }

type openaiEmbeddingRequest struct {
	Input      interface{} `json:"input"` // string for single, []string for batch
	Model      string      `json:"model"`
	Dimensions int         `json:"dimensions,omitempty"`
}

type openaiEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// GetEmbedding returns an embedding vector for the given text.
// OpenAI doesn't use document/query prefixes -- the model handles both.
// Delegates to the batch path with a single-item input.
func (p *OpenAIProvider) GetEmbedding(text string, _ string) ([]float32, error) {
	vecs, err := p.GetDocumentEmbeddings([]string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// openaiHTTPError distinguishes retryable errors from non-retryable ones.
type openaiHTTPError struct {
	StatusCode int
	Message    string // sanitized message (no API key)
}

func (e *openaiHTTPError) Error() string {
	return fmt.Sprintf("openai returned %d: %s", e.StatusCode, e.Message)
}

// isRetryable returns true for 429 (rate limit) and 5xx (server) errors.
func (e *openaiHTTPError) isRetryable() bool {
	return e.StatusCode == 0 || e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// doEmbedRequest performs a single embedding HTTP request and returns all
// embeddings in the response, sorted by the index field.
func (p *OpenAIProvider) doEmbedRequest(body []byte) ([][]float32, error) {
	req, err := http.NewRequest("POST", strings.TrimRight(p.baseURL, "/")+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	// App attribution headers for OpenRouter and compatible services
	req.Header.Set("X-Title", "SAME")
	req.Header.Set("HTTP-Referer", "https://statelessagent.com")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, &openaiHTTPError{StatusCode: 0, Message: sanitizeError(err.Error(), p.apiKey)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		// SECURITY: sanitize response to prevent API key leakage (E7)
		sanitized := sanitizeError(string(respBody), p.apiKey)
		return nil, &openaiHTTPError{StatusCode: resp.StatusCode, Message: sanitized}
	}

	// Limit successful response body to 100MB to prevent a malicious endpoint
	// from sending an unbounded response that exhausts memory.
	var result openaiEmbeddingResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 100*1024*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("openai error: %s", sanitizeError(result.Error.Message, p.apiKey))
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}

	// Sort by index to ensure correct ordering
	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].Index < result.Data[j].Index
	})

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("empty embedding at index %d", d.Index)
		}
		if err := validateEmbedding(d.Embedding, p.dims); err != nil {
			return nil, fmt.Errorf("embedding at index %d: %w", d.Index, err)
		}
		embeddings[i] = d.Embedding
	}

	return embeddings, nil
}

func (p *OpenAIProvider) GetDocumentEmbedding(text string) ([]float32, error) {
	vecs, err := p.GetDocumentEmbeddings([]string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// GetDocumentEmbeddings returns embeddings for multiple texts using batch requests.
// Texts are sent in batches of up to openaiBatchSize per HTTP request.
// Each batch is retried independently on transient errors.
func (p *OpenAIProvider) GetDocumentEmbeddings(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	allEmbeddings := make([][]float32, 0, len(texts))

	for batchStart := 0; batchStart < len(texts); batchStart += openaiBatchSize {
		batchEnd := batchStart + openaiBatchSize
		if batchEnd > len(texts) {
			batchEnd = len(texts)
		}
		batch := texts[batchStart:batchEnd]

		// Truncate individual texts (OpenAI has 8191 token limit for most models)
		truncated := make([]string, len(batch))
		for i, t := range batch {
			if len(t) > 30000 {
				truncated[i] = t[:30000]
			} else {
				truncated[i] = t
			}
		}

		reqBody := openaiEmbeddingRequest{
			Input: truncated,
			Model: p.model,
		}
		if p.dims > 0 && isVariableDimModel(p.model) {
			reqBody.Dimensions = p.dims
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("marshal batch request: %w", err)
		}

		var lastErr error
		var batchResult [][]float32
		for attempt := 0; attempt < openaiMaxRetries; attempt++ {
			if attempt > 0 {
				delay := time.Duration(attempt) * openaiRetryBase
				fmt.Fprintf(os.Stderr, "same: openai batch request failed, retrying in %s... (attempt %d/%d)\n",
					delay, attempt+1, openaiMaxRetries)
				time.Sleep(delay)
			}

			batchResult, err = p.doEmbedRequest(body)
			if err == nil {
				break
			}

			if he, ok := err.(*openaiHTTPError); ok && !he.isRetryable() {
				return nil, he
			}
			lastErr = err
		}
		if err != nil {
			return nil, humanizeHTTPError(fmt.Errorf("openai batch request failed after %d attempts: %w", openaiMaxRetries, lastErr))
		}

		if len(batchResult) != len(batch) {
			return nil, fmt.Errorf("openai returned %d embeddings, expected %d", len(batchResult), len(batch))
		}

		allEmbeddings = append(allEmbeddings, batchResult...)
	}

	return allEmbeddings, nil
}

func (p *OpenAIProvider) GetQueryEmbedding(text string) ([]float32, error) {
	return p.GetEmbedding(text, "query")
}

// sanitizeError removes any occurrence of the API key from an error message
// to prevent credential leakage in logs or user-facing output.
func sanitizeError(msg, apiKey string) string {
	if apiKey == "" {
		return msg
	}
	return strings.ReplaceAll(msg, apiKey, "[REDACTED]")
}

// openaiDefaultDims returns default dimensions for known OpenAI embedding models.
func openaiDefaultDims(model string) int {
	switch model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 1536
	}
}

// isVariableDimModel returns true if the model supports custom dimension output.
func isVariableDimModel(model string) bool {
	return model == "text-embedding-3-small" || model == "text-embedding-3-large"
}
