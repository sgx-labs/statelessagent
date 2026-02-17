package hooks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// isVerbose checks whether verbose monitoring is active on each invocation.
// Supports mid-session toggling via flag file or env var.
func isVerbose() bool {
	return config.VerboseEnabled()
}

// ANSI escape codes for styled terminal output.
const (
	cReset  = "\033[0m"
	cDim    = "\033[2m"
	cBold   = "\033[1m"
	cCyan   = "\033[36m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
)

// Rotating vocabulary for verbose output — keeps it human and alive.
var surfaceVerbs = []string{
	"surfaced", "recalled", "unearthed", "found something",
	"remembered", "connected", "dug up", "sparked",
}

var quietVerbs = []string{
	"quiet", "nothing needed", "all good", "standing by",
	"listening", "at ease", "moving on", "got it",
}

// verbosePickIndex is a simple counter that rotates through word lists.
// Not random — deterministic rotation so consecutive prompts always differ.
var verbosePickIndex int

func pickVerb(verbs []string) string {
	v := verbs[verbosePickIndex%len(verbs)]
	verbosePickIndex++
	return v
}

// verboseLogPath returns the file path for verbose output.
func verboseLogPath() string {
	return filepath.Join(config.DataDir(), "verbose.log")
}

// pendingVerboseMsg accumulates the verbose status line for this invocation.
// Read by Run() in runner.go to attach as systemMessage in the JSON output.
// Guarded by pendingVerboseMu to prevent races between the handler goroutine
// (which writes via verboseDecision) and the main goroutine (which reads via
// getPendingVerboseMsg in runner.go).
var (
	pendingVerboseMsg string
	pendingVerboseMu  sync.Mutex
)

// getPendingVerboseMsg returns and clears the pending verbose message.
func getPendingVerboseMsg() string {
	pendingVerboseMu.Lock()
	defer pendingVerboseMu.Unlock()
	msg := pendingVerboseMsg
	pendingVerboseMsg = ""
	return msg
}

// setPendingVerboseMsg sets the pending verbose message under the mutex.
func setPendingVerboseMsg(msg string) {
	pendingVerboseMu.Lock()
	defer pendingVerboseMu.Unlock()
	pendingVerboseMsg = msg
}

// verboseDecision writes a styled decision box to both stderr and verbose.log.
// stderr may be swallowed by Claude Code, but the log file is always available
// via: tail -f .scripts/same/data/verbose.log
func verboseDecision(decision, mode string, jaccard float64, prompt string, titles []string, tokens int) {
	if !isVerbose() {
		return
	}

	snippet := prompt
	if len(snippet) > 60 {
		snippet = snippet[:60] + "…"
	}

	// Pick verb once, use for both plain and styled output
	var verb, reasonStr string
	if decision == "inject" {
		verb = pickVerb(surfaceVerbs)
	} else {
		verb = pickVerb(quietVerbs)
	}
	switch {
	case jaccard >= 0:
		reasonStr = fmt.Sprintf("%s · jaccard=%.2f", mode, jaccard)
	case mode != "":
		reasonStr = mode
	default:
		reasonStr = decision
	}

	// --- Dual output: systemMessage (inject only) + stderr (Ctrl+O verbose) ---

	// 1. systemMessage: only for injects. Skips are silent — no noise, no error look.
	if decision == "inject" {
		setPendingVerboseMsg(fmt.Sprintf("✦ %s — %d notes · ~%d tokens: %s",
			verb, len(titles), tokens, strings.Join(titles, ", ")))
	}

	// 2. stderr: ANSI-styled box, visible when Ctrl+O verbose is expanded.
	//    Shows both inject and skip for debugging/monitoring.
	if decision == "inject" {
		fmt.Fprintf(os.Stderr, "\n%s╭─%s %sSAME%s %s─────────────────────────────────╮%s\n",
			cDim+cCyan, cReset, cBold+cCyan, cReset, cDim+cCyan, cReset)
		fmt.Fprintf(os.Stderr, "%s│%s  %s✦%s  %s%s%s — %d notes · ~%d tokens\n",
			cDim+cCyan, cReset, cGreen, cReset, cBold+cGreen, verb, cReset, len(titles), tokens)
		for i, t := range titles {
			conn := "├"
			if i == len(titles)-1 {
				conn = "└"
			}
			fmt.Fprintf(os.Stderr, "%s│%s      %s%s %s%s\n",
				cDim+cCyan, cReset, cDim, conn, t, cReset)
		}
		fmt.Fprintf(os.Stderr, "%s╰─────────────── %ssame verbose off%s %sto disable%s ─╯%s\n",
			cDim+cCyan, cCyan, cReset, cDim+cCyan, cDim+cCyan, cReset)
	} else {
		fmt.Fprintf(os.Stderr, "%s╭─%s %sSAME%s %s─╮%s %s·%s %s%s%s\n",
			cDim+cCyan, cReset, cBold+cCyan, cReset, cDim+cCyan, cReset,
			cDim, cReset, cDim+cYellow, verb, cReset)
	}

	// --- Styled box to log file (visible via same verbose watch) ---
	b := cDim + cCyan
	r := cReset
	var buf strings.Builder

	fmt.Fprintf(&buf, "\n%s╭─%s %sSAME%s %s────────────────────────────────────────────╮%s\n",
		b, r, cBold+cCyan, r, b, r)

	if decision == "inject" {
		fmt.Fprintf(&buf, "%s│%s  %s✦%s  %s%s%s%s (%s) · %d notes · ~%d tokens\n",
			b, r, cGreen, r, cBold, cGreen, verb, r, mode, len(titles), tokens)
		for i, t := range titles {
			conn := "├"
			if i == len(titles)-1 {
				conn = "└"
			}
			fmt.Fprintf(&buf, "%s│%s      %s%s %s%s\n", b, r, cDim, conn, t, r)
		}
	} else {
		fmt.Fprintf(&buf, "%s│%s  %s·%s  %s%s%s%s (%s): %q\n",
			b, r, cDim, r, cDim, cYellow, verb, r, reasonStr, snippet)
	}

	fmt.Fprintf(&buf, "%s╰──────────────────── %ssame verbose off%s %sto disable %s─╯%s\n",
		b, cCyan, r, b, b, r)

	writeVerboseLog(buf.String())
}

// writeVerboseLog appends content to the verbose log file with size-based rotation.
// If the log exceeds 5MB, it keeps only the last ~1MB before appending.
// Uses 0o600 permissions (owner-only) since the log may contain prompt snippets.
func writeVerboseLog(content string) {
	logPath := verboseLogPath()

	const maxSize = 5 * 1024 * 1024  // 5MB
	const keepSize = 1 * 1024 * 1024  // 1MB

	// Check if rotation is needed
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxSize {
		data, err := os.ReadFile(logPath)
		if err == nil && len(data) > keepSize {
			// Keep the last 1MB
			truncated := data[len(data)-keepSize:]
			// Find the first newline to avoid splitting mid-line
			if idx := bytes.IndexByte(truncated, '\n'); idx >= 0 {
				truncated = truncated[idx+1:]
			}
			if err := os.WriteFile(logPath, truncated, 0o600); err != nil {
				fmt.Fprintf(os.Stderr, "same: warning: failed to rotate verbose log: %v\n", err)
			}
		}
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	if _, err := f.WriteString(content); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to append verbose log: %v\n", err)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to close verbose log file: %v\n", err)
	}
}

// logDecision records a context surfacing gate decision to the DB
// and emits styled verbose output to stderr when verbose is enabled.
func logDecision(db *store.DB, sessionID, prompt, mode string, jaccard float64, decision string, paths []string) {
	snippet := prompt
	if len(snippet) > 80 {
		snippet = snippet[:80]
	}

	// Styled verbose output (inject is handled separately with titles/tokens)
	if decision != "inject" {
		verboseDecision(decision, mode, jaccard, prompt, nil, 0)
	}

	if sessionID == "" {
		return
	}
	_ = db.InsertDecision(&store.DecisionRecord{
		SessionID:     sessionID,
		PromptSnippet: snippet,
		Mode:          mode,
		JaccardScore:  jaccard,
		Decision:      decision,
		InjectedPaths: paths,
	})
}
