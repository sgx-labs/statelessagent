package embedding

import (
	"fmt"
	"strings"
	"testing"
)

func TestHumanizeError_ConnectionRefused(t *testing.T) {
	err := fmt.Errorf(`Post "http://localhost:11434/api/embed": dial tcp 127.0.0.1:11434: connect: connection refused`)
	got := HumanizeError(err)
	if !strings.Contains(got.Error(), "Cannot connect to Ollama") {
		t.Errorf("expected user-friendly connection refused message, got: %v", got)
	}
	if !strings.Contains(got.Error(), "ollama serve") {
		t.Errorf("expected hint about ollama serve, got: %v", got)
	}
}

func TestHumanizeError_Timeout(t *testing.T) {
	err := fmt.Errorf("context deadline exceeded")
	got := HumanizeError(err)
	if !strings.Contains(got.Error(), "timeout") {
		t.Errorf("expected timeout message, got: %v", got)
	}
}

func TestHumanizeError_NoSuchHost(t *testing.T) {
	err := fmt.Errorf(`Post "http://badhost:11434/api/embed": dial tcp: lookup badhost: no such host`)
	got := HumanizeError(err)
	if !strings.Contains(got.Error(), "DNS lookup failed") {
		t.Errorf("expected DNS failure message, got: %v", got)
	}
}

func TestHumanizeError_DimensionMismatch(t *testing.T) {
	err := fmt.Errorf("embedding dimensions changed from 768 to 1024")
	got := HumanizeError(err)
	if !strings.Contains(got.Error(), "same reindex --force") {
		t.Errorf("expected reindex hint, got: %v", got)
	}
}

func TestHumanizeError_ModelChanged(t *testing.T) {
	err := fmt.Errorf("embedding model changed from ollama/nomic-embed-text to openai/text-embedding-3-small")
	got := HumanizeError(err)
	if !strings.Contains(got.Error(), "same reindex --force") {
		t.Errorf("expected reindex hint, got: %v", got)
	}
}

func TestHumanizeError_UnknownPassthrough(t *testing.T) {
	err := fmt.Errorf("some unknown error")
	got := HumanizeError(err)
	if got.Error() != err.Error() {
		t.Errorf("expected passthrough for unknown error, got: %v", got)
	}
}

func TestHumanizeError_Nil(t *testing.T) {
	got := HumanizeError(nil)
	if got != nil {
		t.Errorf("expected nil for nil input, got: %v", got)
	}
}

func TestHumanizeHTTPError_OllamaConnectionRefused(t *testing.T) {
	err := &httpError{StatusCode: 0, Body: "connection refused", Reason: "connection_refused"}
	got := humanizeHTTPError(err)
	if !strings.Contains(got.Error(), "Cannot connect to Ollama") {
		t.Errorf("expected connection refused message, got: %v", got)
	}
}

func TestHumanizeHTTPError_OllamaTimeout(t *testing.T) {
	err := &httpError{StatusCode: 0, Body: "timeout", Reason: "timeout"}
	got := humanizeHTTPError(err)
	if !strings.Contains(got.Error(), "timeout") {
		t.Errorf("expected timeout message, got: %v", got)
	}
}

func TestHumanizeHTTPError_OpenAI401(t *testing.T) {
	err := &openaiHTTPError{StatusCode: 401, Message: "invalid api key"}
	got := humanizeHTTPError(err)
	if !strings.Contains(got.Error(), "Authentication failed") {
		t.Errorf("expected auth failure message, got: %v", got)
	}
}

func TestHumanizeHTTPError_OpenAI429(t *testing.T) {
	err := &openaiHTTPError{StatusCode: 429, Message: "rate limited"}
	got := humanizeHTTPError(err)
	if !strings.Contains(got.Error(), "Rate limited") {
		t.Errorf("expected rate limit message, got: %v", got)
	}
}
