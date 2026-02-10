// Package hooks implements Claude Code lifecycle hook handlers.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// maxTranscriptSize is the maximum transcript file size we'll process (50 MB).
const maxTranscriptSize = 50 * 1024 * 1024

// AI-facing diagnostic messages. These are pre-written constants — no raw Go
// error strings leak to the AI (could contain paths or internal state).
const diagNoDB = `<same-diagnostic>
SAME cannot open its database. The user's vault may not be initialized.
Suggested actions for the user:
- Run "same init" to set up SAME
- Run "same doctor" to diagnose issues
</same-diagnostic>`

const diagNoEmbed = `<same-diagnostic>
SAME cannot connect to the embedding provider. Ollama may not be running.
Suggested actions for the user:
- Check if Ollama is running (look for the llama icon in the menu bar)
- Run "same doctor" to diagnose issues
</same-diagnostic>`

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
	SystemMessage      string        `json:"systemMessage,omitempty"`
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
	"feedback-loop":      "Stop",
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

	// SECURITY: Validate transcript path before processing (M1, M5).
	if input.TranscriptPath != "" {
		if !validateTranscriptPath(input.TranscriptPath, hookName) {
			input.TranscriptPath = "" // clear invalid path
		}
	}

	// Propagate config-driven noise paths to the store package for ranking filters.
	store.NoisePaths = config.NoisePaths()

	db, err := store.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "same hook %s: cannot open database: %v\n", hookName, err)
		fmt.Fprintf(os.Stderr, "  Hint: run 'same init' or 'same doctor' to diagnose\n")
		// Return diagnostic output so the AI knows what happened
		eventName := hookEventMap[hookName]
		if eventName == "" {
			eventName = "UserPromptSubmit"
		}
		output := &HookOutput{
			HookSpecificOutput: &HookSpecific{
				HookEventName:     eventName,
				AdditionalContext: diagNoDB,
			},
		}
		data, jsonErr := json.Marshal(output)
		if jsonErr == nil {
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
		}
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
	case "feedback-loop":
		output = runFeedbackLoop(db, input)
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

	// Attach pending verbose message (set by verboseDecision) as systemMessage.
	// systemMessage is displayed to the user but NOT injected into Claude's context.
	if msg := getPendingVerboseMsg(); msg != "" {
		if output == nil {
			output = &HookOutput{}
		}
		output.SystemMessage = msg
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

// validateTranscriptPath checks that a transcript path is safe to open.
// Must be an absolute path to a regular file with a .jsonl extension and under size limit.
func validateTranscriptPath(path string, hookName string) bool {
	if !filepath.IsAbs(path) {
		fmt.Fprintf(os.Stderr, "same hook %s: transcript path must be absolute\n", hookName)
		return false
	}
	if filepath.Ext(path) != ".jsonl" {
		fmt.Fprintf(os.Stderr, "same hook %s: transcript must be .jsonl file\n", hookName)
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		fmt.Fprintf(os.Stderr, "same hook %s: transcript path is not a regular file\n", hookName)
		return false
	}
	if info.Size() > maxTranscriptSize {
		fmt.Fprintf(os.Stderr, "same hook %s: transcript too large (%d MB, max %d MB)\n",
			hookName, info.Size()/(1024*1024), maxTranscriptSize/(1024*1024))
		return false
	}
	return true
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
