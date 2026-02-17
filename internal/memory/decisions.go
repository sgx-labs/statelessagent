package memory

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Decision represents an extracted decision.
type Decision struct {
	Text       string `json:"text"`
	Pattern    string `json:"pattern"`
	Confidence string `json:"confidence"` // "high" or "medium"
	Context    string `json:"context"`
	Role       string `json:"role,omitempty"`
}

// Decision patterns — ordered by specificity (most specific first).
var decisionPatterns = []*regexp.Regexp{
	// Explicit decision markers
	regexp.MustCompile(`(?i)\*\*Decision:?\*\*\s*(.+?)(?:\n|$)`),
	regexp.MustCompile(`(?im)(?:^|\n)\s*Decision:\s*(.+?)(?:\n|$)`),

	// "chose X over Y because Z" — high confidence
	regexp.MustCompile(`(?i)(?:chose|picked|selected)\s+(.+?)\s+over\s+(.+?)\s+(?:because|since|due to)\s+(.+?)(?:\.|$)`),

	// "decided to / decided on / decided that"
	regexp.MustCompile(`(?i)(?:I |we |I've |we've )?decided\s+(?:to|on|that)\s+(.+?)(?:\.|$)`),

	// "went with X"
	regexp.MustCompile(`(?i)(?:I |we )?went\s+with\s+(.+?)(?:\.|$)`),

	// "let's go with / use / adopt"
	regexp.MustCompile(`(?i)let'?s\s+(?:go with|use|adopt|stick with)\s+(.+?)(?:\.|$)`),

	// "chose / picked X"
	regexp.MustCompile(`(?i)(?:I |we )?(?:chose|picked)\s+(.+?)(?:\.|$)`),

	// "going to use / going with"
	regexp.MustCompile(`(?i)(?:I'm |we're |I am |we are )?going\s+(?:to use|with)\s+(.+?)(?:\.|$)`),
}

// Rationale indicators — decision must have one of these within 200 chars.
var rationaleIndicators = regexp.MustCompile(
	`(?i)(?:because|since|due to|reason|rationale|trade-?off|instead of|over|` +
		`better|simpler|easier|faster|prefer|advantage|benefit)`,
)

// False positive suppressors.
var falsePositivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:if|when|would|could|should|might)\s+(?:we|I|you)\s+(?:decide|chose)`),
	regexp.MustCompile(`(?i)(?:haven't|hasn't|didn't)\s+(?:decided|chose)`),
	regexp.MustCompile(`(?i)(?:need to|want to)\s+decide`),
}

// Number of patterns that are explicit decision markers (bypass rationale requirement).
const explicitMarkerCount = 2

// ExtractDecisions scans text for decision-like statements.
func ExtractDecisions(text string, requireRationale bool) []Decision {
	var decisions []Decision
	seen := make(map[string]bool)

	for patIdx, pattern := range decisionPatterns {
		isExplicitMarker := patIdx < explicitMarkerCount

		for _, match := range pattern.FindAllStringIndex(text, -1) {
			decisionText := strings.TrimSpace(text[match[0]:match[1]])

			// Check false positives with 20-char lookbehind window
			lookbehindStart := match[0] - 20
			if lookbehindStart < 0 {
				lookbehindStart = 0
			}
			fpWindow := text[lookbehindStart:match[1]]
			isFP := false
			for _, fp := range falsePositivePatterns {
				if fp.MatchString(fpWindow) {
					isFP = true
					break
				}
			}
			if isFP {
				continue
			}

			// Deduplicate by normalized text
			normalized := strings.ToLower(strings.TrimSpace(decisionText))
			if seen[normalized] {
				continue
			}
			seen[normalized] = true

			// Check for rationale within 200 chars of match end
			end := match[1] + 200
			if end > len(text) {
				end = len(text)
			}
			surrounding := text[match[0]:end]
			hasRationale := rationaleIndicators.MatchString(surrounding)

			if requireRationale && !hasRationale && !isExplicitMarker {
				continue
			}

			// Get context (100 chars before and after)
			ctxStart := match[0] - 100
			if ctxStart < 0 {
				ctxStart = 0
			}
			ctxEnd := match[1] + 100
			if ctxEnd > len(text) {
				ctxEnd = len(text)
			}
			context := strings.TrimSpace(text[ctxStart:ctxEnd])

			conf := "medium"
			if hasRationale || isExplicitMarker {
				conf = "high"
			}

			patternStr := pattern.String()
			if len(patternStr) > 50 {
				patternStr = patternStr[:50]
			}

			decisions = append(decisions, Decision{
				Text:       decisionText,
				Pattern:    patternStr,
				Confidence: conf,
				Context:    context,
			})
		}
	}

	return decisions
}

// ExtractDecisionsFromMessages extracts decisions from conversation messages.
// SECURITY: Only processes assistant messages to prevent adversarial injection
// via crafted user messages that contain decision-like patterns.
func ExtractDecisionsFromMessages(messages []Message) []Decision {
	var all []Decision
	for _, msg := range messages {
		if len(msg.Content) < 20 {
			continue
		}
		// Only extract decisions from assistant/tool outputs, not user messages.
		// User messages could contain adversarial text designed to inject
		// fake decisions into the decision log.
		if msg.Role == "user" {
			continue
		}
		decisions := ExtractDecisions(msg.Content, true)
		for i := range decisions {
			decisions[i].Role = msg.Role
		}
		all = append(all, decisions...)
	}
	return all
}

// FormatDecisionEntry formats a decision for appending to a log file.
func FormatDecisionEntry(d Decision, project string) string {
	dateStr := time.Now().Format("2006-01-02")
	projectTag := ""
	if project != "" {
		projectTag = fmt.Sprintf(" (project: %s)", project)
	}
	return fmt.Sprintf("\n### %s%s\n- %s\n  - *confidence: %s, auto-extracted*\n",
		dateStr, projectTag, d.Text, d.Confidence)
}

// AppendToDecisionLog appends extracted decisions to a decision log file.
func AppendToDecisionLog(decisions []Decision, logPath string, project string) int {
	if len(decisions) == 0 {
		return 0
	}

	var entries []string
	for _, d := range decisions {
		entries = append(entries, FormatDecisionEntry(d, project))
	}

	existing, _ := os.ReadFile(logPath)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to close decision log %q: %v\n", logPath, cerr)
		}
	}()

	written := 0

	if len(strings.TrimSpace(string(existing))) == 0 {
		if _, err := f.WriteString("# Decisions & Conclusions\n\n"); err != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to write decision log header %q: %v\n", logPath, err)
			return 0
		}
		if _, err := f.WriteString("*Auto-extracted decisions are tagged for human review.*\n"); err != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to write decision log header %q: %v\n", logPath, err)
			return 0
		}
	}

	for _, entry := range entries {
		if _, err := f.WriteString(entry); err != nil {
			fmt.Fprintf(os.Stderr, "same: warning: failed to append decision entry %q: %v\n", logPath, err)
			return written
		}
		written++
	}

	return written
}
