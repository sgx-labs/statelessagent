// Package hooks implements Claude Code lifecycle hook handlers.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// maxTranscriptSize is the maximum transcript file size we'll process (50 MB).
const maxTranscriptSize = 50 * 1024 * 1024

// maxStdinSize is the maximum size of stdin input we'll read (10 MB).
const maxStdinSize = 10 * 1024 * 1024

// AI-facing diagnostic messages. These are pre-written constants — no raw Go
// error strings leak to the AI (could contain paths or internal state).
const diagNoDB = `<same-diagnostic>
SAME cannot open its database. The user's vault may not be initialized.
Suggested actions for the user:
- Run "same init" to set up SAME
- Run "same doctor" to diagnose issues
</same-diagnostic>`

const diagTimeout = `<same-diagnostic>
SAME timed out while processing your prompt. This usually means the embedding provider is slow or unresponsive.
Suggested actions for the user:
- Check if Ollama is running and responsive
- Run "same doctor" to diagnose issues
</same-diagnostic>`

// hookTimeout is the maximum time allowed for hook execution.
// Covers Ollama cold start (2-5s) + one retry (4s) with margin.
const hookTimeout = 10 * time.Second

// HookInput is the JSON input from Claude Code hooks.
// Field names use snake_case to match Claude Code's JSON format.
type HookInput struct {
	Prompt         string `json:"prompt,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	HookEventName  string `json:"hook_event_name,omitempty"`
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
			fmt.Fprintf(os.Stderr, "same: unexpected error in %s hook — run 'same doctor' if this persists\n", hookName)
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
		fmt.Fprintf(os.Stderr, "same: can't find your vault database — run 'same init' to set up, or 'same doctor' to diagnose\n")
		// Return diagnostic output so the AI knows what happened.
		// hookSpecificOutput is only valid for PreToolUse, UserPromptSubmit,
		// PostToolUse. For other events, use systemMessage.
		eventName := hookEventMap[hookName]
		if eventName == "" {
			eventName = "UserPromptSubmit"
		}
		var output *HookOutput
		switch eventName {
		case "UserPromptSubmit", "PreToolUse", "PostToolUse":
			output = &HookOutput{
				HookSpecificOutput: &HookSpecific{
					HookEventName:     eventName,
					AdditionalContext: diagNoDB,
				},
			}
		default:
			output = &HookOutput{
				SystemMessage: diagNoDB,
			}
		}
		data, jsonErr := json.Marshal(output)
		if jsonErr == nil {
			os.Stdout.Write(data)
			os.Stdout.Write([]byte("\n"))
		}
		return
	}
	// Do NOT defer db.Close() here — we must wait for the goroutine to
	// finish before closing, to prevent use-after-close when the timeout
	// fires but the goroutine is still writing to the DB.

	var output *HookOutput
	activity := hookEmpty("")

	// Run hook dispatch with timeout to prevent hung embedding providers
	// from blocking the user's prompt indefinitely.
	// A WaitGroup ensures the goroutine has finished before we close the DB.
	type hookResult struct {
		result hookRunResult
	}
	ch := make(chan hookResult, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var result hookRunResult
		switch hookName {
		case "context-surfacing":
			result = runContextSurfacing(db, input)
		case "decision-extractor":
			result = runDecisionExtractor(db, input)
		case "handoff-generator":
			result = runHandoffGenerator(db, input)
		case "feedback-loop":
			result = runFeedbackLoop(db, input)
		case "staleness-check":
			result = runStalenessCheck(db, input)
		case "session-bootstrap":
			result = runSessionBootstrap(db, input)
		default:
			fmt.Fprintf(os.Stderr, "same: unknown hook %q — your hooks config may need updating\n", hookName)
			result = hookError("unknown hook")
		}

		// Run plugins matching this hook's event
		eventName := hookEventMap[hookName]
		if eventName != "" {
			pluginContexts := RunPlugins(eventName, inputData)
			if len(pluginContexts) > 0 {
				result.Output = mergePluginOutput(result.Output, eventName, pluginContexts)
			}
		}

		// Attach pending verbose message
		if msg := getPendingVerboseMsg(); msg != "" {
			if result.Output == nil {
				result.Output = &HookOutput{}
			}
			result.Output.SystemMessage += msg
		}

		ch <- hookResult{result: normalizeHookResult(result)}
	}()

	select {
	case result := <-ch:
		normalized := normalizeHookResult(result.result)
		output = normalized.Output
		activity = normalized
	case <-time.After(hookTimeout):
		fmt.Fprintf(os.Stderr, "same: timed out waiting for Ollama — it may be starting up. If this persists, run 'same doctor'\n")
		eventName := hookEventMap[hookName]
		if eventName == "" {
			eventName = "UserPromptSubmit"
		}
		// hookSpecificOutput is only valid for PreToolUse, UserPromptSubmit,
		// and PostToolUse. For other events, use systemMessage.
		switch eventName {
		case "UserPromptSubmit", "PreToolUse", "PostToolUse":
			output = &HookOutput{
				HookSpecificOutput: &HookSpecific{
					HookEventName:     eventName,
					AdditionalContext: diagTimeout,
				},
			}
		default:
			output = &HookOutput{
				SystemMessage: diagTimeout,
			}
		}
		activity = hookError("timeout")
		activity.Output = output
	}

	// Wait for the goroutine to finish before closing the DB.
	// On timeout, this blocks briefly until the goroutine returns,
	// preventing writes to a closed database.
	wg.Wait()
	recordHookActivity(db, hookName, input, activity)
	db.Close()

	// Always write valid JSON to stdout. Claude Code treats empty stdout
	// as a hook failure. When there's nothing to report, write a minimal
	// response appropriate for the hook's event type.
	//
	// IMPORTANT: hookSpecificOutput is only valid for PreToolUse,
	// UserPromptSubmit, and PostToolUse events. Stop and SessionStart
	// hooks must use top-level fields only (systemMessage, decision, etc.)
	// or an empty object {}.
	if output == nil {
		output = &HookOutput{}
	}
	data, err := json.Marshal(output)
	if err != nil {
		return
	}
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}

func recordHookActivity(db *store.DB, hookName string, input *HookInput, result hookRunResult) {
	if db == nil {
		return
	}
	result = normalizeHookResult(result)

	sessionID := ""
	if input != nil {
		sessionID = input.SessionID
	}

	rec := &store.HookActivityRecord{
		TimestampUnix:   time.Now().Unix(),
		HookSessionID:   sessionID,
		HookName:        hookName,
		Status:          result.Status,
		SurfacedNotes:   result.NotesCount,
		EstimatedTokens: result.EstimatedTokens,
		ErrorMessage:    result.ErrorMessage,
		Detail:          result.Detail,
		NotePaths:       result.NotePaths,
	}
	_ = db.InsertHookActivity(rec) // best-effort telemetry
}

// mergePluginOutput appends plugin contexts to the built-in hook output.
// SECURITY (S8): Plugin output is sanitized to prevent XML tag injection
// that could break the <plugin-context> wrapper or the <vault-context>
// wrapper, which would let a malicious plugin inject system-level instructions.
func mergePluginOutput(output *HookOutput, eventName string, pluginContexts []string) *HookOutput {
	// Sanitize each plugin context to strip/neutralize structural XML tags.
	sanitized := make([]string, len(pluginContexts))
	for i, ctx := range pluginContexts {
		sanitized[i] = sanitizeContextTags(ctx)
	}

	extra := "\n<plugin-context>\n" + strings.Join(sanitized, "\n---\n") + "\n</plugin-context>\n"

	if output == nil {
		output = &HookOutput{}
	}

	// hookSpecificOutput is only valid for PreToolUse, UserPromptSubmit,
	// and PostToolUse. For Stop/SessionStart, use systemMessage.
	switch eventName {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		if output.HookSpecificOutput == nil {
			output.HookSpecificOutput = &HookSpecific{
				HookEventName:     eventName,
				AdditionalContext: extra,
			}
		} else {
			output.HookSpecificOutput.AdditionalContext += extra
		}
	default:
		output.SystemMessage += extra
	}
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
// SECURITY: stdin is capped at maxStdinSize (10 MB) to prevent memory exhaustion.
func readInputRaw() ([]byte, *HookInput, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinSize))
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
