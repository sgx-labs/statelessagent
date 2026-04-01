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
	CatCloudCred  Category = "cloud_credential"
	CatGitToken   Category = "git_token"
	CatPayment    Category = "payment_credential"
	CatComms      Category = "comms_credential"
	CatDevToken   Category = "dev_token"
	CatObserve    Category = "observability_credential"
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

// credentialPatterns returns compiled patterns for API keys, tokens, and secrets
// across major providers. These complement the builtinPatterns which cover
// general PII. High-confidence patterns only — each targets a specific,
// well-defined prefix or format to minimize false positives.
func credentialPatterns() []Pattern {
	return []Pattern{
		// AI API keys
		{Name: "anthropic_key", Tier: TierHard, Category: CatAPIKey,
			Regex: regexp.MustCompile(`\bsk-ant-[a-zA-Z0-9_\-]{20,}\b`)},
		{Name: "openai_proj_key", Tier: TierHard, Category: CatAPIKey,
			Regex: regexp.MustCompile(`\bsk-proj-[a-zA-Z0-9_\-]{20,}\b`)},
		{Name: "openai_svcacct_key", Tier: TierHard, Category: CatAPIKey,
			Regex: regexp.MustCompile(`\bsk-svcacct-[a-zA-Z0-9_\-]{20,}\b`)},
		{Name: "huggingface_token", Tier: TierHard, Category: CatAPIKey,
			Regex: regexp.MustCompile(`\bhf_[a-zA-Z0-9]{20,}\b`)},

		// Cloud provider credentials
		{Name: "gcp_api_key", Tier: TierHard, Category: CatCloudCred,
			Regex: regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`)},
		{Name: "digitalocean_pat", Tier: TierHard, Category: CatCloudCred,
			Regex: regexp.MustCompile(`\bdop_v1_[a-f0-9]{64}\b`)},
		{Name: "digitalocean_oauth", Tier: TierHard, Category: CatCloudCred,
			Regex: regexp.MustCompile(`\bdoo_v1_[a-f0-9]{64}\b`)},

		// Git platform tokens
		{Name: "github_pat_fine", Tier: TierHard, Category: CatGitToken,
			Regex: regexp.MustCompile(`\bgithub_pat_[a-zA-Z0-9_]{22,}\b`)},
		{Name: "gitlab_pat", Tier: TierHard, Category: CatGitToken,
			Regex: regexp.MustCompile(`\bglpat-[a-zA-Z0-9_\-]{20,}\b`)},
		{Name: "gitlab_deploy", Tier: TierHard, Category: CatGitToken,
			Regex: regexp.MustCompile(`\bgldt-[a-zA-Z0-9_\-]{20,}\b`)},

		// Communication platform tokens
		{Name: "slack_bot_token", Tier: TierHard, Category: CatComms,
			Regex: regexp.MustCompile(`\bxoxb-[0-9]{10,}-[a-zA-Z0-9\-]+\b`)},
		{Name: "slack_app_token", Tier: TierHard, Category: CatComms,
			Regex: regexp.MustCompile(`\bxapp-[0-9]-[a-zA-Z0-9\-]{20,}\b`)},
		{Name: "twilio_api_key", Tier: TierHard, Category: CatComms,
			Regex: regexp.MustCompile(`\bSK[a-f0-9]{32}\b`)},
		{Name: "sendgrid_key", Tier: TierHard, Category: CatComms,
			Regex: regexp.MustCompile(`\bSG\.[a-zA-Z0-9_\-]{22,}\.[a-zA-Z0-9_\-]{20,}\b`)},

		// Developer tokens
		{Name: "npm_token", Tier: TierHard, Category: CatDevToken,
			Regex: regexp.MustCompile(`\bnpm_[a-zA-Z0-9]{36,}\b`)},
		{Name: "pypi_token", Tier: TierHard, Category: CatDevToken,
			Regex: regexp.MustCompile(`\bpypi-[a-zA-Z0-9_\-]{50,}\b`)},
		{Name: "postman_key", Tier: TierHard, Category: CatDevToken,
			Regex: regexp.MustCompile(`\bPMAK-[a-f0-9]{24,}\b`)},
		{Name: "pulumi_token", Tier: TierHard, Category: CatDevToken,
			Regex: regexp.MustCompile(`\bpul-[a-f0-9]{40,}\b`)},

		// Observability
		{Name: "grafana_cloud_key", Tier: TierHard, Category: CatObserve,
			Regex: regexp.MustCompile(`\bglc_[a-zA-Z0-9_\-]{20,}\b`)},
		{Name: "grafana_sa_key", Tier: TierHard, Category: CatObserve,
			Regex: regexp.MustCompile(`\bglsa_[a-zA-Z0-9_\-]{20,}\b`)},
		{Name: "sentry_user_token", Tier: TierHard, Category: CatObserve,
			Regex: regexp.MustCompile(`\bsntryu_[a-f0-9]{64}\b`)},
		{Name: "sentry_system_token", Tier: TierHard, Category: CatObserve,
			Regex: regexp.MustCompile(`\bsntrys_[a-f0-9]{64}\b`)},

		// Payment
		{Name: "stripe_secret", Tier: TierHard, Category: CatPayment,
			Regex: regexp.MustCompile(`\bsk_(?:test|live)_[a-zA-Z0-9]{20,}\b`)},
		{Name: "stripe_restricted", Tier: TierHard, Category: CatPayment,
			Regex: regexp.MustCompile(`\brk_(?:test|live)_[a-zA-Z0-9]{20,}\b`)},
		{Name: "shopify_pat", Tier: TierHard, Category: CatPayment,
			Regex: regexp.MustCompile(`\bshpat_[a-f0-9]{32,}\b`)},
		{Name: "shopify_shared_secret", Tier: TierHard, Category: CatPayment,
			Regex: regexp.MustCompile(`\bshpss_[a-f0-9]{32,}\b`)},
	}
}

// AllPatterns returns the complete set of patterns — both PII and credential.
func AllPatterns() []Pattern {
	return append(builtinPatterns(), credentialPatterns()...)
}

// ScanContent checks text content against all patterns (PII + credentials).
// Returns results for each match found. Used by MCP save_note to warn on
// credential content.
func ScanContent(content string) []ScanLineResult {
	patterns := AllPatterns()
	var results []ScanLineResult
	for _, line := range strings.Split(content, "\n") {
		lineResults := scanLine(line, "", patterns)
		results = append(results, lineResults...)
	}
	return results
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
