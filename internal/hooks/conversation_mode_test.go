package hooks

import "testing"

func TestDetectMode_Executing(t *testing.T) {
	cases := []string{
		"fix the login bug",
		"implement the session state table",
		"add a new column to the schema",
		"build the binary and deploy",
		"refactor this function to use goroutines",
		"create a new file for the mode detection",
		"run the tests",
		"delete the old migration",
		"can you fix the CSS alignment",
	}
	for _, c := range cases {
		mode := detectMode(c)
		if mode != ModeExecuting {
			t.Errorf("expected Executing for %q, got %s", c, mode)
		}
	}
}

func TestDetectMode_Reflecting(t *testing.T) {
	cases := []string{
		"what are the risks of this approach",
		"what do you think about the architecture",
		"is this the right design philosophy",
		"what are the tradeoffs between push and pull",
		"should we evaluate alternatives before committing",
		"whats your recommendation as a senior dev",
		"compare the pros and cons",
		"is there a better strategy",
	}
	for _, c := range cases {
		mode := detectMode(c)
		if mode != ModeReflecting {
			t.Errorf("expected Reflecting for %q, got %s", c, mode)
		}
	}
}

func TestDetectMode_Exploring(t *testing.T) {
	cases := []string{
		"what is the vault structure",
		"how does SAME work",
		"where are the session files stored",
		"explain the embedding model",
		"tell me about the handoff system",
		"show me the recent decisions",
		"walk me through the architecture",
	}
	for _, c := range cases {
		mode := detectMode(c)
		if mode != ModeExploring {
			t.Errorf("expected Exploring for %q, got %s", c, mode)
		}
	}
}

func TestDetectMode_Deepening(t *testing.T) {
	cases := []string{
		"and the other one",
		"that too",
		"same for this",
		"yep do that",
	}
	for _, c := range cases {
		mode := detectMode(c)
		if mode != ModeDeepening {
			t.Errorf("expected Deepening for %q, got %s", c, mode)
		}
	}
}

func TestDetectMode_ExecutingDoesNotOverrideReflecting(t *testing.T) {
	// "what are the risks" should be reflecting even though "are" is present
	mode := detectMode("what are the risks and benefits of this approach")
	if mode != ModeReflecting {
		t.Errorf("expected Reflecting, got %s", mode)
	}
}

func TestDetectMode_MixedExploringExecuting(t *testing.T) {
	// "build" is imperative and starts the sentence
	mode := detectMode("build the session state table")
	if mode != ModeExecuting {
		t.Errorf("expected Executing for imperative start, got %s", mode)
	}
}
