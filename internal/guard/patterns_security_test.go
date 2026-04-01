package guard

import (
	"strings"
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

func TestCredentialPatterns_NoFalsePositives(t *testing.T) {
	creds := credentialPatterns()
	patternMap := make(map[string]Pattern)
	for _, p := range creds {
		patternMap[p.Name] = p
	}

	tests := []struct {
		patternName string
		name        string
		input       string
	}{
		// AI APIs
		{"anthropic_key", "doc_placeholder", "sk-ant-<your-anthropic-key>"},
		{"anthropic_key", "truncated_token", "sk-ant-short"},
		{"anthropic_key", "mixed_case_prefix", "Sk-ant-abcdefghijklmnopqrstuvwxyz1234"},
		{"anthropic_key", "ordinary_identifier", "sk-ant-client-id"},
		{"openai_proj_key", "doc_placeholder", "sk-proj-<project-key>"},
		{"openai_proj_key", "truncated_token", "sk-proj-short"},
		{"openai_proj_key", "mixed_case_prefix", "sk-Proj-abcdefghij1234567890"},
		{"openai_proj_key", "ordinary_identifier", "sk-proj-cache-key"},
		{"openai_svcacct_key", "doc_placeholder", "sk-svcacct-<svc-account-key>"},
		{"openai_svcacct_key", "truncated_token", "sk-svcacct-short"},
		{"openai_svcacct_key", "mixed_case_prefix", "sk-Svcacct-abcdefghij1234567890"},
		{"openai_svcacct_key", "ordinary_identifier", "sk-svcacct-client-id"},
		{"huggingface_token", "doc_placeholder", "hf_<token>"},
		{"huggingface_token", "truncated_token", "hf_abc123"},
		{"huggingface_token", "mixed_case_prefix", "Hf_ABCDEFGHIJKLMNOPQRSTUVWXYZ12"},
		{"huggingface_token", "ordinary_identifier", "hf_model_id"},

		// Cloud credentials
		{"gcp_api_key", "doc_placeholder", "AIza<api-key>"},
		{"gcp_api_key", "truncated_token", "AIzaSyShortKey123"},
		{"gcp_api_key", "mixed_case_prefix", "AIzAabcdefghijklmnopqrstuvwxyzABCDE12345"},
		{"gcp_api_key", "ordinary_identifier", "AIzaConfigValue"},
		{"digitalocean_pat", "doc_placeholder", "dop_v1_<token>"},
		{"digitalocean_pat", "truncated_token", "dop_v1_deadbeef"},
		{"digitalocean_pat", "mixed_case_id", "dop_v1_" + repeat32('A') + repeat32('a')},
		{"digitalocean_pat", "ordinary_identifier", "dop_v1_client_id"},
		{"digitalocean_oauth", "doc_placeholder", "doo_v1_<token>"},
		{"digitalocean_oauth", "truncated_token", "doo_v1_deadbeef"},
		{"digitalocean_oauth", "mixed_case_id", "doo_v1_" + repeat32('B') + repeat32('b')},
		{"digitalocean_oauth", "ordinary_identifier", "doo_v1_oauth_id"},

		// Git platform tokens
		{"github_pat_fine", "doc_placeholder", "github_pat_<token>"},
		{"github_pat_fine", "truncated_token", "github_pat_short_id"},
		{"github_pat_fine", "mixed_case_prefix", "GitHub_pat_11AAAAAAA0abcdefghijklmnopqrstuvwxyz"},
		{"github_pat_fine", "ordinary_identifier", "github_pat_template_id"},
		{"gitlab_pat", "doc_placeholder", "glpat-<token>"},
		{"gitlab_pat", "truncated_token", "glpat-short"},
		{"gitlab_pat", "mixed_case_prefix", "Glpat-abcdefghijklmnopqrstuv"},
		{"gitlab_pat", "ordinary_identifier", "glpat-cache-key"},
		{"gitlab_deploy", "doc_placeholder", "gldt-<deploy-token>"},
		{"gitlab_deploy", "truncated_token", "gldt-short"},
		{"gitlab_deploy", "mixed_case_prefix", "Gldt-abcdefghijklmnopqrstuv"},
		{"gitlab_deploy", "ordinary_identifier", "gldt-runner-id"},

		// Communication platform tokens
		{"slack_bot_token", "doc_placeholder", "xoxb-<workspace-id>-<bot-token>"},
		{"slack_bot_token", "truncated_token", "xoxb-1234-abcd"},
		{"slack_bot_token", "mixed_case_prefix", "Xoxb-1234567890-abcdefghij"},
		{"slack_bot_token", "ordinary_identifier", "xoxb-1234-bot-token"},
		{"slack_app_token", "doc_placeholder", "xapp-<env>-<app-token>"},
		{"slack_app_token", "truncated_token", "xapp-1-short"},
		{"slack_app_token", "mixed_case_prefix", "Xapp-1-ABCDEFGHIJKLMNOPQRSTUV"},
		{"slack_app_token", "ordinary_identifier", "xapp-1-app-token-id"},
		{"twilio_api_key", "doc_placeholder", "SK<twilio-key>"},
		{"twilio_api_key", "truncated_token", "SKabc123"},
		{"twilio_api_key", "mixed_case_id", "SK" + repeat16('A') + repeat16('a')},
		{"twilio_api_key", "ordinary_identifier", "SKCLIENTTOKEN"},
		{"sendgrid_key", "doc_placeholder", "SG.<api-key-id>.<secret>"},
		{"sendgrid_key", "truncated_token", "SG.short.segment"},
		{"sendgrid_key", "mixed_case_prefix", "Sg.abcdefghijklmnopqrstuv.ABCDEFGHIJKLMNOPQRSTUV"},
		{"sendgrid_key", "ordinary_identifier", "SG.template.id"},

		// Developer tokens
		{"npm_token", "doc_placeholder", "npm_<token>"},
		{"npm_token", "truncated_token", "npm_short"},
		{"npm_token", "mixed_case_prefix", "Npm_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"},
		{"npm_token", "ordinary_identifier", "npm_package_id"},
		{"pypi_token", "doc_placeholder", "pypi-<upload-token>"},
		{"pypi_token", "truncated_token", "pypi-short"},
		{"pypi_token", "mixed_case_prefix", "PyPI-" + repeatN('a', 55)},
		{"pypi_token", "ordinary_identifier", "pypi-package-name"},
		{"postman_key", "doc_placeholder", "PMAK-<api-key>"},
		{"postman_key", "truncated_token", "PMAK-abc123"},
		{"postman_key", "mixed_case_id", "PMAK-ABCDEFabcdef1234567890"},
		{"postman_key", "ordinary_identifier", "PMAK-client-id"},
		{"pulumi_token", "doc_placeholder", "pul-<access-token>"},
		{"pulumi_token", "truncated_token", "pul-abc123"},
		{"pulumi_token", "mixed_case_id", "pul-" + repeat20('A') + repeat20('a')},
		{"pulumi_token", "ordinary_identifier", "pul-policy-id"},

		// Observability
		{"grafana_cloud_key", "doc_placeholder", "glc_<cloud-key>"},
		{"grafana_cloud_key", "truncated_token", "glc_short"},
		{"grafana_cloud_key", "mixed_case_prefix", "Glc_ABCDEFGHIJKLMNOPQRSTUVab"},
		{"grafana_cloud_key", "ordinary_identifier", "glc_dashboard_uid"},
		{"grafana_sa_key", "doc_placeholder", "glsa_<service-account-key>"},
		{"grafana_sa_key", "truncated_token", "glsa_short"},
		{"grafana_sa_key", "mixed_case_prefix", "Glsa_ABCDEFGHIJKLMNOPQRSTUVab"},
		{"grafana_sa_key", "ordinary_identifier", "glsa_team_id"},
		{"sentry_user_token", "doc_placeholder", "sntryu_<user-token>"},
		{"sentry_user_token", "truncated_token", "sntryu_deadbeef"},
		{"sentry_user_token", "mixed_case_id", "sntryu_" + repeat32('A') + repeat32('a')},
		{"sentry_user_token", "ordinary_identifier", "sntryu_session_id"},
		{"sentry_system_token", "doc_placeholder", "sntrys_<system-token>"},
		{"sentry_system_token", "truncated_token", "sntrys_deadbeef"},
		{"sentry_system_token", "mixed_case_id", "sntrys_" + repeat32('B') + repeat32('b')},
		{"sentry_system_token", "ordinary_identifier", "sntrys_service_id"},

		// Payment
		{"stripe_secret", "doc_placeholder", "sk_test_<secret-key>"},
		{"stripe_secret", "truncated_token", "sk_test_short"},
		{"stripe_secret", "mixed_case_prefix", "Sk_test_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"},
		{"stripe_secret", "ordinary_identifier", "sk_test_publishable"},
		{"stripe_restricted", "doc_placeholder", "rk_live_<restricted-key>"},
		{"stripe_restricted", "truncated_token", "rk_live_short"},
		{"stripe_restricted", "mixed_case_prefix", "Rk_live_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"},
		{"stripe_restricted", "ordinary_identifier", "rk_live_publishable"},
		{"shopify_pat", "doc_placeholder", "shpat_<token>"},
		{"shopify_pat", "truncated_token", "shpat_deadbeef"},
		{"shopify_pat", "mixed_case_id", "shpat_" + repeat16('A') + repeat16('a')},
		{"shopify_pat", "ordinary_identifier", "shpat_order_id"},
		{"shopify_shared_secret", "doc_placeholder", "shpss_<shared-secret>"},
		{"shopify_shared_secret", "truncated_token", "shpss_deadbeef"},
		{"shopify_shared_secret", "mixed_case_id", "shpss_" + repeat16('B') + repeat16('b')},
		{"shopify_shared_secret", "ordinary_identifier", "shpss_webhook_id"},
	}

	for _, tt := range tests {
		t.Run(tt.patternName+"_"+tt.name, func(t *testing.T) {
			p, ok := patternMap[tt.patternName]
			if !ok {
				t.Fatalf("pattern %q not found in credentialPatterns", tt.patternName)
			}
			if p.Regex.MatchString(tt.input) {
				t.Errorf("pattern %q should not match %q", tt.patternName, tt.input)
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

func TestScanContent_CredentialLookalikesDoNotMatch(t *testing.T) {
	content := strings.Join([]string{
		"docs: export ANTHROPIC_API_KEY=sk-ant-<your-anthropic-key>",
		"example: OPENAI_PROJECT_KEY=sk-proj-<project-key>",
		"placeholder: sk-svcacct-<svc-account-key>",
		"model id: hf_model_id",
		"docs: AIza<api-key>",
		"config key: dop_v1_client_id",
		"oauth id: doo_v1_oauth_id",
		"template: github_pat_template_id",
		"gitlab cache key: glpat-cache-key",
		"runner label: gldt-runner-id",
		"docs: xoxb-<workspace-id>-<bot-token>",
		"identifier: xapp-1-app-token-id",
		"sample key: SK<twilio-key>",
		"docs: SG.template.id",
		"package var: npm_package_id",
		"package label: pypi-package-name",
		"identifier: PMAK-client-id",
		"policy ref: pul-policy-id",
		"dashboard ref: glc_dashboard_uid",
		"team ref: glsa_team_id",
		"session ref: sntryu_session_id",
		"service ref: sntrys_service_id",
		"docs: sk_test_<secret-key>",
		"ordinary id: rk_live_publishable",
		"order id: shpat_order_id",
		"webhook id: shpss_webhook_id",
	}, "\n")

	results := ScanContent(content)
	if len(results) != 0 {
		t.Errorf("expected 0 matches for credential lookalikes, got %d", len(results))
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

func repeat16(c byte) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func repeat20(c byte) string {
	b := make([]byte, 20)
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
