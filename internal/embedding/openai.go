package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIProvider generates embeddings via the OpenAI API.
type OpenAIProvider struct {
	httpClient *http.Client
	baseURL    string
	model      string
	apiKey     string
	dims       int
}

// newOpenAIProvider creates an OpenAI embedding provider.
func newOpenAIProvider(cfg ProviderConfig) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai embedding provider requires an API key (set SAME_EMBED_API_KEY or embedding.api_key in config)")
	}

	model := cfg.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	dims := cfg.Dimensions
	if dims == 0 {
		dims = openaiDefaultDims(model)
	}

	return &OpenAIProvider{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		model:      model,
		apiKey:     cfg.APIKey,
		dims:       dims,
	}, nil
}

func (p *OpenAIProvider) Name() string    { return "openai" }
func (p *OpenAIProvider) Dimensions() int { return p.dims }

type openaiEmbeddingRequest struct {
	Input      string `json:"input"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions,omitempty"`
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
// OpenAI doesn't use document/query prefixes — the model handles both.
func (p *OpenAIProvider) GetEmbedding(text string, _ string) ([]float32, error) {
	// Truncate if too long (OpenAI has 8191 token limit for most models)
	if len(text) > 30000 {
		text = text[:30000]
	}

	reqBody := openaiEmbeddingRequest{
		Input: text,
		Model: p.model,
	}

	// text-embedding-3-* models support custom dimensions
	if p.dims > 0 && isVariableDimModel(p.model) {
		reqBody.Dimensions = p.dims
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", p.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result openaiEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("openai error: %s", result.Error.Message)
	}

	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}

	return result.Data[0].Embedding, nil
}

func (p *OpenAIProvider) GetDocumentEmbedding(text string) ([]float32, error) {
	return p.GetEmbedding(text, "document")
}

func (p *OpenAIProvider) GetQueryEmbedding(text string) ([]float32, error) {
	return p.GetEmbedding(text, "query")
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
