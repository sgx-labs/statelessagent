package llmutil

import "testing"

func TestStripThinkingTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no tags",
			input: "Hello world",
			want:  "Hello world",
		},
		{
			name:  "think tags single line",
			input: "<think>let me reason about this</think>The answer is 42.",
			want:  "The answer is 42.",
		},
		{
			name:  "think tags multiline",
			input: "<think>\nStep 1: consider X\nStep 2: consider Y\n</think>\nThe answer is 42.",
			want:  "The answer is 42.",
		},
		{
			name:  "reasoning tags",
			input: "<reasoning>I need to think carefully</reasoning>Here is the result.",
			want:  "Here is the result.",
		},
		{
			name:  "reflection tags",
			input: "<reflection>Let me reconsider</reflection>Final answer: yes.",
			want:  "Final answer: yes.",
		},
		{
			name:  "mixed tags",
			input: "<think>hmm</think><reasoning>ok</reasoning>The response.",
			want:  "The response.",
		},
		{
			name:  "case insensitive",
			input: "<Think>uppercase</Think>Result here.",
			want:  "Result here.",
		},
		{
			name:  "THINK uppercase",
			input: "<THINK>all caps</THINK>Output.",
			want:  "Output.",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "only whitespace",
			input: "   ",
			want:  "",
		},
		{
			name:  "tags with surrounding whitespace",
			input: "  <think>reasoning</think>  The answer  ",
			want:  "The answer",
		},
		{
			name:  "tags only returns empty",
			input: "<think>all thinking no output</think>",
			want:  "",
		},
		{
			name:  "JSON after think tags",
			input: "<think>Let me generate JSON</think>{\"nodes\":[], \"edges\":[]}",
			want:  `{"nodes":[], "edges":[]}`,
		},
		{
			name:  "nested angle brackets in content",
			input: "<think>comparing <a> vs <b></think>Use option A.",
			want:  "Use option A.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripThinkingTokens(tt.input)
			if got != tt.want {
				t.Errorf("StripThinkingTokens(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
