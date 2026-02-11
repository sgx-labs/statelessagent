package hooks

import (
	"regexp"
	"strings"
)

// keyTermsPrompt stores the current prompt for keyword extraction.
// Set before calling standardSearch.
var keyTermsPrompt string

// extractKeyTerms pulls meaningful search terms from the current prompt.
// Returns two slices: specific (high-signal: acronyms, quoted, hyphenated)
// and broad (individual words). Keyword fallback only triggers on specific terms
// to avoid false positives on novel/generic queries.
func extractKeyTerms() (specific []string, broad []string) {
	prompt := keyTermsPrompt
	if prompt == "" {
		return nil, nil
	}

	seen := make(map[string]bool)

	// Extract quoted phrases -> specific
	quotedRe := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range quotedRe.FindAllStringSubmatch(prompt, -1) {
		t := strings.TrimSpace(m[1])
		if len(t) >= 2 && !seen[strings.ToLower(t)] {
			specific = append(specific, t)
			seen[strings.ToLower(t)] = true
		}
	}

	// Extract uppercase acronyms (2+ chars) -> specific
	// Skip common tech acronyms that appear in generic programming questions
	acronymRe := regexp.MustCompile(`\b[A-Z]{2,}\b`)
	for _, m := range acronymRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if lower == "the" || lower == "and" || lower == "for" || lower == "not" || lower == "api" {
			continue
		}
		if commonTechAcronyms[lower] {
			continue
		}
		if !seen[lower] {
			specific = append(specific, m)
			seen[lower] = true
		}
	}

	// Extract hyphenated terms -> specific
	// Skip common generic hyphenated patterns (e.g., "three-way", "real-time")
	hyphenRe := regexp.MustCompile(`\b\w+-\w+(?:-\w+)*\b`)
	for _, m := range hyphenRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if commonHyphenated[lower] {
			continue
		}
		if !seen[lower] {
			specific = append(specific, m)
			seen[lower] = true
		}
	}

	// Extract significant individual words (5+ chars) -> broad
	// These supplement specific terms but don't trigger keyword fallback alone
	wordRe := regexp.MustCompile(`\b[a-zA-Z]{5,}\b`)
	for _, m := range wordRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if stopWords[lower] || seen[lower] {
			continue
		}
		broad = append(broad, m)
		seen[lower] = true
	}

	return specific, broad
}

var stopWords = map[string]bool{
	"about": true, "above": true, "after": true, "again": true, "being": true,
	"below": true, "between": true, "could": true, "doing": true, "during": true,
	"every": true, "found": true, "going": true, "having": true, "might": true,
	"never": true, "other": true, "should": true, "their": true, "there": true,
	"these": true, "thing": true, "think": true, "those": true, "under": true,
	"until": true, "using": true, "where": true, "which": true, "while": true,
	"would": true, "write": true, "yours": true, "really": true, "please": true,
	"right": true, "since": true, "still": true, "today": true,
}

// commonTechAcronyms are generic technology acronyms that should not trigger
// keyword fallback. These appear frequently in programming questions unrelated
// to vault content.
var commonTechAcronyms = map[string]bool{
	// Protocols & networking
	"tcp": true, "udp": true, "http": true, "https": true, "ssh": true,
	"ftp": true, "dns": true, "ssl": true, "tls": true, "ip": true,
	"smtp": true, "imap": true, "grpc": true, "ws": true, "wss": true,
	// Data formats & languages
	"json": true, "xml": true, "csv": true, "html": true, "css": true,
	"sql": true, "yaml": true, "toml": true, "svg": true, "pdf": true,
	// Cloud & infrastructure
	"aws": true, "gcp": true, "vpc": true, "cdn": true, "iam": true,
	"ec2": true, "ecs": true, "eks": true, "rds": true, "sqs": true,
	"sns": true, "alb": true, "elb": true, "nat": true,
	// Dev tools & concepts
	"cli": true, "sdk": true, "ide": true, "jwt": true, "url": true,
	"uri": true, "dom": true, "orm": true, "oop": true, "tdd": true,
	"bdd": true, "ddd": true, "mvp": true, "mvc": true, "gpu": true,
	"cpu": true, "ram": true, "ssd": true, "hdd": true, "ci": true,
	"cd": true, "os": true, "vm": true,
	// REST is a special case — common in "REST API" questions
	"rest": true,
}

// isConversational returns true if the prompt is a social/conversational
// message that doesn't warrant vault context injection. Only matches when
// the entire normalized prompt is conversational — mixed prompts like
// "thanks, now show me the health data" are NOT considered conversational.
func isConversational(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	// Strip trailing punctuation for matching
	normalized = strings.TrimRight(normalized, ".!?,;:")

	// Exact match against known conversational phrases
	if conversationalPhrases[normalized] {
		return true
	}

	// Short prompts (< 5 words) where every word is conversational
	words := strings.Fields(normalized)
	if len(words) <= 5 {
		allConv := true
		for _, w := range words {
			w = strings.TrimRight(w, ".!?,;:")
			if !conversationalWords[w] {
				allConv = false
				break
			}
		}
		if allConv {
			return true
		}
	}

	return false
}

var conversationalPhrases = map[string]bool{
	"hi": true, "hey": true, "hello": true, "howdy": true,
	"thanks": true, "thank you": true, "thanks a lot": true, "thank you so much": true,
	"ok": true, "okay": true, "k": true, "got it": true, "understood": true,
	"yes": true, "yeah": true, "yep": true, "yup": true, "no": true, "nah": true, "nope": true,
	"sure": true, "sure thing": true, "of course": true,
	"great": true, "perfect": true, "awesome": true, "nice": true, "cool": true,
	"sounds good": true, "looks good": true, "that works": true, "works for me": true,
	"good job": true, "great job": true, "well done": true, "nice work": true,
	"go ahead": true, "go on": true, "continue": true, "proceed": true, "next": true,
	"i see": true, "makes sense": true, "fair enough": true,
	"good morning": true, "good afternoon": true, "good evening": true, "good night": true,
	"bye": true, "goodbye": true, "see you": true, "later": true, "cheers": true,
	"please continue": true, "please proceed": true, "please go ahead": true,
	"lgtm": true, "sgtm": true, "ty": true, "thx": true, "np": true, "yw": true,
}

var conversationalWords = map[string]bool{
	"hi": true, "hey": true, "hello": true, "howdy": true,
	"thanks": true, "thank": true, "you": true, "so": true, "much": true, "a": true, "lot": true,
	"ok": true, "okay": true, "got": true, "it": true,
	"yes": true, "yeah": true, "yep": true, "yup": true, "no": true, "nah": true, "nope": true,
	"sure": true, "great": true, "perfect": true, "awesome": true, "nice": true, "cool": true,
	"good": true, "sounds": true, "looks": true, "that": true, "works": true, "for": true, "me": true,
	"go": true, "ahead": true, "on": true, "continue": true, "proceed": true, "next": true,
	"please": true, "see": true, "makes": true, "sense": true, "fair": true, "enough": true,
	"morning": true, "afternoon": true, "evening": true, "night": true,
	"bye": true, "goodbye": true, "later": true, "cheers": true,
	"lgtm": true, "sgtm": true, "ty": true, "thx": true, "np": true, "yw": true,
	"well": true, "done": true, "job": true, "work": true,
}

// hasLowSignal returns true when the prompt lacks enough domain-specific
// terms to warrant a vault search. Requires keyTermsPrompt to be set.
// Returns true when there are 0 specific terms (acronyms, quoted phrases,
// hyphenated) AND at most 1 broad term (5+ char non-stop words).
func hasLowSignal() bool {
	specific, broad := extractKeyTerms()
	return len(specific) == 0 && len(broad) <= 1
}

// commonHyphenated are generic hyphenated terms that should not trigger
// keyword fallback. These appear in general tech discussion.
var commonHyphenated = map[string]bool{
	"three-way": true, "two-way": true, "one-way": true,
	"real-time": true, "non-blocking": true, "multi-threaded": true,
	"single-threaded": true, "open-source": true, "built-in": true,
	"read-only": true, "read-write": true, "write-only": true,
	"end-to-end": true, "peer-to-peer": true, "point-to-point": true,
	"client-side": true, "server-side": true, "front-end": true,
	"back-end": true, "full-stack": true, "high-level": true,
	"low-level": true, "long-running": true, "well-known": true,
	"re-use": true, "re-run": true, "pre-built": true,
}

// extractDisplayTerms extracts meaningful terms from the prompt for display purposes.
// Returns short, recognizable terms that users will understand.
func extractDisplayTerms(prompt string) []string {
	var terms []string
	seen := make(map[string]bool)

	// Extract quoted phrases
	quotedRe := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range quotedRe.FindAllStringSubmatch(prompt, -1) {
		t := strings.TrimSpace(m[1])
		lower := strings.ToLower(t)
		if len(t) >= 2 && !seen[lower] {
			terms = append(terms, t)
			seen[lower] = true
		}
	}

	// Extract significant words (4+ chars, skip common words)
	wordRe := regexp.MustCompile(`\b[a-zA-Z]{4,}\b`)
	for _, m := range wordRe.FindAllString(prompt, -1) {
		lower := strings.ToLower(m)
		if displayStopWords[lower] || seen[lower] {
			continue
		}
		terms = append(terms, m)
		seen[lower] = true
		if len(terms) >= 5 { // cap at 5 terms for display
			break
		}
	}

	return terms
}

// findMatchingTerms returns which prompt terms appear in the note content.
func findMatchingTerms(promptTerms []string, title, snippet string) []string {
	content := strings.ToLower(title + " " + snippet)
	var matched []string
	for _, term := range promptTerms {
		if strings.Contains(content, strings.ToLower(term)) {
			matched = append(matched, term)
		}
		if len(matched) >= 3 { // cap at 3 for display
			break
		}
	}
	return matched
}

// displayStopWords are common words to skip in match term extraction.
var displayStopWords = map[string]bool{
	"what": true, "when": true, "where": true, "which": true, "while": true,
	"with": true, "would": true, "could": true, "should": true, "about": true,
	"after": true, "before": true, "being": true, "between": true, "both": true,
	"each": true, "from": true, "have": true, "having": true, "here": true,
	"into": true, "just": true, "like": true, "make": true, "more": true,
	"most": true, "need": true, "only": true, "other": true, "over": true,
	"same": true, "some": true, "such": true, "than": true, "that": true,
	"their": true, "them": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "through": true, "under": true,
	"very": true, "want": true, "were": true, "will": true, "your": true,
	"also": true, "been": true, "does": true, "done": true, "going": true,
	"help": true, "know": true, "look": true, "many": true, "much": true,
	"show": true, "tell": true, "think": true, "using": true, "work": true,
}
