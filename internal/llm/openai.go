package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sgx-labs/statelessagent/internal/llmutil"
)

const (
	openAIDefaultBaseURL = "https://api.openai.com"
	openAIDefaultModel   = "gpt-4o-mini"
)

type openAIClientConfig struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
}

type openAIClient struct {
	provider   string
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func newOpenAIClient(cfg openAIClientConfig) (*openAIClient, error) {
	provider := normalizeProvider(cfg.Provider)
	if provider != "openai" && provider != "openai-compatible" {
		return nil, fmt.Errorf("unsupported chat provider: %q", cfg.Provider)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		if provider == "openai" {
			baseURL = openAIDefaultBaseURL
		} else {
			return nil, fmt.Errorf("openai-compatible chat provider requires SAME_CHAT_BASE_URL (or matching embedding base_url)")
		}
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	if provider == "openai" && apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if provider == "openai" && apiKey == "" {
		return nil, fmt.Errorf("openai chat provider requires SAME_CHAT_API_KEY or OPENAI_API_KEY")
	}

	model := strings.TrimSpace(cfg.Model)
	if provider == "openai" && model == "" {
		model = openAIDefaultModel
	}

	if provider == "openai-compatible" {
		if u, err := url.Parse(baseURL); err == nil {
			host := u.Hostname()
			if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "host.docker.internal" {
				fmt.Fprintf(os.Stderr, "same: warning: chat generation requests will be sent to remote server (%s)\n", u.Host)
			}
		}
	}

	return &openAIClient{
		provider:   provider,
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *openAIClient) Provider() string {
	return c.provider
}

func (c *openAIClient) PickBestModel() (string, error) {
	if c.model != "" {
		return c.model, nil
	}

	models, err := c.listModels()
	if err != nil {
		if c.provider == "openai" {
			return openAIDefaultModel, nil
		}
		return "", err
	}
	if len(models) == 0 {
		if c.provider == "openai" {
			return openAIDefaultModel, nil
		}
		return "", fmt.Errorf("no chat-capable models reported by %s", c.baseURL)
	}

	preferred := []string{
		"gpt-4.1-mini",
		"gpt-4o-mini",
		"gpt-4o",
		"o4-mini",
		"o3-mini",
		"gpt-4.1",
		"gpt-4-turbo",
		"gpt-3.5-turbo",
		"llama3.2",
		"llama3.1",
		"qwen2.5",
		"mistral",
		"gemma",
	}

	for _, pref := range preferred {
		for _, m := range models {
			name := strings.ToLower(m)
			if name == pref || strings.HasPrefix(name, pref+":") {
				return m, nil
			}
		}
	}

	for _, m := range models {
		if !looksLikeEmbeddingModel(m) {
			return m, nil
		}
	}
	return models[0], nil
}

func (c *openAIClient) Generate(model, prompt string) (string, error) {
	selected, err := c.resolveModel(model)
	if err != nil {
		return "", err
	}
	return c.generate(selected, prompt, false)
}

func (c *openAIClient) GenerateJSON(model, prompt string) (string, error) {
	selected, err := c.resolveModel(model)
	if err != nil {
		return "", err
	}

	resp, err := c.generate(selected, prompt, true)
	if err == nil {
		return normalizeJSONText(resp), nil
	}

	var httpErr *chatHTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusBadRequest {
		fallbackPrompt := prompt + "\n\nReturn ONLY valid JSON (no markdown or prose)."
		resp, err = c.generate(selected, fallbackPrompt, false)
		if err != nil {
			return "", err
		}
		return normalizeJSONText(resp), nil
	}

	return "", err
}

func (c *openAIClient) resolveModel(model string) (string, error) {
	if strings.TrimSpace(model) != "" {
		return strings.TrimSpace(model), nil
	}
	if c.model != "" {
		return c.model, nil
	}
	best, err := c.PickBestModel()
	if err != nil {
		return "", err
	}
	if best == "" {
		return "", fmt.Errorf("no chat model available")
	}
	return best, nil
}

type chatRequest struct {
	Model          string            `json:"model"`
	Messages       []chatMessage     `json:"messages"`
	Stream         bool              `json:"stream"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *openAIClient) generate(model, prompt string, jsonMode bool) (string, error) {
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}
	if jsonMode {
		reqBody.ResponseFormat = map[string]string{"type": "json_object"}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	// App attribution headers for OpenRouter and compatible services.
	req.Header.Set("X-Title", "SAME")
	req.Header.Set("HTTP-Referer", "https://statelessagent.com")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", &chatHTTPError{StatusCode: 0, Message: sanitizeChatError(err.Error(), c.apiKey)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return "", &chatHTTPError{StatusCode: resp.StatusCode, Message: sanitizeChatError(string(respBody), c.apiKey)}
	}

	var out chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10*1024*1024)).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("chat provider error: %s", sanitizeChatError(out.Error.Message, c.apiKey))
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty chat response")
	}

	text := parseChatContent(out.Choices[0].Message.Content)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("chat response had empty content")
	}
	return llmutil.StripThinkingTokens(text), nil
}

type listModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *openAIClient) listModels() ([]string, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, &chatHTTPError{StatusCode: resp.StatusCode, Message: sanitizeChatError(string(body), c.apiKey)}
	}

	var out listModelsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("list models: %s", sanitizeChatError(out.Error.Message, c.apiKey))
	}

	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		name := strings.TrimSpace(m.ID)
		if name == "" {
			continue
		}
		models = append(models, name)
	}
	return models, nil
}

type chatHTTPError struct {
	StatusCode int
	Message    string
}

func (e *chatHTTPError) Error() string {
	return fmt.Sprintf("chat provider returned %d: %s", e.StatusCode, e.Message)
}

func sanitizeChatError(msg, apiKey string) string {
	if apiKey == "" {
		return msg
	}
	return strings.ReplaceAll(msg, apiKey, "[REDACTED]")
}

func looksLikeEmbeddingModel(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "embed") || strings.Contains(lower, "embedding")
}

func parseChatContent(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return strings.TrimSpace(string(raw))
}

func normalizeJSONText(s string) string {
	t := llmutil.StripThinkingTokens(s)
	if strings.HasPrefix(t, "```") {
		t = strings.TrimPrefix(t, "```")
		t = strings.TrimSpace(t)
		if strings.HasPrefix(strings.ToLower(t), "json") {
			t = strings.TrimSpace(t[4:])
		}
		if idx := strings.LastIndex(t, "```"); idx >= 0 {
			t = strings.TrimSpace(t[:idx])
		}
	}

	start := strings.Index(t, "{")
	end := strings.LastIndex(t, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(t[start : end+1])
	}
	return t
}
