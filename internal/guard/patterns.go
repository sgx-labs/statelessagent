// Package guard implements a pre-commit PII and content scanner.
package guard

import (
	"regexp"
	"strings"
)

// Tier indicates how strictly a violation is enforced.
type Tier string

const (
	TierHard Tier = "hard"
	TierSoft Tier = "soft"
)

// Category identifies the type of pattern that matched.
type Category string

const (
	CatEmail      Category = "pii_email"
	CatPhone      Category = "pii_phone"
	CatSSN        Category = "pii_ssn"
	CatLocalPath  Category = "pii_local_path"
	CatAPIKey     Category = "pii_api_key"
	CatAWSKey     Category = "pii_aws_key"
	CatPrivateKey Category = "pii_private_key"
	CatHardBlock  Category = "hard_blocklist"
	CatSoftBlock  Category = "soft_blocklist"
	CatPathBlock  Category = "path_violation"
)

// Pattern is a compiled PII detection pattern.
type Pattern struct {
	Name     string
	Tier     Tier
	Category Category
	Regex    *regexp.Regexp
}

// builtinPatterns returns the compiled set of PII regexes.
func builtinPatterns() []Pattern {
	return []Pattern{
		{
			Name:     "email",
			Tier:     TierHard,
			Category: CatEmail,
			Regex:    regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
		},
		{
			Name:     "us_phone",
			Tier:     TierHard,
			Category: CatPhone,
			Regex:    regexp.MustCompile(`(?:\+1[\s.-]?)?\(?\d{3}\)?[\s.-]?\d{3}[\s.-]?\d{4}`),
		},
		{
			Name:     "ssn",
			Tier:     TierHard,
			Category: CatSSN,
			Regex:    regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		},
		{
			Name:     "local_path_unix",
			Tier:     TierSoft,
			Category: CatLocalPath,
			Regex:    regexp.MustCompile(`/Users/[a-zA-Z][a-zA-Z0-9._-]*/`),
		},
		{
			Name:     "local_path_windows",
			Tier:     TierSoft,
			Category: CatLocalPath,
			Regex:    regexp.MustCompile(`[A-Z]:\\Users\\[a-zA-Z][a-zA-Z0-9._\- ]*\\`),
		},
		{
			Name:     "api_key_assignment",
			Tier:     TierHard,
			Category: CatAPIKey,
			Regex:    regexp.MustCompile(`(?i)(?:api[_-]?key|secret[_-]?key|access[_-]?token)\s*[:=]\s*["']?[a-zA-Z0-9_\-]{20,}["']?`),
		},
		{
			Name:     "sk_key",
			Tier:     TierHard,
			Category: CatAPIKey,
			Regex:    regexp.MustCompile(`\bsk-[a-zA-Z0-9]{20,}\b`),
		},
		{
			Name:     "aws_key",
			Tier:     TierHard,
			Category: CatAWSKey,
			Regex:    regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		},
		{
			Name:     "private_key_header",
			Tier:     TierHard,
			Category: CatPrivateKey,
			Regex:    regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`),
		},
		{
			Name:     "github_token",
			Tier:     TierHard,
			Category: CatAPIKey,
			Regex:    regexp.MustCompile(`\bgh[ps]_[a-zA-Z0-9]{36,}\b`),
		},
		{
			Name:     "slack_token",
			Tier:     TierHard,
			Category: CatAPIKey,
			Regex:    regexp.MustCompile(`\bxox[bpsar]-[a-zA-Z0-9\-]{10,}\b`),
		},
	}
}

// FilterByConfig returns only the patterns whose internal names appear in the enabled set.
// If enabled is nil, no patterns are returned (guard or PII disabled).
func FilterByConfig(patterns []Pattern, enabled map[string]bool) []Pattern {
	if enabled == nil {
		return nil
	}
	var out []Pattern
	for _, p := range patterns {
		if enabled[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// falsePositivePatterns are exact match patterns that are known false positives.
// Unlike the previous broad substring exclusions, these only suppress the specific
// match itself rather than the entire line, so real secrets on the same line are
// still detected.
var falsePositivePatterns = []string{
	"@example.com",
	"@example.org",
	"@example.net",
	"noreply@",
	"no-reply@",
	"xxx-xx-xxxx",
	"000-00-0000",
	"test@test.com",
}

// isFalsePositiveMatch checks whether a specific PII match is a known false positive.
// SECURITY: This checks the match itself, NOT the whole line. A real token on a line
// containing "test" will still be flagged — only the specific match is evaluated.
func isFalsePositiveMatch(match string) bool {
	lower := strings.ToLower(match)
	for _, fp := range falsePositivePatterns {
		if strings.Contains(lower, fp) {
			return true
		}
	}
	return false
}

// isExcludedFile checks whether a file should be skipped from PII scanning entirely.
func isExcludedFile(filePath string) bool {
	lowerPath := strings.ToLower(filePath)
	if strings.HasSuffix(lowerPath, "_test.go") || strings.Contains(lowerPath, "/test/") || strings.Contains(lowerPath, "/tests/") {
		return true
	}
	return false
}

// isRegexDefinitionLine checks whether a line is a regex pattern definition,
// which commonly contains PII-like strings as part of the pattern itself.
func isRegexDefinitionLine(line string) bool {
	return strings.Contains(line, "regexp.") || strings.Contains(line, "regexp.MustCompile")
}

// ScanLineResult holds a single match within a line.
type ScanLineResult struct {
	Pattern  Pattern
	Match    string
	Redacted string
}

// scanLine checks a single line against all PII patterns.
// Returns nil if the file is excluded or has no matches after false-positive filtering.
func scanLine(line string, filePath string, patterns []Pattern) []ScanLineResult {
	// Skip entire test files
	if isExcludedFile(filePath) {
		return nil
	}

	// Skip regex definition lines (contain pattern strings, not real PII)
	if isRegexDefinitionLine(line) {
		return nil
	}

	var results []ScanLineResult
	for _, p := range patterns {
		matches := p.Regex.FindAllString(line, -1)
		for _, m := range matches {
			// SECURITY: only suppress the specific match if it's a known false positive,
			// NOT the entire line
			if isFalsePositiveMatch(m) {
				continue
			}
			results = append(results, ScanLineResult{
				Pattern:  p,
				Match:    m,
				Redacted: redact(m),
			})
		}
	}
	return results
}

// redact partially obscures a matched string for safe display.
func redact(s string) string {
	if len(s) <= 6 {
		return strings.Repeat("*", len(s))
	}
	// Show first 3 and last 3, mask the middle
	runes := []rune(s)
	if len(runes) <= 6 {
		return strings.Repeat("*", len(runes))
	}
	prefix := string(runes[:3])
	suffix := string(runes[len(runes)-3:])
	middle := strings.Repeat("*", len(runes)-6)
	return prefix + middle + suffix
}
