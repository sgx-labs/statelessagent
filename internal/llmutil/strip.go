// Package llmutil provides shared utilities for processing LLM responses.
package llmutil

import (
	"regexp"
	"strings"
)

// Compiled patterns for thinking/reasoning tags emitted by chain-of-thought
// models (DeepSeek-R1, QwQ, gpt-oss, etc.) before their actual response.
var (
	reThinkTags      = regexp.MustCompile(`(?si)<think>.*?</think>`)
	reReasoningTags  = regexp.MustCompile(`(?si)<reasoning>.*?</reasoning>`)
	reReflectionTags = regexp.MustCompile(`(?si)<reflection>.*?</reflection>`)
)

// StripThinkingTokens removes <think>...</think>, <reasoning>...</reasoning>,
// and <reflection>...</reflection> blocks from an LLM response. These are
// emitted by reasoning models before the actual answer and should not be
// shown to users or parsed as content.
//
// Returns the original string unchanged if no tags are found.
func StripThinkingTokens(s string) string {
	result := reThinkTags.ReplaceAllString(s, "")
	result = reReasoningTags.ReplaceAllString(result, "")
	result = reReflectionTags.ReplaceAllString(result, "")
	return strings.TrimSpace(result)
}
