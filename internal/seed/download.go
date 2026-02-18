package seed

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// TarballURL is the GitHub API endpoint for downloading the repo tarball.
	TarballURL = "https://api.github.com/repos/sgx-labs/seed-vaults/tarball/main"

	// MaxTarballSize is the maximum tarball download size.
	MaxTarballSize = 50 * 1024 * 1024 // 50 MB

	// MaxFileCount is the maximum number of files to extract.
	MaxFileCount = 500

	// MaxFileSize is the maximum size of a single extracted file.
	MaxFileSize = 10 * 1024 * 1024 // 10 MB

	// HTTPTimeout is the HTTP client timeout for tarball downloads.
	HTTPTimeout = 60 * time.Second
)

// DownloadAndExtract downloads the seed-vaults tarball and extracts only the
// files under seedPath into destDir. Returns the number of files extracted.
func DownloadAndExtract(seedPath, destDir string) (int, error) {
	client := &http.Client{
		Timeout: HTTPTimeout,
		// Follow redirects (GitHub API redirects to a CDN)
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Get(TarballURL)
	if err != nil {
		return 0, fmt.Errorf("download tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download tarball: HTTP %d", resp.StatusCode)
	}

	// SECURITY: limit download size
	limitedBody := io.LimitReader(resp.Body, MaxTarballSize+1)

	return extractTarGz(limitedBody, seedPath, destDir)
}

// extractTarGz reads a gzip-compressed tar stream and extracts files matching
// the seedPath prefix into destDir. Internal for testing.
func extractTarGz(r io.Reader, seedPath, destDir string) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var fileCount int

	// Normalize seed path for matching so manifests like "./foo" remain compatible.
	normalizedSeed := strings.ReplaceAll(seedPath, "\\", "/")
	normalizedSeed = filepath.ToSlash(filepath.Clean(filepath.FromSlash(normalizedSeed)))
	normalizedSeed = strings.TrimPrefix(normalizedSeed, "./")
	if normalizedSeed == "" || normalizedSeed == "." || normalizedSeed == ".." ||
		strings.HasPrefix(normalizedSeed, "../") || strings.HasPrefix(normalizedSeed, "/") {
		return 0, fmt.Errorf("invalid seed path: %q", seedPath)
	}
	seedPrefix := strings.TrimSuffix(normalizedSeed, "/") + "/"

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fileCount, fmt.Errorf("read tar entry: %w", err)
		}

		// Strip first path component (GitHub adds {owner}-{repo}-{sha}/)
		name := header.Name
		idx := strings.Index(name, "/")
		if idx < 0 {
			continue
		}
		name = name[idx+1:]

		// Only extract files under the seed path
		if !strings.HasPrefix(name, seedPrefix) {
			continue
		}

		// Get the relative path within the seed
		relPath := strings.TrimPrefix(name, seedPrefix)
		if relPath == "" {
			continue
		}

		// SECURITY: reject symlinks and hardlinks
		switch header.Typeflag {
		case tar.TypeReg:
			// Regular file — OK
			case tar.TypeDir:
				// Directory — create it
				dirPath, err := validateExtractPath(relPath, destDir)
				if err != nil {
					continue // skip invalid paths
				}
				if err := os.MkdirAll(dirPath, 0o755); err != nil {
					return fileCount, fmt.Errorf("create directory %s: %w", relPath, err)
				}
				continue
		default:
			// Skip symlinks, hardlinks, and anything else
			continue
		}

		// SECURITY: validate the extraction path
		destPath, err := validateExtractPath(relPath, destDir)
		if err != nil {
			continue // skip invalid paths
		}

		// SECURITY: enforce per-file size limit
		if header.Size > MaxFileSize {
			continue
		}

		// SECURITY: enforce total file count
		fileCount++
		if fileCount > MaxFileCount {
			return fileCount - 1, fmt.Errorf("too many files (max %d)", MaxFileCount)
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fileCount, fmt.Errorf("create directory for %s: %w", relPath, err)
		}

		// Extract the file
		if err := extractFile(tr, destPath, header.Size); err != nil {
			return fileCount, fmt.Errorf("extract %s: %w", relPath, err)
		}
	}

	return fileCount, nil
}

// extractFile writes a single tar entry to disk with size limits.
func extractFile(r io.Reader, destPath string, size int64) error {
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	// SECURITY: limit read to declared size + 1 to detect overflow
	n, err := io.Copy(f, io.LimitReader(r, size+1))
	if err != nil {
		_ = f.Close()
		return err
	}
	if n > size {
		_ = f.Close()
		return fmt.Errorf("entry exceeds declared size")
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}
