package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Violation represents a single content violation.
type Violation struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Tier     Tier     `json:"tier"`
	Category Category `json:"category"`
	Rule     string   `json:"rule"`
	Redacted string   `json:"redacted"`
	Match    string   `json:"-"` // original matched text, excluded from JSON output
	Reviewed bool     `json:"reviewed,omitempty"`
}

// PathViolation represents a file that is not in the allowed paths.
type PathViolation struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// ScanResult is the complete output of a guard scan.
type ScanResult struct {
	Passed         bool            `json:"passed"`
	FilesScanned   int             `json:"files_scanned"`
	Violations     []Violation     `json:"violations,omitempty"`
	PathViolations []PathViolation `json:"path_violations,omitempty"`
	Warnings       []Violation     `json:"warnings,omitempty"` // reviewed soft violations
}

// HasBlocking returns true if there are any non-reviewed violations.
func (r *ScanResult) HasBlocking() bool {
	return len(r.Violations) > 0 || len(r.PathViolations) > 0
}

// FormatJSON returns the scan result as indented JSON.
func (r *ScanResult) FormatJSON() string {
	data, _ := json.MarshalIndent(r, "", "  ")
	return string(data)
}

// FormatHuman returns a human-readable summary for terminal display.
func (r *ScanResult) FormatHuman() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("SAME Guard — %d files scanned\n", r.FilesScanned))

	if r.Passed && len(r.Warnings) == 0 {
		b.WriteString("\n  PASSED\n")
		return b.String()
	}

	if len(r.PathViolations) > 0 {
		b.WriteString("\n")
		for _, pv := range r.PathViolations {
			b.WriteString(fmt.Sprintf("  %s\n", pv.File))
			b.WriteString(fmt.Sprintf("    [path] %s\n", pv.Reason))
		}
	}

	if len(r.Violations) > 0 {
		b.WriteString("\n  BLOCKED\n")
		for _, v := range r.Violations {
			b.WriteString(fmt.Sprintf("  %s:%d\n", v.File, v.Line))
			b.WriteString(fmt.Sprintf("    [%s] %s: %s\n", v.Tier, v.Rule, v.Redacted))
		}
	}

	if len(r.Warnings) > 0 {
		b.WriteString("\n  WARNINGS (reviewed, allowed)\n")
		for _, w := range r.Warnings {
			b.WriteString(fmt.Sprintf("  %s:%d\n", w.File, w.Line))
			b.WriteString(fmt.Sprintf("    [%s] %s: %s (reviewed)\n", w.Tier, w.Rule, w.Redacted))
		}
	}

	if !r.Passed {
		b.WriteString("\n  Commit blocked. Fix violations and retry.\n")
	}

	return b.String()
}

// CategoryLabel returns a plain-English label for a category.
func CategoryLabel(cat Category) string {
	switch cat {
	case CatEmail:
		return "An email address"
	case CatPhone:
		return "A phone number"
	case CatSSN:
		return "A social security number"
	case CatLocalPath:
		return "A local file path"
	case CatAPIKey:
		return "An API key"
	case CatAWSKey:
		return "An AWS access key"
	case CatPrivateKey:
		return "A private key"
	case CatHardBlock:
		return "A blocklisted term"
	case CatSoftBlock:
		return "A blocklisted term (soft)"
	case CatPathBlock:
		return "Not in allowed directories"
	default:
		return string(cat)
	}
}

// FormatFriendly returns a user-friendly output with allow commands.
func (r *ScanResult) FormatFriendly() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("SAME Guard — %d files scanned\n", r.FilesScanned))

	if r.Passed && len(r.Warnings) == 0 {
		b.WriteString("\n  All clear.\n")
		return b.String()
	}

	// Group violations by category for readable output
	if len(r.Violations) > 0 || len(r.PathViolations) > 0 {
		// Describe what was found
		hasPII := false
		hasBlock := false
		hasPath := false
		for _, v := range r.Violations {
			switch {
			case strings.HasPrefix(string(v.Category), "pii_"):
				hasPII = true
			case v.Category == CatHardBlock || v.Category == CatSoftBlock:
				hasBlock = true
			}
		}
		if len(r.PathViolations) > 0 {
			hasPath = true
		}

		b.WriteString("\n")
		if hasPII {
			b.WriteString("  I found something that looks like personal info:\n\n")
		} else if hasBlock {
			b.WriteString("  I found blocklisted content:\n\n")
		} else if hasPath {
			b.WriteString("  I found files outside allowed directories:\n\n")
		}

		// Show violations
		for _, v := range r.Violations {
			b.WriteString(fmt.Sprintf("  %s:%d\n", v.File, v.Line))
			b.WriteString(fmt.Sprintf("    %s: %s\n\n", CategoryLabel(v.Category), v.Redacted))
		}
		for _, pv := range r.PathViolations {
			b.WriteString(fmt.Sprintf("  %s\n", pv.File))
			b.WriteString(fmt.Sprintf("    %s\n\n", CategoryLabel(CatPathBlock)))
		}

		b.WriteString("  This only affects git commits — your notes are never touched.\n\n")

		// Show allow commands
		b.WriteString("  To allow these and commit:\n")
		for _, v := range r.Violations {
			b.WriteString(fmt.Sprintf("    same guard allow --file %s --match %q\n", v.File, v.Redacted))
		}
		b.WriteString("\n  Or allow everything from the last scan:\n")
		b.WriteString("    same guard allow --last\n")
	}

	// Show warnings if present
	if len(r.Warnings) > 0 {
		b.WriteString("\n  Warnings (non-blocking):\n")
		for _, w := range r.Warnings {
			b.WriteString(fmt.Sprintf("  %s:%d\n", w.File, w.Line))
			label := CategoryLabel(w.Category)
			if w.Reviewed {
				label += " (reviewed)"
			}
			b.WriteString(fmt.Sprintf("    %s: %s\n", label, w.Redacted))
		}
	}

	return b.String()
}

// LastScanViolation extends Violation with the original match text for the allow flow.
// This is stored only in the internal last-scan cache, never in user-facing output.
type LastScanViolation struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Tier     Tier     `json:"tier"`
	Category Category `json:"category"`
	Rule     string   `json:"rule"`
	Redacted string   `json:"redacted"`
	Match    string   `json:"match"`
	Reviewed bool     `json:"reviewed,omitempty"`
}

// LastScan is the cached result of the most recent scan, for the allow flow.
type LastScan struct {
	Violations     []LastScanViolation `json:"violations"`
	PathViolations []PathViolation     `json:"path_violations,omitempty"`
}

// lastScanPath returns the path to the last-scan cache file.
func lastScanPath(vaultPath string) string {
	return filepath.Join(vaultPath, ".same", "last-scan.json")
}

// SaveLastScan writes the scan's violations to disk for the allow flow.
func SaveLastScan(vaultPath string, result *ScanResult) error {
	// Convert Violations to LastScanViolations, preserving Match for the allow flow
	lsViolations := make([]LastScanViolation, len(result.Violations))
	for i, v := range result.Violations {
		lsViolations[i] = LastScanViolation{
			File:     v.File,
			Line:     v.Line,
			Tier:     v.Tier,
			Category: v.Category,
			Rule:     v.Rule,
			Redacted: v.Redacted,
			Match:    v.Match,
			Reviewed: v.Reviewed,
		}
	}
	ls := LastScan{
		Violations:     lsViolations,
		PathViolations: result.PathViolations,
	}
	path := lastScanPath(vaultPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadLastScan reads the cached last scan from disk.
func LoadLastScan(vaultPath string) (*LastScan, error) {
	data, err := os.ReadFile(lastScanPath(vaultPath))
	if err != nil {
		return nil, err
	}
	var ls LastScan
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, err
	}
	return &ls, nil
}
