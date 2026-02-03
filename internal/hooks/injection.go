package hooks

import (
	"context"

	"github.com/mdombrov-33/go-promptguard/detector"
)

// promptGuard is the package-level detector instance. Initialized once at import
// time with all pattern-matching and statistical detectors enabled, no LLM judge.
// This keeps detection sub-millisecond since every call to sanitizeSnippet uses it.
var promptGuard = detector.New(
	detector.WithThreshold(0.6),       // stricter than default 0.7 — we're filtering vault content, not user input
	detector.WithAllDetectors(),       // role injection, prompt leak, instruction override, obfuscation, normalization, delimiter
	detector.WithMaxInputLength(1000), // snippets are capped at 300 chars, but be safe
	// No LLM judge — pattern + statistical analysis only for sub-ms latency
)

// detectInjection runs the go-promptguard multi-detector against text.
// Returns true if an injection attempt is detected (i.e. the input is NOT safe).
func detectInjection(text string) bool {
	if len(text) == 0 {
		return false
	}
	result := promptGuard.Detect(context.Background(), text)
	return !result.Safe
}
