package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSameignoreString_BasicPatterns(t *testing.T) {
	content := `# Comment line
node_modules/
*.pyc
.git/

# Another comment
*.exe
package-lock.json
`

	ip := ParseSameignoreString(content)
	if ip == nil {
		t.Fatal("expected non-nil IgnorePatterns")
	}

	if ip.PatternCount() != 5 {
		t.Fatalf("expected 5 patterns, got %d", ip.PatternCount())
	}
}

func TestParseSameignoreString_EmptyAndComments(t *testing.T) {
	content := `# Only comments

# And blank lines

`
	ip := ParseSameignoreString(content)
	if ip.PatternCount() != 0 {
		t.Fatalf("expected 0 patterns, got %d", ip.PatternCount())
	}
}

func TestShouldIgnore_DirectoryPatterns(t *testing.T) {
	ip := ParseSameignoreString("node_modules/\n.git/\n")

	tests := []struct {
		path   string
		isDir  bool
		expect bool
	}{
		{"node_modules", true, true},
		{"node_modules", false, false}, // dir pattern doesn't match files
		{"src/node_modules", true, true},
		{".git", true, true},
		{"project/.git", true, true},
		{"readme.md", false, false},
	}

	for _, tt := range tests {
		got := ip.ShouldIgnore(tt.path, tt.isDir)
		if got != tt.expect {
			t.Errorf("ShouldIgnore(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
		}
	}
}

func TestShouldIgnore_FilePatterns(t *testing.T) {
	ip := ParseSameignoreString("*.pyc\n*.exe\npackage-lock.json\n")

	tests := []struct {
		path   string
		isDir  bool
		expect bool
	}{
		{"test.pyc", false, true},
		{"subdir/cache.pyc", false, true},
		{"app.exe", false, true},
		{"package-lock.json", false, true},
		{"subdir/package-lock.json", false, true},
		{"notes.md", false, false},
		{"readme.txt", false, false},
	}

	for _, tt := range tests {
		got := ip.ShouldIgnore(tt.path, tt.isDir)
		if got != tt.expect {
			t.Errorf("ShouldIgnore(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
		}
	}
}

func TestShouldIgnore_ExtensionPatterns(t *testing.T) {
	ip := ParseSameignoreString("*.png\n*.jpg\n*.min.js\n*.tar.gz\n")

	tests := []struct {
		path   string
		expect bool
	}{
		{"logo.png", true},
		{"assets/icon.jpg", true},
		{"dist/app.min.js", true},
		{"app.js", false},
		{"notes.md", false},
	}

	for _, tt := range tests {
		got := ip.ShouldIgnore(tt.path, false)
		if got != tt.expect {
			t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.expect)
		}
	}
}

func TestShouldIgnore_NilPatterns(t *testing.T) {
	var ip *IgnorePatterns

	if ip.ShouldIgnore("anything.md", false) {
		t.Error("nil IgnorePatterns should not ignore anything")
	}
}

func TestShouldIgnore_EmptyPatterns(t *testing.T) {
	ip := ParseSameignoreString("")

	if ip.ShouldIgnore("anything.md", false) {
		t.Error("empty patterns should not ignore anything")
	}
}

func TestShouldIgnore_PathPatterns(t *testing.T) {
	ip := ParseSameignoreString("coverage/\n.nyc_output/\n")

	tests := []struct {
		path   string
		isDir  bool
		expect bool
	}{
		{"coverage", true, true},
		{"project/coverage", true, true},
		{"coverage/lcov.info", false, false}, // coverage/ is dir-only pattern
		{".nyc_output", true, true},
	}

	for _, tt := range tests {
		got := ip.ShouldIgnore(tt.path, tt.isDir)
		if got != tt.expect {
			t.Errorf("ShouldIgnore(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
		}
	}
}

func TestLoadSameignore_FileExists(t *testing.T) {
	tmpDir := t.TempDir()

	content := "node_modules/\n*.pyc\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".sameignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ip := LoadSameignore(tmpDir)
	if ip == nil {
		t.Fatal("expected non-nil IgnorePatterns")
	}
	if ip.PatternCount() != 2 {
		t.Fatalf("expected 2 patterns, got %d", ip.PatternCount())
	}
}

func TestLoadSameignore_FileNotExists(t *testing.T) {
	tmpDir := t.TempDir()

	ip := LoadSameignore(tmpDir)
	if ip != nil {
		t.Error("expected nil for missing .sameignore")
	}
}

func TestPatterns_ReturnsCopy(t *testing.T) {
	ip := ParseSameignoreString("node_modules/\n*.pyc\n.git/\n")

	patterns := ip.Patterns()
	if len(patterns) != 3 {
		t.Fatalf("expected 3 patterns, got %d", len(patterns))
	}

	// Verify directory patterns have trailing slash restored
	if patterns[0] != "node_modules/" {
		t.Errorf("expected 'node_modules/', got %q", patterns[0])
	}
	if patterns[1] != "*.pyc" {
		t.Errorf("expected '*.pyc', got %q", patterns[1])
	}
	if patterns[2] != ".git/" {
		t.Errorf("expected '.git/', got %q", patterns[2])
	}
}

func TestWriteSameignore(t *testing.T) {
	tmpDir := t.TempDir()
	content := "node_modules/\n*.pyc\n"

	if err := WriteSameignore(tmpDir, content); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".sameignore"))
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != content {
		t.Errorf("written content mismatch: got %q, want %q", string(data), content)
	}
}

func TestAddPattern(t *testing.T) {
	tmpDir := t.TempDir()

	// Add to non-existent file
	if err := AddPattern(tmpDir, "*.log"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".sameignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "*.log") {
		t.Error("pattern not found in file")
	}

	// Add a second pattern
	if err := AddPattern(tmpDir, "tmp/"); err != nil {
		t.Fatal(err)
	}

	data, err = os.ReadFile(filepath.Join(tmpDir, ".sameignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tmp/") {
		t.Error("second pattern not found in file")
	}

	// Try adding duplicate (should not duplicate)
	if err := AddPattern(tmpDir, "*.log"); err != nil {
		t.Fatal(err)
	}

	data, err = os.ReadFile(filepath.Join(tmpDir, ".sameignore"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(data), "*.log")
	if count != 1 {
		t.Errorf("pattern duplicated: found %d occurrences", count)
	}
}

func TestDefaultSameignore_Parseable(t *testing.T) {
	ip := ParseSameignoreString(DefaultSameignore)
	if ip == nil {
		t.Fatal("failed to parse default .sameignore")
	}
	if ip.PatternCount() == 0 {
		t.Error("default .sameignore has no patterns")
	}

	// Verify some key patterns are present
	patterns := ip.Patterns()
	patternSet := make(map[string]bool)
	for _, p := range patterns {
		patternSet[p] = true
	}

	expected := []string{
		"node_modules/",
		".git/",
		"*.exe",
		"*.png",
		"package-lock.json",
		".DS_Store",
	}
	for _, e := range expected {
		if !patternSet[e] {
			t.Errorf("expected pattern %q in defaults, not found", e)
		}
	}
}

func TestWalkVault_RespectsSameignore(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .sameignore
	if err := os.WriteFile(filepath.Join(tmpDir, ".sameignore"), []byte("ignored/\n*.skip.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create files
	if err := os.MkdirAll(filepath.Join(tmpDir, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ignored", "secret.md"), []byte("# Secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "good.md"), []byte("# Good\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "bad.skip.md"), []byte("# Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := WalkVaultWithIgnore(tmpDir)

	if len(found) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(found), found)
	}
	if !strings.HasSuffix(found[0], "good.md") {
		t.Errorf("expected good.md, got %s", found[0])
	}
}
