package seed

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ManifestURL is the default location of the seed manifest.
	ManifestURL = "https://raw.githubusercontent.com/sgx-labs/seed-vaults/main/seeds.json"

	// ManifestCacheTTL is how long a cached manifest is considered fresh.
	ManifestCacheTTL = 1 * time.Hour

	// MaxManifestSize is the maximum manifest download size.
	MaxManifestSize = 1 * 1024 * 1024 // 1 MB
)

// Manifest is the top-level seed registry structure.
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Seeds         []Seed `json:"seeds"`
}

// Seed describes a single installable seed vault.
type Seed struct {
	Name           string   `json:"name"`
	DisplayName    string   `json:"display_name"`
	Description    string   `json:"description"`
	Audience       string   `json:"audience"`
	NoteCount      int      `json:"note_count"`
	SizeKB         int      `json:"size_kb"`
	Tags           []string `json:"tags"`
	MinSameVersion string   `json:"min_same_version"`
	Path           string   `json:"path"`
	Featured       bool     `json:"featured"`
}

// manifestCache wraps a manifest with a timestamp for TTL checking.
type manifestCache struct {
	FetchedAt time.Time `json:"fetched_at"`
	Manifest  Manifest  `json:"manifest"`
}

// manifestCachePath returns the path to the cached manifest file.
func manifestCachePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "same", "seed-manifest.json")
}

// FetchManifest retrieves the seed manifest, using a local cache when fresh.
// Set forceRefresh to bypass the cache.
func FetchManifest(forceRefresh bool) (*Manifest, error) {
	cachePath := manifestCachePath()

	// Try cache first (unless forced)
	if !forceRefresh {
		if m, err := loadCachedManifest(cachePath, false); err == nil {
			return m, nil
		}
	}

	// Fetch from remote
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(ManifestURL)
	if err != nil {
		// If network fails, accept stale cache
		if m, cacheErr := loadCachedManifest(cachePath, true); cacheErr == nil {
			return m, nil
		}
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try stale cache on HTTP errors
		if m, cacheErr := loadCachedManifest(cachePath, true); cacheErr == nil {
			return m, nil
		}
		return nil, fmt.Errorf("fetch manifest: HTTP %d", resp.StatusCode)
	}

	// SECURITY: limit response size
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxManifestSize))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if err := validateManifest(&manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	// Write cache (best effort; network fetch already succeeded)
	if err := saveManifestCache(cachePath, &manifest); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to update seed manifest cache (%s): %v\n", cachePath, err)
	}

	return &manifest, nil
}

// FindSeed looks up a seed by name in the manifest.
func FindSeed(manifest *Manifest, name string) *Seed {
	lower := strings.ToLower(name)
	for i := range manifest.Seeds {
		if strings.ToLower(manifest.Seeds[i].Name) == lower {
			return &manifest.Seeds[i]
		}
	}
	return nil
}

// validateSeedName checks that a seed name is safe for use as a directory name.
func validateSeedName(name string) error {
	if name == "" {
		return fmt.Errorf("seed name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("seed name too long (%d chars, max 64)", len(name))
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return fmt.Errorf("seed name must be lowercase alphanumeric with hyphens (got %q)", name)
		}
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("seed name cannot start or end with a hyphen")
	}
	return nil
}

func validateSeedPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("seed path cannot be empty")
	}
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf("seed path contains null byte")
	}

	normalized := strings.ReplaceAll(path, "\\", "/")
	if len(normalized) >= 3 {
		ch := normalized[0]
		if ((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')) && normalized[1] == ':' && normalized[2] == '/' {
			return fmt.Errorf("seed path must be relative (got %q)", path)
		}
	}
	for i, part := range strings.Split(normalized, "/") {
		if part == ".." || (part == "." && i > 0) {
			return fmt.Errorf("seed path cannot contain dot traversal segments (got %q)", path)
		}
	}

	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(normalized)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("seed path must stay within repository (got %q)", path)
	}

	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("seed path contains invalid segment (got %q)", path)
		}
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("seed path cannot contain hidden segment %q", part)
		}
	}
	return nil
}

func validateManifest(m *Manifest) error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema version: %d", m.SchemaVersion)
	}
	for _, s := range m.Seeds {
		if err := validateSeedName(s.Name); err != nil {
			return fmt.Errorf("invalid seed name %q: %w", s.Name, err)
		}
		if err := validateSeedPath(s.Path); err != nil {
			return fmt.Errorf("invalid seed path for %q: %w", s.Name, err)
		}
	}
	return nil
}

// loadCachedManifest reads and validates the cached manifest.
// Returns nil if the cache is missing, corrupt, or (when allowStale is false) expired.
// Set allowStale to true for network-failure fallback paths.
func loadCachedManifest(path string, allowStale bool) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache manifestCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if !allowStale && time.Since(cache.FetchedAt) > ManifestCacheTTL {
		return nil, fmt.Errorf("cache expired")
	}
	if err := validateManifest(&cache.Manifest); err != nil {
		return nil, err
	}
	return &cache.Manifest, nil
}

// saveManifestCache writes the manifest to the cache file.
func saveManifestCache(path string, m *Manifest) error {
	cache := manifestCache{
		FetchedAt: time.Now(),
		Manifest:  *m,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write cache file: %w", err)
	}
	return nil
}
