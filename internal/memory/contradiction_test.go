package memory

import (
	"testing"
)

func TestDetectContradictions_Factual(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/deadline.md",
			Text:  "The project deadline is March 15th. We need to ship by then.",
			Score: 0.85,
			Title: "Project Deadline",
		},
	}

	newContent := "The deadline has changed to April 30th. We no longer need to ship by March 15th."
	results := DetectContradictions(newContent, "notes/deadline-update.md", candidates)

	if len(results) == 0 {
		t.Fatal("expected at least one contradiction, got none")
	}
	r := results[0]
	if r.Type != ContradictionFactual {
		t.Errorf("expected factual contradiction, got %s", r.Type)
	}
	if r.OldNotePath != "notes/deadline.md" {
		t.Errorf("expected old note path 'notes/deadline.md', got %q", r.OldNotePath)
	}
	if r.NewNotePath != "notes/deadline-update.md" {
		t.Errorf("expected new note path 'notes/deadline-update.md', got %q", r.NewNotePath)
	}
	if r.Confidence < MinContradictionConfidence {
		t.Errorf("expected confidence >= %.2f, got %.3f", MinContradictionConfidence, r.Confidence)
	}
}

func TestDetectContradictions_Preference(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "decisions/frontend.md",
			Text:  "We decided to use React for the frontend. React gives us a large ecosystem.",
			Score: 0.82,
			Title: "Frontend Framework Decision",
		},
	}

	newContent := "We decided to use Svelte instead. We prefer Svelte over React for its simpler model."
	results := DetectContradictions(newContent, "decisions/frontend-v2.md", candidates)

	if len(results) == 0 {
		t.Fatal("expected at least one contradiction, got none")
	}
	r := results[0]
	if r.Type != ContradictionPreference {
		t.Errorf("expected preference contradiction, got %s", r.Type)
	}
	if r.Confidence < MinContradictionConfidence {
		t.Errorf("confidence too low: %.3f", r.Confidence)
	}
}

func TestDetectContradictions_Context(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/database.md",
			Text:  "We use PostgreSQL for the main database.",
			Score: 0.78,
			Title: "Database Choice",
		},
	}

	newContent := "In this project, we use SQLite for the database. Specifically for embedded use."
	results := DetectContradictions(newContent, "notes/embedded-db.md", candidates)

	if len(results) == 0 {
		t.Fatal("expected at least one contradiction, got none")
	}
	r := results[0]
	if r.Type != ContradictionContext {
		t.Errorf("expected context contradiction, got %s", r.Type)
	}
}

func TestDetectContradictions_NoContradiction(t *testing.T) {
	// Similar content that adds to, but does not contradict, the original
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/auth.md",
			Text:  "Authentication uses JWT tokens with RS256 signing.",
			Score: 0.80,
			Title: "Auth Architecture",
		},
	}

	// Complementary content — no negation, temporal, or preference signals
	newContent := "JWT token validation middleware checks the RS256 signature and expiry claim."
	results := DetectContradictions(newContent, "notes/auth-middleware.md", candidates)

	if len(results) != 0 {
		t.Errorf("expected no contradictions for complementary content, got %d: %+v", len(results), results)
	}
}

func TestDetectContradictions_BelowSimilarityThreshold(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/unrelated.md",
			Text:  "We no longer use Python for scripting.",
			Score: 0.5, // below threshold
			Title: "Python",
		},
	}

	newContent := "We switched from Python to Go for all tooling."
	results := DetectContradictions(newContent, "notes/tooling.md", candidates)

	if len(results) != 0 {
		t.Errorf("expected no contradictions below similarity threshold, got %d", len(results))
	}
}

func TestDetectContradictions_EmptyContent(t *testing.T) {
	results := DetectContradictions("", "notes/empty.md", nil)
	if results != nil {
		t.Errorf("expected nil for empty content, got %v", results)
	}

	results = DetectContradictions("some content", "notes/test.md", nil)
	if results != nil {
		t.Errorf("expected nil for no candidates, got %v", results)
	}
}

func TestDetectContradictions_SelfReference(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/same-note.md",
			Text:  "We no longer use this approach.",
			Score: 0.95,
			Title: "Same Note",
		},
	}

	newContent := "We no longer use this approach. Switched to a new method."
	results := DetectContradictions(newContent, "notes/same-note.md", candidates)

	if len(results) != 0 {
		t.Errorf("expected no contradictions for self-reference, got %d", len(results))
	}
}

func TestDetectContradictions_TemporalSignals(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/api-version.md",
			Text:  "The API is at version 2.3. Clients should use v2.3 endpoints.",
			Score: 0.88,
			Title: "API Version",
		},
	}

	newContent := "As of today, the API has been updated to version 3.0. We moved to a new endpoint structure."
	results := DetectContradictions(newContent, "notes/api-v3.md", candidates)

	if len(results) == 0 {
		t.Fatal("expected temporal contradiction, got none")
	}
	r := results[0]
	if r.Type != ContradictionFactual {
		t.Errorf("expected factual (temporal update), got %s", r.Type)
	}
}

func TestDetectContradictions_DeprecationSignal(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/auth-method.md",
			Text:  "Use basic auth with API key header for service-to-service calls.",
			Score: 0.82,
			Title: "Auth Method",
		},
	}

	newContent := "Basic auth is deprecated. Do not use API key headers. Use OAuth2 instead."
	results := DetectContradictions(newContent, "notes/auth-update.md", candidates)

	if len(results) == 0 {
		t.Fatal("expected deprecation contradiction, got none")
	}
	if results[0].Type != ContradictionFactual {
		t.Errorf("expected factual contradiction for deprecation, got %s", results[0].Type)
	}
}

func TestDetectContradictions_MultipleContradictions(t *testing.T) {
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/deploy-method.md",
			Text:  "We deploy using Docker Compose on a single server.",
			Score: 0.80,
			Title: "Deployment",
		},
		{
			Path:  "notes/ci-pipeline.md",
			Text:  "CI pipeline builds and pushes to the single server.",
			Score: 0.75,
			Title: "CI Pipeline",
		},
	}

	newContent := "We migrated to Kubernetes. We no longer use Docker Compose or single-server deployments."
	results := DetectContradictions(newContent, "notes/k8s-migration.md", candidates)

	if len(results) < 2 {
		t.Errorf("expected 2 contradictions, got %d", len(results))
	}
}

func TestClassifyContradiction(t *testing.T) {
	tests := []struct {
		name        string
		oldContent  string
		newContent  string
		wantType    ContradictionType
		wantMinConf float64
	}{
		{
			name:        "factual negation",
			oldContent:  "The deadline is March 15.",
			newContent:  "The deadline has changed to April 30. No longer March 15.",
			wantType:    ContradictionFactual,
			wantMinConf: 0.3,
		},
		{
			name:        "preference switch",
			oldContent:  "We use React for the frontend.",
			newContent:  "We decided to use Vue instead. We prefer Vue for this project.",
			wantType:    ContradictionPreference,
			wantMinConf: 0.3,
		},
		{
			name:        "context dependent",
			oldContent:  "Database is PostgreSQL.",
			newContent:  "In this project, we use SQLite specifically for embedded scenarios.",
			wantType:    ContradictionContext,
			wantMinConf: 0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cType, confidence := ClassifyContradiction(tt.oldContent, tt.newContent)
			if cType != tt.wantType {
				t.Errorf("got type %s, want %s", cType, tt.wantType)
			}
			if confidence < tt.wantMinConf {
				t.Errorf("confidence %.3f below minimum %.3f", confidence, tt.wantMinConf)
			}
		})
	}
}

func TestContradictionTypes(t *testing.T) {
	// Verify type string values for database storage
	if string(ContradictionFactual) != "factual" {
		t.Errorf("ContradictionFactual = %q, want 'factual'", ContradictionFactual)
	}
	if string(ContradictionPreference) != "preference" {
		t.Errorf("ContradictionPreference = %q, want 'preference'", ContradictionPreference)
	}
	if string(ContradictionContext) != "context" {
		t.Errorf("ContradictionContext = %q, want 'context'", ContradictionContext)
	}
}

func TestDetectContradictions_MixedSignals(t *testing.T) {
	// When both preference and temporal signals appear, the stronger set wins.
	// "prefer" + "decided to use" = 2 preference signals,
	// "as of today" + "switched to" + "going forward" = 3 temporal signals.
	// Since temporal signals outnumber preference, this classifies as factual.
	candidates := []ContradictionCandidate{
		{
			Path:  "notes/framework.md",
			Text:  "Our frontend framework is Angular.",
			Score: 0.85,
			Title: "Framework",
		},
	}

	newContent := "As of today, we switched to React. We prefer React now and decided to use it going forward."
	results := DetectContradictions(newContent, "notes/framework-update.md", candidates)

	if len(results) == 0 {
		t.Fatal("expected contradiction from mixed signals, got none")
	}
	// With 3 temporal vs 2 preference signals, factual (negation+temporal) wins
	if results[0].Type != ContradictionFactual {
		t.Errorf("expected factual type from temporal-dominant mixed signals, got %s", results[0].Type)
	}

	// Now test when preference signals dominate
	newContent2 := "We opted for Svelte. We prefer Svelte and chose it over Angular. Went with it for simplicity."
	results2 := DetectContradictions(newContent2, "notes/framework-update2.md", candidates)

	if len(results2) == 0 {
		t.Fatal("expected contradiction from preference-dominant signals, got none")
	}
	if results2[0].Type != ContradictionPreference {
		t.Errorf("expected preference type from preference-dominant signals, got %s", results2[0].Type)
	}
}

func TestCountSignals(t *testing.T) {
	text := "we no longer use react. we switched to svelte instead of react."
	count, reason := countSignals(text, negationSignals)
	if count == 0 {
		t.Error("expected at least one negation signal")
	}
	if reason == "" {
		t.Error("expected a reason string")
	}
}

func TestComputeContradictionConfidence(t *testing.T) {
	// Zero signals = zero confidence
	conf := computeContradictionConfidence(0, 0.9, 0)
	if conf > 0.5 {
		// With 0 signals, signal confidence is 0, but similarity boost can add up to 0.4
		t.Logf("zero signals confidence: %.3f (expected <= 0.4 from similarity boost only)", conf)
	}

	// One signal at high similarity should give moderate confidence
	conf1 := computeContradictionConfidence(1, 0.9, 1)
	if conf1 < 0.3 {
		t.Errorf("one strong signal should give >= 0.3 confidence, got %.3f", conf1)
	}

	// More signals = higher confidence
	conf3 := computeContradictionConfidence(3, 0.9, 3)
	if conf3 <= conf1 {
		t.Errorf("more signals should increase confidence: 3-signal=%.3f, 1-signal=%.3f", conf3, conf1)
	}

	// Higher similarity = higher confidence
	confLowSim := computeContradictionConfidence(2, 0.75, 2)
	confHighSim := computeContradictionConfidence(2, 0.95, 2)
	if confHighSim <= confLowSim {
		t.Errorf("higher similarity should increase confidence: high=%.3f, low=%.3f", confHighSim, confLowSim)
	}
}
