package guard

import (
	"bytes"
	"os/exec"
	"strings"
)

// ContentReader is a function that returns the content of a file by path.
type ContentReader func(file string) ([]byte, error)

// Scanner is the main guard scanner that runs the full pipeline.
type Scanner struct {
	VaultPath     string
	CustomPaths   []string // additional allowed paths
	ContentReader ContentReader
	Config        GuardConfig
	patterns      []Pattern
	blockTerms    []CompiledTerm
	reviewed      *ReviewedTerms
}

// NewScanner creates a scanner with the given vault path.
// It loads config, blocklist, and reviewed terms from their expected locations.
func NewScanner(vaultPath string) (*Scanner, error) {
	cfg := LoadGuardConfig()
	return NewScannerWithConfig(vaultPath, cfg)
}

// NewScannerWithConfig creates a scanner with explicit config (useful for testing).
func NewScannerWithConfig(vaultPath string, cfg GuardConfig) (*Scanner, error) {
	patterns := builtinPatterns()
	if cfg.Enabled && cfg.PII.Enabled {
		patterns = FilterByConfig(patterns, cfg.EnabledPatternNames())
	} else if cfg.Enabled {
		patterns = nil // PII disabled but guard on
	} else {
		patterns = nil // guard disabled entirely
	}

	s := &Scanner{
		VaultPath:     vaultPath,
		Config:        cfg,
		patterns:      patterns,
		ContentReader: getStagedContent,
	}

	// Load blocklist (optional, skip if blocklist disabled)
	if cfg.Enabled && cfg.Blocklist.Enabled {
		terms, err := LoadBlocklist(blocklistPath(vaultPath))
		if err != nil {
			return nil, err
		}
		s.blockTerms = terms
	}

	// Load reviewed terms (optional)
	reviewed, err := LoadReviewedTerms(vaultPath)
	if err != nil {
		return nil, err
	}
	s.reviewed = reviewed

	return s, nil
}

// ScanStaged scans all git-staged files.
func (s *Scanner) ScanStaged() (*ScanResult, error) {
	files, err := getStagedFiles()
	if err != nil {
		return nil, err
	}
	return s.ScanFiles(files)
}

// ScanFiles scans the given list of file paths through the full pipeline.
func (s *Scanner) ScanFiles(files []string) (*ScanResult, error) {
	result := &ScanResult{
		FilesScanned: len(files),
		Passed:       true,
	}

	// Master switch: when guard is disabled, pass immediately
	if !s.Config.Enabled {
		return result, nil
	}

	reader := s.ContentReader
	if reader == nil {
		reader = getStagedContent
	}

	for _, file := range files {
		// Step 1: Path allowlist (skip if path filter disabled)
		if s.Config.PathFilter.Enabled && !IsPathAllowed(file, s.CustomPaths) {
			result.PathViolations = append(result.PathViolations, PathViolation{
				File:   file,
				Reason: "not in allowed directories",
			})
			result.Passed = false
			continue
		}

		// Step 2: Get file content
		content, err := reader(file)
		if err != nil {
			continue // file might be deleted
		}

		// Step 3: Binary detection — skip files with null bytes in first 8KB
		checkLen := len(content)
		if checkLen > 8192 {
			checkLen = 8192
		}
		if checkLen > 0 && bytes.ContainsRune(content[:checkLen], 0) {
			_ = AppendAudit(s.VaultPath, AuditEntry{
				Action:     "scan_skip_binary",
				FilesCount: 1,
				Passed:     true,
				Details:    file,
			})
			continue
		}

		s.scanContent(file, string(content), result)
	}

	// Audit the scan
	_ = AppendAudit(s.VaultPath, AuditEntry{
		Action:     "scan",
		FilesCount: len(files),
		Passed:     result.Passed,
		Violations: len(result.Violations) + len(result.PathViolations),
	})

	return result, nil
}

// scanContent scans file content line-by-line for blocklist and PII violations.
func (s *Scanner) scanContent(file, text string, result *ScanResult) {
	lines := strings.Split(text, "\n")

	warnSoft := s.Config.SoftMode == "warn"

	for lineNum, line := range lines {
		// Blocklist scan
		for _, bm := range scanBlocklist(line, s.blockTerms) {
			if bm.Tier == TierHard {
				result.Violations = append(result.Violations, Violation{
					File:     file,
					Line:     lineNum + 1,
					Tier:     TierHard,
					Category: bm.Category,
					Rule:     "blocklist:" + bm.Term,
					Redacted: redact(bm.Term),
					Match:    bm.Term,
				})
				result.Passed = false
			} else {
				cat := string(bm.Category)
				if s.reviewed.IsReviewed(bm.Term, file, cat) || warnSoft {
					result.Warnings = append(result.Warnings, Violation{
						File:     file,
						Line:     lineNum + 1,
						Tier:     TierSoft,
						Category: bm.Category,
						Rule:     "blocklist:" + bm.Term,
						Redacted: redact(bm.Term),
						Match:    bm.Term,
						Reviewed: s.reviewed.IsReviewed(bm.Term, file, cat),
					})
				} else {
					result.Violations = append(result.Violations, Violation{
						File:     file,
						Line:     lineNum + 1,
						Tier:     TierSoft,
						Category: bm.Category,
						Rule:     "blocklist:" + bm.Term,
						Redacted: redact(bm.Term),
						Match:    bm.Term,
					})
					result.Passed = false
				}
			}
		}

		// PII patterns
		for _, plr := range scanLine(line, file, s.patterns) {
			cat := string(plr.Pattern.Category)
			isSoft := plr.Pattern.Tier == TierSoft
			reviewed := isSoft && s.reviewed.IsReviewed(plr.Match, file, cat)
			if reviewed || (isSoft && warnSoft) {
				result.Warnings = append(result.Warnings, Violation{
					File:     file,
					Line:     lineNum + 1,
					Tier:     plr.Pattern.Tier,
					Category: plr.Pattern.Category,
					Rule:     plr.Pattern.Name,
					Redacted: plr.Redacted,
					Match:    plr.Match,
					Reviewed: reviewed,
				})
			} else {
				result.Violations = append(result.Violations, Violation{
					File:     file,
					Line:     lineNum + 1,
					Tier:     plr.Pattern.Tier,
					Category: plr.Pattern.Category,
					Rule:     plr.Pattern.Name,
					Redacted: plr.Redacted,
					Match:    plr.Match,
				})
				result.Passed = false
			}
		}
	}
}

// getStagedFiles returns the list of files staged for commit.
func getStagedFiles() ([]string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-only", "--diff-filter=ACMR")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// getStagedContent returns the content of a file from the git staging area.
func getStagedContent(file string) ([]byte, error) {
	cmd := exec.Command("git", "show", ":"+file)
	return cmd.Output()
}
