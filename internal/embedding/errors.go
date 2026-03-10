package embedding

import (
	"fmt"
	"strings"
)

// HumanizeError translates common network and provider errors into
// user-friendly messages. If the error doesn't match a known pattern,
// it is returned unchanged.
func HumanizeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "connection refused"):
		return fmt.Errorf("Cannot connect to Ollama. Is it running? Start with: ollama serve")
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "context deadline exceeded") ||
		(strings.Contains(lower, "timeout") && !strings.Contains(lower, "retry")):
		return fmt.Errorf("Embedding provider is not responding (timeout). Try a smaller model or use keyword-only mode (same reindex --lite)")
	case strings.Contains(lower, "no such host"):
		return fmt.Errorf("Cannot reach embedding endpoint (DNS lookup failed). Check your configuration with: same config show")
	case strings.Contains(lower, "embedding dimensions changed") || strings.Contains(lower, "embedding dimension mismatch"):
		return fmt.Errorf("Your embedding model changed. Run 'same reindex --force' to rebuild the index")
	case strings.Contains(lower, "embedding model changed"):
		return fmt.Errorf("Your embedding model changed. Run 'same reindex --force' to rebuild the index")
	}

	return err
}

// humanizeHTTPError converts an HTTP error into a user-friendly message.
// Used by providers after HTTP failures.
func humanizeHTTPError(err error) error {
	if err == nil {
		return nil
	}

	// Check if it's an httpError (Ollama)
	if he, ok := err.(*httpError); ok {
		switch he.Reason {
		case "connection_refused":
			return fmt.Errorf("Cannot connect to Ollama. Is it running? Start with: ollama serve")
		case "timeout":
			return fmt.Errorf("Ollama is not responding (timeout). Try a smaller model or use keyword-only mode (same reindex --lite)")
		case "dns_failure":
			return fmt.Errorf("Cannot reach Ollama endpoint (DNS lookup failed). Check your configuration with: same config show")
		case "permission_denied":
			return fmt.Errorf("Permission denied connecting to Ollama. Check firewall or security settings")
		}
	}

	// Check if it's an openaiHTTPError
	if he, ok := err.(*openaiHTTPError); ok {
		if he.StatusCode == 0 {
			return HumanizeError(err)
		}
		if he.StatusCode == 401 {
			return fmt.Errorf("Authentication failed. Check your API key with: same config show")
		}
		if he.StatusCode == 429 {
			return fmt.Errorf("Rate limited by embedding provider. Wait a moment and try again")
		}
	}

	return HumanizeError(err)
}
