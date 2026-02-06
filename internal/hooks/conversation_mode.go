package hooks

import (
	"regexp"
	"strings"
)

// ConversationMode represents the detected interaction mode.
type ConversationMode int

const (
	ModeExploring  ConversationMode = iota // Questions, open-ended inquiry
	ModeDeepening                          // Specific follow-ups on a known topic
	ModeExecuting                          // Imperative tasks: fix, build, implement
	ModeReflecting                         // Evaluative: risks, benefits, tradeoffs
	ModeSocializing                        // Greetings, affirmations (handled by isConversational)
)

func (m ConversationMode) String() string {
	switch m {
	case ModeExploring:
		return "exploring"
	case ModeDeepening:
		return "deepening"
	case ModeExecuting:
		return "executing"
	case ModeReflecting:
		return "reflecting"
	case ModeSocializing:
		return "socializing"
	default:
		return "unknown"
	}
}

// detectMode classifies the prompt into a conversation mode.
// Uses cheap pattern matching — no API calls, no DB access.
// Socializing is handled upstream by isConversational; this function
// distinguishes between the remaining modes.
func detectMode(prompt string) ConversationMode {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	words := strings.Fields(lower)
	if len(words) == 0 {
		return ModeSocializing
	}

	// Score each mode based on signal presence
	execScore := executingScore(lower, words)
	reflectScore := reflectingScore(lower, words)
	exploreScore := exploringScore(lower, words)

	// Executing wins when imperative signals are strong
	if execScore >= 2 || (execScore >= 1 && reflectScore == 0 && exploreScore == 0) {
		return ModeExecuting
	}

	// Reflecting wins when evaluative signals are strong.
	// A question with reflecting terms ("what are the risks?",
	// "whats your recommendation?") is reflective, not exploratory.
	// But a single reflecting term in an exploring frame ("walk me
	// through the architecture") is still exploring — require score >= 2
	// OR a reflecting question pattern to be present.
	hasReflectQuestion := reflectingQuestions.MatchString(lower)
	if reflectScore >= 2 || (reflectScore >= 1 && hasReflectQuestion) {
		return ModeReflecting
	}

	// Exploring is the default for prompts with questions or domain terms
	if exploreScore >= 1 {
		return ModeExploring
	}

	// Deepening: follow-up on an existing topic (short, no strong mode signal)
	if len(words) <= 15 && execScore == 0 && reflectScore == 0 {
		return ModeDeepening
	}

	return ModeExploring
}

// executingScore returns a signal count for imperative/task mode.
var executingVerbs = regexp.MustCompile(`\b(fix|build|create|implement|add|remove|delete|update|refactor|write|run|deploy|install|move|rename|replace|change|set|configure|wire|ship|push|commit|merge|rebase)\b`)
var executingPhrases = regexp.MustCompile(`\b(make it .{3,30}|start with|can you .{3,30}(fix|build|add|create|implement|change|update|write|run)|could you .{3,30}(fix|build|add|create|implement|change|update|write|run)|please .{3,20}(fix|build|add|create|implement|change|update|write|run))\b`)

func executingScore(lower string, words []string) int {
	score := 0

	// Imperative verb at or near start of sentence
	if len(words) >= 1 && executingVerbs.MatchString(words[0]) {
		score += 2 // Strong signal: sentence starts with imperative
	}

	// Imperative verbs anywhere
	matches := executingVerbs.FindAllString(lower, -1)
	if len(matches) > 0 {
		score++
	}
	if len(matches) >= 2 {
		score++ // Multiple imperatives: "fix X and add Y"
	}

	// Task phrases
	if executingPhrases.MatchString(lower) {
		score++
	}

	return score
}

// reflectingScore returns a signal count for evaluative/reflective mode.
var reflectingTerms = regexp.MustCompile(`\b(recommend\w*|risk\w*|benefit\w*|tradeoff\w*|trade-off\w*|pros|cons|swot|assess\w*|evaluat\w*|compar\w*|versus|should we|worth|downside|upside|concern\w*|implication\w*|consequence\w*|alternativ\w*|option\w*|strateg\w*|approach\w*|architect\w*|design|principle\w*|philosophy)\b`)
var reflectingQuestions = regexp.MustCompile(`\b(what do you think|what are the|is this the right|is there a better|what would|how should|what if|are there any|what.?s your|whats the|what is your)\b`)

func reflectingScore(lower string, words []string) int {
	score := 0

	matches := reflectingTerms.FindAllString(lower, -1)
	score += len(matches)
	if score > 3 {
		score = 3 // Cap to avoid over-counting
	}

	if reflectingQuestions.MatchString(lower) {
		score++
	}

	return score
}

// exploringScore returns a signal count for open-ended inquiry.
var exploringStarts = regexp.MustCompile(`^(what|where|how|who|when|why|which|tell me|show me|explain|describe|walk me)`)
var exploringPatterns = regexp.MustCompile(`\b(what is|how does|where are|can you explain|tell me about|show me the|walk me through|what about)\b`)

func exploringScore(lower string, words []string) int {
	score := 0

	if exploringStarts.MatchString(lower) {
		score += 2 // Starts with question word
	}

	if exploringPatterns.MatchString(lower) {
		score++
	}

	// Contains question mark
	if strings.Contains(lower, "?") {
		score++
	}

	return score
}
