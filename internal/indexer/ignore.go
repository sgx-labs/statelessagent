// Package indexer implements file indexing for SAME vaults.
// This file provides .sameignore support — gitignore-style file exclusion patterns.
package indexer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultSameignore is the default content for .sameignore files created by `same init`.
const DefaultSameignore = `# .sameignore — files and directories to exclude from SAME indexing
# Works like .gitignore: glob patterns, one per line, # for comments

# Build artifacts and dependencies
node_modules/
vendor/
dist/
build/
.next/
target/
__pycache__/
*.pyc
.cache/
.venv/
venv/

# IDE and tool config
.git/
.svn/
.vscode/
.idea/
.kilocode/
.cursor/
.claude/

# Binary and media files
*.exe
*.dll
*.so
*.dylib
*.png
*.jpg
*.jpeg
*.gif
*.svg
*.ico
*.woff
*.woff2
*.ttf
*.mp3
*.mp4
*.zip
*.tar.gz
*.pdf

# Lock files
package-lock.json
yarn.lock
pnpm-lock.yaml
go.sum
Cargo.lock
Gemfile.lock
poetry.lock

# Generated files
*.min.js
*.min.css
*.map
*.bundle.js
coverage/
.nyc_output/

# OS files
.DS_Store
Thumbs.db
`

// IgnorePatterns holds parsed .sameignore patterns for a vault.
type IgnorePatterns struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	pattern string
	isDir   bool // pattern ends with / — only matches directories
}

// LoadSameignore reads and parses a .sameignore file from the vault root.
// Returns nil (no patterns) if the file doesn't exist.
func LoadSameignore(vaultPath string) *IgnorePatterns {
	path := filepath.Join(vaultPath, ".sameignore")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	return ParseSameignore(f)
}

// ParseSameignore parses ignore patterns from a reader (for testing).
func ParseSameignore(r *os.File) *IgnorePatterns {
	return parseSameignoreFromScanner(bufio.NewScanner(r))
}

// ParseSameignoreString parses ignore patterns from a string (for testing).
func ParseSameignoreString(content string) *IgnorePatterns {
	return parseSameignoreFromScanner(bufio.NewScanner(strings.NewReader(content)))
}

func parseSameignoreFromScanner(scanner *bufio.Scanner) *IgnorePatterns {
	ip := &IgnorePatterns{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		p := ignorePattern{pattern: line}
		if strings.HasSuffix(line, "/") {
			p.isDir = true
			p.pattern = strings.TrimSuffix(line, "/")
		}

		ip.patterns = append(ip.patterns, p)
	}

	return ip
}

// ShouldIgnore checks if a path relative to the vault root should be ignored.
// relPath should use forward slashes (e.g., "subdir/file.md").
// isDir should be true if the path is a directory.
func (ip *IgnorePatterns) ShouldIgnore(relPath string, isDir bool) bool {
	if ip == nil || len(ip.patterns) == 0 {
		return false
	}

	// Normalize to forward slashes
	relPath = filepath.ToSlash(relPath)
	name := filepath.Base(relPath)

	for _, p := range ip.patterns {
		// Directory-only patterns only match directories
		if p.isDir && !isDir {
			continue
		}

		// Check if the pattern contains a slash (path pattern vs basename pattern)
		if strings.Contains(p.pattern, "/") {
			// Path pattern: match against the full relative path
			if matchPath(relPath, p.pattern) {
				return true
			}
		} else {
			// Basename pattern: match against any path component
			// For directories, check each component
			if isDir {
				// Match the directory name itself
				if matchGlob(name, p.pattern) {
					return true
				}
			} else {
				// For files, check the filename
				if matchGlob(name, p.pattern) {
					return true
				}
				// Also check each directory component in the path
				parts := strings.Split(relPath, "/")
				if p.isDir {
					// Directory pattern: check path components (not the file itself)
					for _, part := range parts[:len(parts)-1] {
						if matchGlob(part, p.pattern) {
							return true
						}
					}
				}
			}
		}
	}

	return false
}

// matchPath matches a relative path against a pattern that may contain path separators.
func matchPath(relPath, pattern string) bool {
	// Try matching the full path
	if matched, _ := filepath.Match(pattern, relPath); matched {
		return true
	}

	// Try matching against each suffix of the path
	// e.g., pattern "build/" should match "project/build"
	parts := strings.Split(relPath, "/")
	for i := range parts {
		suffix := strings.Join(parts[i:], "/")
		if matched, _ := filepath.Match(pattern, suffix); matched {
			return true
		}
	}

	return false
}

// matchGlob matches a single name against a glob pattern.
func matchGlob(name, pattern string) bool {
	// Handle *.ext patterns (the most common case)
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// PatternCount returns the number of active patterns.
func (ip *IgnorePatterns) PatternCount() int {
	if ip == nil {
		return 0
	}
	return len(ip.patterns)
}

// Patterns returns a copy of the pattern strings (for display).
func (ip *IgnorePatterns) Patterns() []string {
	if ip == nil {
		return nil
	}
	result := make([]string, len(ip.patterns))
	for i, p := range ip.patterns {
		if p.isDir {
			result[i] = p.pattern + "/"
		} else {
			result[i] = p.pattern
		}
	}
	return result
}

// WriteSameignore writes a .sameignore file to the vault root.
// Returns an error if the file cannot be written.
func WriteSameignore(vaultPath, content string) error {
	path := filepath.Join(vaultPath, ".sameignore")
	return os.WriteFile(path, []byte(content), 0o644)
}

// AddPattern appends a pattern to the .sameignore file.
// Creates the file if it doesn't exist.
// Returns an error if the pattern contains newlines (prevents injection of
// multiple patterns via a single call).
func AddPattern(vaultPath, pattern string) error {
	if strings.ContainsAny(pattern, "\n\r") {
		return fmt.Errorf("pattern must not contain newlines")
	}

	path := filepath.Join(vaultPath, ".sameignore")

	// Read existing content
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Check if pattern already exists
	if existing != nil {
		scanner := bufio.NewScanner(strings.NewReader(string(existing)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == pattern {
				return nil // already present
			}
		}
	}

	// Append pattern
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// If file doesn't end with newline, add one
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	_, err = f.WriteString(pattern + "\n")
	return err
}
