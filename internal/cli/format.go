// Package cli provides shared formatting helpers for CLI output.
package cli

import (
	"fmt"
	"os"
	"strings"
)

// ANSI color constants.
const (
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Red     = "\033[31m"
	Cyan    = "\033[36m"
	DimCyan = "\033[2;36m"
	Dim     = "\033[2m"
	Bold    = "\033[1m"
	Reset   = "\033[0m"
)

// Box width is the inner content width (between the border characters).
const boxWidth = 40

// Margin is the left indent for all branded output.
const margin = "  "

// ANSI 256-color red gradient — bright to dark, one per logo line.
var redGradient = []string{
	"\033[38;5;196m", // #ff1a1a bright red
	"\033[38;5;196m", // #f01515
	"\033[38;5;160m", // #e01010
	"\033[38;5;160m", // #d00c0c
	"\033[38;5;124m", // #bf0808
	"\033[38;5;124m", // #af0505
	"\033[38;5;124m", // #9e0404
	"\033[38;5;88m",  // #8e0303
	"\033[38;5;88m",  // #7d0202
	"\033[38;5;88m",  // #6d0202
	"\033[38;5;52m",  // #5c0101
	"\033[38;5;52m",  // #4c0101
}

// ShortenHome replaces $HOME prefix with ~.
func ShortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// FormatNumber adds comma separators (1234 -> "1,234").
func FormatNumber(n int) string {
	if n < 0 {
		return "-" + FormatNumber(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return FormatNumber(n/1000) + "," + fmt.Sprintf("%03d", n%1000)
}

// Banner prints the large STATELESS AGENT ASCII art logo with red gradient
// and tagline. Used by `same init`.
func Banner(version string) {
	logo := []string{
		"  \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557 \u2588\u2588\u2588\u2588\u2588\u2557 \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2557     \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557",
		"  \u2588\u2588\u2554\u2550\u2550\u2550\u2550\u255d\u255a\u2550\u2550\u2588\u2588\u2554\u2550\u2550\u255d\u2588\u2588\u2554\u2550\u2550\u2588\u2588\u2557\u255a\u2550\u2550\u2588\u2588\u2554\u2550\u2550\u255d\u2588\u2588\u2554\u2550\u2550\u2550\u2550\u255d\u2588\u2588\u2551     \u2588\u2588\u2554\u2550\u2550\u2550\u2550\u255d\u2588\u2588\u2554\u2550\u2550\u2550\u2550\u255d\u2588\u2588\u2554\u2550\u2550\u2550\u2550\u255d",
		"  \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557   \u2588\u2588\u2551   \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2551   \u2588\u2588\u2551   \u2588\u2588\u2588\u2588\u2588\u2557  \u2588\u2588\u2551     \u2588\u2588\u2588\u2588\u2588\u2557  \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557",
		"  \u255a\u2550\u2550\u2550\u2550\u2588\u2588\u2551   \u2588\u2588\u2551   \u2588\u2588\u2554\u2550\u2550\u2588\u2588\u2551   \u2588\u2588\u2551   \u2588\u2588\u2554\u2550\u2550\u255d  \u2588\u2588\u2551     \u2588\u2588\u2554\u2550\u2550\u255d  \u255a\u2550\u2550\u2550\u2550\u2588\u2588\u2551\u255a\u2550\u2550\u2550\u2550\u2588\u2588\u2551",
		"  \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2551   \u2588\u2588\u2551   \u2588\u2588\u2551  \u2588\u2588\u2551   \u2588\u2588\u2551   \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2551\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2551",
		"  \u255a\u2550\u2550\u2550\u2550\u2550\u2550\u255d   \u255a\u2550\u255d   \u255a\u2550\u255d  \u255a\u2550\u255d   \u255a\u2550\u255d   \u255a\u2550\u2550\u2550\u2550\u2550\u2550\u255d\u255a\u2550\u2550\u2550\u2550\u2550\u2550\u255d\u255a\u2550\u2550\u2550\u2550\u2550\u2550\u255d\u255a\u2550\u2550\u2550\u2550\u2550\u2550\u255d\u255a\u2550\u2550\u2550\u2550\u2550\u2550\u255d",
		"           \u2588\u2588\u2588\u2588\u2588\u2557  \u2588\u2588\u2588\u2588\u2588\u2588\u2557 \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2557   \u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557",
		"          \u2588\u2588\u2554\u2550\u2550\u2588\u2588\u2557\u2588\u2588\u2554\u2550\u2550\u2550\u2550\u255d \u2588\u2588\u2554\u2550\u2550\u2550\u2550\u255d\u2588\u2588\u2588\u2588\u2557  \u2588\u2588\u2551\u255a\u2550\u2550\u2588\u2588\u2554\u2550\u2550\u255d",
		"          \u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2551\u2588\u2588\u2551  \u2588\u2588\u2588\u2557\u2588\u2588\u2588\u2588\u2588\u2557  \u2588\u2588\u2554\u2588\u2588\u2557 \u2588\u2588\u2551   \u2588\u2588\u2551",
		"          \u2588\u2588\u2554\u2550\u2550\u2588\u2588\u2551\u2588\u2588\u2551   \u2588\u2588\u2551\u2588\u2588\u2554\u2550\u2550\u255d  \u2588\u2588\u2551\u255a\u2588\u2588\u2557\u2588\u2588\u2551   \u2588\u2588\u2551",
		"          \u2588\u2588\u2551  \u2588\u2588\u2551\u255a\u2588\u2588\u2588\u2588\u2588\u2588\u2554\u255d\u2588\u2588\u2588\u2588\u2588\u2588\u2588\u2557\u2588\u2588\u2551 \u255a\u2588\u2588\u2588\u2588\u2551   \u2588\u2588\u2551",
		"          \u255a\u2550\u255d  \u255a\u2550\u255d \u255a\u2550\u2550\u2550\u2550\u2550\u255d \u255a\u2550\u2550\u2550\u2550\u2550\u2550\u255d\u255a\u2550\u255d  \u255a\u2550\u2550\u2550\u255d   \u255a\u2550\u255d",
	}

	fmt.Println()
	for i, line := range logo {
		color := redGradient[i%len(redGradient)]
		fmt.Printf("%s%s%s\n", color, line, Reset)
	}
	fmt.Println()
	fmt.Printf("  %sEvery AI session starts from zero.%s %s%sNot anymore.%s\n",
		Dim, Reset, Bold, Red, Reset)
	fmt.Println()
	fmt.Printf("  %sSAME%s %s\u2014 Stateless Agent Memory Engine v%s%s\n",
		Bold, Reset, Dim, version, Reset)
}

// Header prints a small heavy-border box with a title. Used by `same status` and `same doctor`.
func Header(title string) {
	fmt.Println()
	heavyTop := margin + "\u250f" + strings.Repeat("\u2501", boxWidth) + "\u2513"
	heavyBottom := margin + "\u2517" + strings.Repeat("\u2501", boxWidth) + "\u251b"

	content := "  " + title
	padded := padRight(content, boxWidth)

	fmt.Printf("%s%s%s\n", Cyan, heavyTop, Reset)
	fmt.Printf("%s%s\u2503%s\u2503%s\n", Cyan, margin, padded, Reset)
	fmt.Printf("%s%s%s\n", Cyan, heavyBottom, Reset)
}

// Section prints a section divider line: ── Name ─────────────────
func Section(name string) {
	prefix := "\u2500\u2500 " + name + " "
	remaining := boxWidth + 2 - runeLen(prefix)
	if remaining < 0 {
		remaining = 0
	}
	rule := prefix + strings.Repeat("\u2500", remaining)
	fmt.Printf("\n%s%s%s%s%s\n\n", margin, Cyan, rule, Reset, "")
}

// Box prints a light-border box around content lines.
func Box(lines []string) {
	lightTop := margin + "\u250c" + strings.Repeat("\u2500", boxWidth) + "\u2510"
	lightBottom := margin + "\u2514" + strings.Repeat("\u2500", boxWidth) + "\u2518"

	fmt.Println()
	fmt.Println(lightTop)
	for _, line := range lines {
		content := "  " + line
		padded := padRight(content, boxWidth)
		fmt.Printf("%s\u2502%s\u2502\n", margin, padded)
	}
	fmt.Println(lightBottom)
}

// Footer prints the branded footer in dim text.
func Footer() {
	fmt.Printf("\n%s%sstatelessagent.com \u00b7 sgx-labs/statelessagent%s\n\n", margin, Dim, Reset)
}

// padRight pads s with spaces to exactly width characters.
// If s is longer than width, it is truncated.
func padRight(s string, width int) string {
	n := runeLen(s)
	if n >= width {
		r := []rune(s)
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-n)
}

// runeLen counts the display width in runes.
func runeLen(s string) int {
	return len([]rune(s))
}

// --- Surfacing Display ---

// SurfacedNote represents a note that was found during context surfacing.
type SurfacedNote struct {
	Title      string
	Tokens     int      // 0 if not included
	Included   bool     // whether it was injected into context
	HighConf   bool     // high confidence = ✦, low = ✧
	MatchTerms []string // keywords that matched
}

// surfacingVerbs are rotated randomly for delight.
var surfacingVerbs = []string{
	"surfaced", "unearthed", "recalled", "discovered", "found", "retrieved",
}

// randomVerb returns a random surfacing verb.
func randomVerb() string {
	// Use nanoseconds for cheap randomness without importing math/rand
	ns := int(runeLen(fmt.Sprintf("%d", os.Getpid()))) // deterministic per process for consistency
	return surfacingVerbs[ns%len(surfacingVerbs)]
}

// SurfacingCompact prints the single-line compact surfacing output.
// Example: ✦ SAME has surfaced 3 of 847 memories
func SurfacingCompact(included, total int) {
	verb := randomVerb()
	fmt.Fprintf(os.Stderr, "%s✦ %sSAME%s %shas %s%s %d of %d memories%s\n",
		Cyan, Cyan, Reset, Dim, verb, Reset, included, total, Reset)
}

// SurfacingEmpty prints the playful empty state.
// Example: ✦ SAME searched 847 memories — nothing matched
func SurfacingEmpty(total int) {
	fmt.Fprintf(os.Stderr, "%s✦ %sSAME%s %ssearched %d memories — nothing matched%s\n",
		Cyan, Cyan, Reset, Dim, total, Reset)
}

// SurfacingVerbose prints surfaced notes using the visual feedback box.
func SurfacingVerbose(notes []SurfacedNote, totalVault int) {
	surfacingBox(notes, totalVault)
}

// surfacingSimple prints a clean, simple list format that works in all terminals.
func surfacingSimple(notes []SurfacedNote, totalVault int) {
	var included, excluded []SurfacedNote
	for _, n := range notes {
		if n.Included {
			included = append(included, n)
		} else {
			excluded = append(excluded, n)
		}
	}

	verb := randomVerb()
	fmt.Fprintf(os.Stderr, "%sSAME %s %d memories:%s\n",
		Cyan, verb, len(included), Reset)

	for _, n := range included {
		fmt.Fprintf(os.Stderr, "  %s+%s %s %s(%d tokens)%s\n",
			Cyan, Reset, n.Title, Dim, n.Tokens, Reset)
		if len(n.MatchTerms) > 0 {
			fmt.Fprintf(os.Stderr, "    %smatched: %s%s\n",
				Dim, strings.Join(quoteTerms(n.MatchTerms), ", "), Reset)
		}
	}

	if len(excluded) > 0 {
		fmt.Fprintf(os.Stderr, "  %s- also found: %s%s\n",
			Dim, excluded[0].Title, Reset)
	}
	fmt.Fprintf(os.Stderr, "\n")
}

// surfacingBox prints the fancy Unicode box format.
func surfacingBox(notes []SurfacedNote, totalVault int) {
	var included, found int
	for _, n := range notes {
		if n.Included {
			included++
		}
		found++
	}

	verb := randomVerb()
	boxWidth := 71

	// Header
	headerLeft := fmt.Sprintf("─ SAME %shas %s%s ", Dim, verb, Cyan)
	headerRight := fmt.Sprintf(" added %d of %d memories ─", included, found)
	headerLeftLen := 8 + len(verb) // "─ SAME has " + verb + " "
	headerRightLen := runeLen(headerRight)
	headerPad := boxWidth - headerLeftLen - headerRightLen
	if headerPad < 0 {
		headerPad = 0
	}

	fmt.Fprintf(os.Stderr, "%s╭%s%s%s%s╮%s\n",
		Cyan, headerLeft, strings.Repeat("─", headerPad), headerRight, Cyan, Reset)
	fmt.Fprintf(os.Stderr, "%s│%s│%s\n", Cyan, strings.Repeat(" ", boxWidth), Reset)

	// Included section
	fmt.Fprintf(os.Stderr, "%s│   ✓ Included%s│%s\n",
		Cyan, strings.Repeat(" ", boxWidth-13), Reset)

	for _, n := range notes {
		if !n.Included {
			continue
		}
		spark := "✦"
		color := Cyan
		if !n.HighConf {
			spark = "✧"
			color = DimCyan
		}

		// Title line with tokens
		titleLine := fmt.Sprintf("      %s %s", spark, n.Title)
		tokenStr := fmt.Sprintf("%d tokens", n.Tokens)
		pad := boxWidth - runeLen(titleLine) - runeLen(tokenStr) - 2
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(os.Stderr, "%s│%s%s%s%s%s  │%s\n",
			Cyan, color, titleLine, strings.Repeat(" ", pad), tokenStr, Cyan, Reset)

		// Match terms line (dim)
		if len(n.MatchTerms) > 0 {
			matchLine := fmt.Sprintf("        ↳ matched: %s", strings.Join(quoteTerms(n.MatchTerms), ", "))
			if runeLen(matchLine) > boxWidth-4 {
				matchLine = matchLine[:boxWidth-7] + "..."
			}
			pad := boxWidth - runeLen(matchLine) - 1
			if pad < 0 {
				pad = 0
			}
			fmt.Fprintf(os.Stderr, "%s│%s%s%s%s│%s\n",
				Cyan, Dim, matchLine, Reset+Cyan, strings.Repeat(" ", pad), Reset)
		}
	}

	// Also found section (excluded notes)
	var excluded []SurfacedNote
	for _, n := range notes {
		if !n.Included {
			excluded = append(excluded, n)
		}
	}

	if len(excluded) > 0 {
		fmt.Fprintf(os.Stderr, "%s│%s│%s\n", Cyan, strings.Repeat(" ", boxWidth), Reset)
		fmt.Fprintf(os.Stderr, "%s│   ⊘ Also found%s│%s\n",
			Cyan, strings.Repeat(" ", boxWidth-15), Reset)

		for _, n := range excluded {
			spark := "✧" // excluded are always lower confidence visually
			titleLine := fmt.Sprintf("      %s %s", spark, n.Title)
			pad := boxWidth - runeLen(titleLine) - 1
			if pad < 0 {
				pad = 0
			}
			fmt.Fprintf(os.Stderr, "%s│%s%s%s%s│%s\n",
				Cyan, DimCyan, titleLine, strings.Repeat(" ", pad), Cyan, Reset)
		}
	}

	// Footer with mode hints
	fmt.Fprintf(os.Stderr, "%s│%s│%s\n", Cyan, strings.Repeat(" ", boxWidth), Reset)
	footerRight := "same display compact · same display quiet"
	footerPad := boxWidth - runeLen(footerRight) - 1
	fmt.Fprintf(os.Stderr, "%s│%s%s%s │%s\n",
		Cyan, strings.Repeat(" ", footerPad), Dim, footerRight, Reset)
	fmt.Fprintf(os.Stderr, "%s╰%s╯%s\n", Cyan, strings.Repeat("─", boxWidth), Reset)
}

// quoteTerms wraps each term in quotes.
func quoteTerms(terms []string) []string {
	out := make([]string, len(terms))
	for i, t := range terms {
		out[i] = fmt.Sprintf("\"%s\"", t)
	}
	return out
}
