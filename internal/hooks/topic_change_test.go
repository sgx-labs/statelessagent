package hooks

import (
	"strings"
	"testing"
)

func TestJaccardSimilarity_Identical(t *testing.T) {
	score := jaccardSimilarity([]string{"a", "b", "c"}, []string{"a", "b", "c"})
	if score != 1.0 {
		t.Errorf("expected 1.0 for identical sets, got %f", score)
	}
}

func TestJaccardSimilarity_Disjoint(t *testing.T) {
	score := jaccardSimilarity([]string{"a", "b"}, []string{"c", "d"})
	if score != 0.0 {
		t.Errorf("expected 0.0 for disjoint sets, got %f", score)
	}
}

func TestJaccardSimilarity_Partial(t *testing.T) {
	// {a, b, c} ∩ {b, c, d} = {b, c} → 2/4 = 0.5
	score := jaccardSimilarity([]string{"a", "b", "c"}, []string{"b", "c", "d"})
	if score != 0.5 {
		t.Errorf("expected 0.5, got %f", score)
	}
}

func TestJaccardSimilarity_BothEmpty(t *testing.T) {
	score := jaccardSimilarity(nil, nil)
	if score != 1.0 {
		t.Errorf("expected 1.0 for both empty, got %f", score)
	}
}

func TestJaccardSimilarity_OneEmpty(t *testing.T) {
	score := jaccardSimilarity([]string{"a"}, nil)
	if score != 0.0 {
		t.Errorf("expected 0.0 when one is empty, got %f", score)
	}
}

func TestCollectTopicTerms_CombinesSpecificAndBroad(t *testing.T) {
	keyTermsPrompt = "explain the SAME validation results"
	terms := collectTopicTerms()
	// Should have at least "SAME" (specific/acronym) and "validation", "results" (broad)
	if len(terms) < 2 {
		t.Errorf("expected at least 2 terms, got %d: %v", len(terms), terms)
	}
	// Check dedup (lowercase)
	seen := make(map[string]bool)
	for _, term := range terms {
		if seen[term] {
			t.Errorf("duplicate term: %s", term)
		}
		seen[term] = true
	}
}

func TestCollectTopicTerms_LowercaseNormalized(t *testing.T) {
	keyTermsPrompt = "check SAME and SWOT"
	terms := collectTopicTerms()
	for _, term := range terms {
		if term != strings.ToLower(term) {
			t.Errorf("expected lowercase, got %q", term)
		}
	}
}
