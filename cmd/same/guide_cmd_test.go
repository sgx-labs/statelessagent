package main

import (
	"strings"
	"testing"
)

func TestGuideCmd_DefaultOutput(t *testing.T) {
	cmd := guideCmd()

	var runErr error
	out := captureCommandStdout(t, func() {
		cmd.SetArgs(nil)
		runErr = cmd.Execute()
	})
	if runErr != nil {
		t.Fatalf("guide command: %v", runErr)
	}

	for _, want := range []string{
		"## SAME Memory System",
		"CLAUDE.md",
		"trust_state",
		`same search "query"`,
		"same tips",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected guide output to contain %q, got: %s", want, out)
		}
	}
}

func TestGuideCmd_AgentOutput(t *testing.T) {
	cmd := guideCmd()

	var runErr error
	out := captureCommandStdout(t, func() {
		cmd.SetArgs([]string{"--agent"})
		runErr = cmd.Execute()
	})
	if runErr != nil {
		t.Fatalf("guide --agent command: %v", runErr)
	}

	for _, want := range []string{
		"Agent Prompt Template",
		`same search "<your task topic>"`,
		`same add "<decision>" --type decision --tags <tags>`,
		"Do NOT push to git without explicit approval",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected agent guide output to contain %q, got: %s", want, out)
		}
	}
}
