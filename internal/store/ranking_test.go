package store

import (
	"testing"
)

func TestQueryWordsForTitleMatch(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{"what is kubernetes", []string{"kubernetes"}},
		{"Power Query SharePoint integration", []string{"Power", "Query", "SharePoint", "integration"}},
		{"SAME strengths weaknesses", []string{"SAME", "strengths", "weaknesses"}},
		{"AI experiments", []string{"AI", "experiments"}},
		{"", nil},
		{"the and for", nil}, // all stop words
	}
	for _, tt := range tests {
		got := QueryWordsForTitleMatch(tt.query)
		if len(got) != len(tt.want) {
			t.Errorf("QueryWordsForTitleMatch(%q) = %v, want %v", tt.query, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("QueryWordsForTitleMatch(%q)[%d] = %q, want %q", tt.query, i, got[i], tt.want[i])
			}
		}
	}
}

func TestTitleOverlapScore(t *testing.T) {
	tests := []struct {
		name   string
		terms  []string
		title  string
		path   string
		wantGT float64 // want overlap > this
		wantLT float64 // want overlap < this
	}{
		{
			name:   "exact match single term",
			terms:  []string{"kubernetes"},
			title:  "Kubernetes Hub",
			path:   "",
			wantGT: 0.40, // 1/1 * 1/2 = 0.5
			wantLT: 0.60,
		},
		{
			name:   "multi-term partial match",
			terms:  []string{"Power", "Query", "SharePoint", "integration"},
			title:  "Power Query M Code Reference: SharePoint Connectivity",
			path:   "",
			wantGT: 0.25, // 3/4 * 3/8 = 0.375
			wantLT: 0.40,
		},
		{
			name:   "no match",
			terms:  []string{"terraform", "infrastructure"},
			title:  "Project Notes Hub",
			path:   "",
			wantGT: -0.01,
			wantLT: 0.01,
		},
		{
			name:   "path adds matching words",
			terms:  []string{"SAME", "architecture"},
			title:  "00_brief",
			path:   "01_Projects/SAME v2 Architecture/00_brief.md",
			wantGT: 0.05,
			wantLT: 0.30,
		},
		{
			name:   "plural matching",
			terms:  []string{"project"},
			title:  "Projects Hub",
			path:   "",
			wantGT: 0.20, // "project" matches "projects" via plural
			wantLT: 0.60,
		},
		{
			name:   "edit distance matching",
			terms:  []string{"kubernetes"},
			title:  "Kubernetes Hub",
			path:   "",
			wantGT: 0.40, // edit distance 1 match
			wantLT: 0.60,
		},
		{
			name:   "stem matching",
			terms:  []string{"invoicing"},
			title:  "Invoice Automation",
			path:   "",
			wantGT: 0.10, // "invoicing" shares stem with "invoice"
			wantLT: 0.60,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TitleOverlapScore(tt.terms, tt.title, tt.path)
			if got <= tt.wantGT || got >= tt.wantLT {
				t.Errorf("TitleOverlapScore(%v, %q, %q) = %.4f, want (%.2f, %.2f)",
					tt.terms, tt.title, tt.path, got, tt.wantGT, tt.wantLT)
			}
		})
	}
}

func TestOverlapForSort(t *testing.T) {
	tests := []struct {
		name  string
		terms []string
		title string
		path  string
		want  float64 // exact expected
	}{
		{
			name:  "title-only overlap preferred",
			terms: []string{"SAME", "architecture"},
			title: "SAME v2 Architecture Decisions",
			path:  "01_Projects/SAME v2 Architecture/decisions.md",
			want:  -1, // positive, title-only is used
		},
		{
			name:  "zero overlap returns zero",
			terms: []string{"terraform"},
			title: "Project Notes Hub",
			path:  "02_Areas/Project Notes Hub.md",
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OverlapForSort(tt.terms, tt.title, tt.path)
			if tt.want == 0 && got != 0 {
				t.Errorf("OverlapForSort = %.4f, want 0", got)
			}
			if tt.want == -1 && got <= 0 {
				t.Errorf("OverlapForSort = %.4f, want > 0", got)
			}
		})
	}
}

func TestRankSearchResults(t *testing.T) {
	results := []SearchResult{
		{Path: "noise.md", Title: "Unrelated Note", Score: 1.0, ContentType: "note"},
		{Path: "exact.md", Title: "Power Query SharePoint Guide", Score: 0.6, ContentType: "note"},
		{Path: "hub.md", Title: "Random Hub", Score: 0.8, ContentType: "hub"},
	}
	queryTerms := []string{"Power", "Query", "SharePoint"}

	ranked := RankSearchResults(results, queryTerms)

	// The exact title match should be first despite lower Score
	if ranked[0].Path != "exact.md" {
		t.Errorf("expected exact.md first, got %s", ranked[0].Path)
	}

	// Hub should not outrank higher-scored result in low tier
	// (hub priority only applies in medium/high tiers)
	noiseIdx, hubIdx := -1, -1
	for i, r := range ranked {
		if r.Path == "noise.md" {
			noiseIdx = i
		}
		if r.Path == "hub.md" {
			hubIdx = i
		}
	}
	if noiseIdx >= 0 && hubIdx >= 0 && noiseIdx > hubIdx {
		// noise has Score 1.0, hub has Score 0.8 — in low tier, noise should rank above hub
		t.Errorf("expected noise.md (score 1.0) before hub.md (score 0.8) in low tier, got noise=%d hub=%d", noiseIdx, hubIdx)
	}
}

func TestRankSearchResults_NearDedup(t *testing.T) {
	results := []SearchResult{
		{Path: "01_Projects/Guide.md", Title: "Guide", Score: 0.9},
		{Path: "01_Projects/Guide v1.md", Title: "Guide v1", Score: 0.8},
		{Path: "other.md", Title: "Other", Score: 0.7},
	}
	queryTerms := []string{"Guide"}

	ranked := RankSearchResults(results, queryTerms)

	// Should deduplicate versioned files
	guideCount := 0
	for _, r := range ranked {
		if r.Path == "01_Projects/Guide.md" || r.Path == "01_Projects/Guide v1.md" {
			guideCount++
		}
	}
	if guideCount != 1 {
		t.Errorf("expected 1 Guide result after dedup, got %d", guideCount)
	}
}

func TestRankSearchResults_RawOutputsFilter(t *testing.T) {
	results := []SearchResult{
		{Path: "experiments/raw_outputs/TRIAL-001.md", Title: "Trial 001", Score: 0.9},
		{Path: "notes/real.md", Title: "Real Note", Score: 0.8},
	}

	ranked := RankSearchResults(results, []string{"trial"})

	for _, r := range ranked {
		if r.Path == "experiments/raw_outputs/TRIAL-001.md" {
			t.Error("raw_outputs file should have been filtered")
		}
	}
	if len(ranked) != 1 {
		t.Errorf("expected 1 result after filter, got %d", len(ranked))
	}
}

func TestRankingEditDistance1(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"kubernetes", "kuberntes", true},  // deletion
		{"kuberntes", "kubernetes", true},   // insertion
		{"embedding", "embeddinh", true},        // substitution
		{"short", "shorr", false},               // both < 7 chars
		{"totally", "different", false},          // too different
		{"same", "same", false},                  // identical
	}
	for _, tt := range tests {
		got := rankingEditDistance1(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("rankingEditDistance1(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestRankingSharesStem(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"invoice", "invoicing", true},
		{"finance", "financing", true},
		{"report", "reporting", true},
		{"cat", "cats", false},             // too short
		{"hello", "goodbye", false},        // no common prefix
		{"embedding", "embeddings", true},
	}
	for _, tt := range tests {
		got := rankingSharesStem(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("rankingSharesStem(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
