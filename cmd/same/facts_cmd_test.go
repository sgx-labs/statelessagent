package main

import (
	"strings"
	"testing"

	"github.com/sgx-labs/statelessagent/internal/store"
)

func TestRunFactsShow_Empty(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runFactsShow()
	})
	if runErr != nil {
		t.Fatalf("runFactsShow: %v", runErr)
	}
	if !strings.Contains(out, "Total facts") {
		t.Fatalf("expected 'Total facts' header, got: %q", out)
	}
	if !strings.Contains(out, "No facts extracted yet") {
		t.Fatalf("expected 'No facts extracted yet' message, got: %q", out)
	}
}

func TestRunFactsShow_WithFacts(t *testing.T) {
	_, db := setupCommandTestVault(t)

	dim := 768
	makeVec := func(val float32) []float32 {
		v := make([]float32, dim)
		v[0] = val
		return v
	}

	// Insert a fact
	if err := db.InsertFact(&store.FactRecord{
		FactText:   "The project uses Go 1.25",
		SourcePath: "notes/setup.md",
		Confidence: 0.9,
	}, makeVec(0.5)); err != nil {
		t.Fatalf("InsertFact: %v", err)
	}
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runFactsShow()
	})
	if runErr != nil {
		t.Fatalf("runFactsShow: %v", runErr)
	}
	if !strings.Contains(out, "Total facts") {
		t.Fatalf("expected 'Total facts' header, got: %q", out)
	}
	if !strings.Contains(out, "The project uses Go 1.25") {
		t.Fatalf("expected fact text in output, got: %q", out)
	}
}

func TestRunFactsSearch_NoFacts(t *testing.T) {
	_, db := setupCommandTestVault(t)
	_ = db.Close()

	var runErr error
	out := captureCommandStdout(t, func() {
		runErr = runFactsSearch("test query", 5, false)
	})
	if runErr != nil {
		t.Fatalf("runFactsSearch: %v", runErr)
	}
	if !strings.Contains(out, "No facts extracted yet") {
		t.Fatalf("expected 'No facts extracted yet', got: %q", out)
	}
}
