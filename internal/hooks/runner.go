// Package hooks implements Claude Code lifecycle hook handlers.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/store"
)

// HookInput is the JSON input from Claude Code hooks.
type HookInput struct {
	Prompt          string `json:"prompt,omitempty"`
	TranscriptPath  string `json:"transcript_path,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	HookEventName   string `json:"hookEventName,omitempty"`
}

// HookOutput is the JSON output for Claude Code hooks.
type HookOutput struct {
	HookSpecificOutput *HookSpecific `json:"hookSpecificOutput,omitempty"`
}

// HookSpecific contains the hook event and context.
type HookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// hookEventMap maps CLI hook names to Claude Code event names.
var hookEventMap = map[string]string{
	"context-surfacing":  "UserPromptSubmit",
	"decision-extractor": "Stop",
	"handoff-generator":  "Stop",
	"staleness-check":    "SessionStart",
	"session-bootstrap":  "SessionStart",
}

// Run reads stdin, dispatches to the named hook handler, and writes stdout.
// Also runs any matching plugins. Panics are recovered silently.
func Run(hookName string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "same hook %s: panic: %v\n", hookName, r)
		}
	}()

	inputData, input, err := readInputRaw()
	if err != nil {
		return
	}

	db, err := store.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "same hook %s: cannot open database: %v\n", hookName, err)
		fmt.Fprintf(os.Stderr, "  Hint: run 'same init' or 'same doctor' to diagnose\n")
		return
	}
	defer db.Close()

	var output *HookOutput

	switch hookName {
	case "context-surfacing":
		output = runContextSurfacing(db, input)
	case "decision-extractor":
		output = runDecisionExtractor(db, input)
	case "handoff-generator":
		output = runHandoffGenerator(db, input)
	case "staleness-check":
		output = runStalenessCheck(db, input)
	case "session-bootstrap":
		output = runSessionBootstrap(db, input)
	default:
		fmt.Fprintf(os.Stderr, "same hook: unknown hook %q\n", hookName)
		return
	}

	// Run plugins matching this hook's event
	eventName := hookEventMap[hookName]
	if eventName != "" {
		pluginContexts := RunPlugins(eventName, inputData)
		if len(pluginContexts) > 0 {
			output = mergePluginOutput(output, eventName, pluginContexts)
		}
	}

	if output != nil {
		data, err := json.Marshal(output)
		if err != nil {
			return
		}
		os.Stdout.Write(data)
		os.Stdout.Write([]byte("\n"))
	}
}

// mergePluginOutput appends plugin contexts to the built-in hook output.
func mergePluginOutput(output *HookOutput, eventName string, pluginContexts []string) *HookOutput {
	extra := "\n<plugin-context>\n" + strings.Join(pluginContexts, "\n---\n") + "\n</plugin-context>\n"

	if output == nil {
		return &HookOutput{
			HookSpecificOutput: &HookSpecific{
				HookEventName:     eventName,
				AdditionalContext: extra,
			},
		}
	}

	output.HookSpecificOutput.AdditionalContext += extra
	return output
}

// readInputRaw reads stdin and returns both the raw bytes and parsed input.
func readInputRaw() ([]byte, *HookInput, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 {
		return data, &HookInput{}, nil
	}
	var input HookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, nil, err
	}
	return data, &input, nil
}
