package guard

import (
	"testing"
)

func TestBuiltinPatterns_GitHubToken(t *testing.T) {
	patterns := builtinPatterns()
	var ghPattern Pattern
	for _, p := range patterns {
		if p.Name == "github_token" {
			ghPattern = p
			break
		}
	}
	if ghPattern.Regex == nil {
		t.Fatal("github_token pattern not found in builtinPatterns")
	}

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"ghp personal access token", "token: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijkl", true},
		{"ghs server token", "ghs_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijkl", true},
		{"too short", "ghp_ABC", false},
		{"not a token", "ghp is not a token", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ghPattern.Regex.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("github_token match(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuiltinPatterns_SlackToken(t *testing.T) {
	patterns := builtinPatterns()
	var slackPattern Pattern
	for _, p := range patterns {
		if p.Name == "slack_token" {
			slackPattern = p
			break
		}
	}
	if slackPattern.Regex == nil {
		t.Fatal("slack_token pattern not found in builtinPatterns")
	}

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"xoxb bot token", "xoxb-1234567890-abcdefghij", true},
		{"xoxp user token", "xoxp-1234567890-abcdefghij", true},
		{"xoxs session token", "xoxs-1234567890-abcdefghij", true},
		{"too short", "xoxb-abc", false},
		{"not a token", "xoxb is not a token", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slackPattern.Regex.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("slack_token match(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuiltinPatterns_ExistingPatterns(t *testing.T) {
	patterns := builtinPatterns()

	expectedNames := []string{
		"email", "us_phone", "ssn", "local_path_unix", "local_path_windows",
		"api_key_assignment", "sk_key", "aws_key", "private_key_header",
		"github_token", "slack_token",
	}

	nameSet := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		nameSet[p.Name] = true
	}

	for _, name := range expectedNames {
		if !nameSet[name] {
			t.Errorf("expected pattern %q not found in builtinPatterns", name)
		}
	}
}

func TestRedact_Security(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"short string fully masked", "abc", "***"},
		{"6 chars fully masked", "abcdef", "******"},
		{"7+ chars partial", "abcdefg", "abc*efg"},
		{"email redacted", "user@example.com", "use**********com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redact(tt.input)
			if got != tt.want {
				t.Errorf("redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsExcludedFile_TestFiles(t *testing.T) {
	if !isExcludedFile("internal/guard/guard_test.go") {
		t.Error("expected test files to be excluded")
	}
	if !isExcludedFile("internal/tests/integration/foo.go") {
		t.Error("expected /tests/ directory to be excluded")
	}
	if isExcludedFile("internal/guard/guard.go") {
		t.Error("non-test file should not be excluded")
	}
}

func TestIsFalsePositiveMatch(t *testing.T) {
	falsePositives := []string{
		"test@example.com",
		"user@example.com",
		"noreply@company.com",
		"no-reply@service.io",
		"xxx-xx-xxxx",
		"000-00-0000",
		"test@test.com",
	}

	for _, m := range falsePositives {
		if !isFalsePositiveMatch(m) {
			t.Errorf("expected match %q to be a false positive", m)
		}
	}

	// Real secrets should NOT be false positives
	realSecrets := []string{
		"sk-ant-abc123456789012345678",
		"AKIAIOSFODNN7EXAMPLE",
		"user@realcompany.com",
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijkl",
	}

	for _, m := range realSecrets {
		if isFalsePositiveMatch(m) {
			t.Errorf("real secret %q should NOT be a false positive", m)
		}
	}
}

func TestScanLine_RealSecretNotHiddenByTestKeyword(t *testing.T) {
	// SECURITY: A real API key on a line containing "test" must still be flagged.
	// This was the vulnerability in the old line-level exclusion approach.
	patterns := builtinPatterns()
	line := "sk-ant-abc12345678901234567890 // test value"
	results := scanLine(line, "real_file.go", patterns)
	if len(results) == 0 {
		t.Error("expected real sk- key to be detected even on a line with 'test'")
	}
}

func TestScanLine_ExampleEmailSuppressed(t *testing.T) {
	patterns := builtinPatterns()
	line := "contact: user@example.com"
	results := scanLine(line, "real_file.go", patterns)
	if len(results) != 0 {
		t.Error("expected example.com email to be suppressed as false positive")
	}
}

func TestCredentialPatterns_Detection(t *testing.T) {
	creds := credentialPatterns()
	patternMap := make(map[string]Pattern)
	for _, p := range creds {
		patternMap[p.Name] = p
	}

	tests := []struct {
		patternName string
		input       string
		shouldMatch bool
	}{
		// AI APIs
		{"anthropic_key", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz", true},
		{"openai_proj_key", "sk-proj-abcdefghij1234567890ABCD", true},
		{"openai_svcacct_key", "sk-svcacct-abcdefghij1234567890", true},
		{"huggingface_token", "hf_ABCDefghijklmnopqrstuvwx", true},
		{"huggingface_token", "hf_ab", false}, // too short

		// Cloud
		{"gcp_api_key", "AIzaSyAaBbCcDdEeFfGgHhIiJjKkLlMmNnOoPp1", true},
		{"digitalocean_pat", "dop_v1_" + repeat64('a'), true},
		{"digitalocean_oauth", "doo_v1_" + repeat64('b'), true},

		// Git
		{"github_pat_fine", "github_pat_11AAAAAAA0abcdefghijklmnopqrstuvwxyz", true},
		{"gitlab_pat", "glpat-abcdefghijklmnopqrstuv", true},
		{"gitlab_deploy", "gldt-abcdefghijklmnopqrstuv", true},

		// Communications
		{"slack_bot_token", "xoxb-1234567890-abcdefghij", true},
		{"slack_app_token", "xapp-1-ABCDEFGHIJKLMNOPQRSTUV", true},
		{"twilio_api_key", "SK" + repeat32('a'), true},
		{"sendgrid_key", "SG.abcdefghijklmnopqrstuv.ABCDEFGHIJKLMNOPQRSTUV", true},

		// Dev tokens
		{"npm_token", "npm_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890", true},
		{"pypi_token", "pypi-" + repeatN('A', 55), true},
		{"postman_key", "PMAK-abcdef1234567890abcdef12", true},
		{"pulumi_token", "pul-" + repeatN('a', 45), true},

		// Observability
		{"grafana_cloud_key", "glc_ABCDEFGHIJKLMNOPQRSTUVab", true},
		{"grafana_sa_key", "glsa_ABCDEFGHIJKLMNOPQRSTUVab", true},
		{"sentry_user_token", "sntryu_" + repeat64('a'), true},
		{"sentry_system_token", "sntrys_" + repeat64('b'), true},

		// Payment
		{"stripe_secret", "sk_test_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", true},
		{"stripe_secret", "sk_live_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", true},
		{"stripe_restricted", "rk_test_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", true},
		{"shopify_pat", "shpat_" + repeat32('a'), true},
		{"shopify_shared_secret", "shpss_" + repeat32('b'), true},

		// Should NOT match
		{"anthropic_key", "sk-regular-key-not-ant", false},
		{"stripe_secret", "sk_staging_key", false},
	}

	for _, tt := range tests {
		t.Run(tt.patternName+"_"+tt.input[:min(20, len(tt.input))], func(t *testing.T) {
			p, ok := patternMap[tt.patternName]
			if !ok {
				t.Fatalf("pattern %q not found in credentialPatterns", tt.patternName)
			}
			matched := p.Regex.MatchString(tt.input)
			if matched != tt.shouldMatch {
				t.Errorf("pattern %q on %q: got match=%v, want %v", tt.patternName, tt.input, matched, tt.shouldMatch)
			}
		})
	}
}

func TestAllPatterns_IncludesCredentials(t *testing.T) {
	all := AllPatterns()
	builtin := builtinPatterns()
	cred := credentialPatterns()

	if len(all) != len(builtin)+len(cred) {
		t.Errorf("AllPatterns() count %d != builtin %d + cred %d", len(all), len(builtin), len(cred))
	}
}

func TestScanContent_DetectsCredentials(t *testing.T) {
	content := "Here is my API key: sk-ant-api03-abcdefghijklmnopqrstuvwxyz\nAnd a Stripe key: sk_test_ABCDEFGHIJKLMNOPQRSTUVWXYZab"
	results := ScanContent(content)
	if len(results) < 2 {
		t.Errorf("expected at least 2 credential detections, got %d", len(results))
	}
}

func TestScanContent_NoFalsePositives(t *testing.T) {
	content := "This is normal text.\nNo secrets here.\nJust regular documentation."
	results := ScanContent(content)
	if len(results) != 0 {
		t.Errorf("expected 0 matches for clean content, got %d", len(results))
	}
}

// Helper to generate repeated character strings for test patterns
func repeat32(c byte) string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func repeat64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func repeatN(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
