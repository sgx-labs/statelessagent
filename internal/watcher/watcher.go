// Package watcher monitors a vault for file changes and triggers incremental reindexing.
package watcher

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/indexer"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// Watch starts watching the vault for changes and reindexes modified files.
// It blocks until the context is done or an unrecoverable error occurs.
func Watch(db *store.DB) error {
	vaultPath := config.VaultPath()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	// Add all directories (not skipped ones)
	dirs := walkDirs(vaultPath)
	for _, d := range dirs {
		if err := w.Add(d); err != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] Could not watch %s: %v\n", d, err)
		}
	}

	fmt.Fprintf(os.Stderr, "Watching %d directories in %s\n", len(dirs), vaultPath)
	fmt.Fprintf(os.Stderr, "Press Ctrl+C to stop.\n\n")

	// Debounce: collect changed files over a window before reindexing
	var (
		mu       sync.Mutex
		pending  = make(map[string]bool)
		timer    *time.Timer
	)

	const debounceDelay = 2 * time.Second

	flush := func() {
		mu.Lock()
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		pending = make(map[string]bool)
		mu.Unlock()

		if len(paths) == 0 {
			return
		}

		fmt.Fprintf(os.Stderr, "  Reindexing %d changed file(s)...\n", len(paths))
		reindexFiles(db, paths, vaultPath)
	}

	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}

			// Only care about markdown files (skip meta-docs)
			if !strings.HasSuffix(event.Name, ".md") || config.SkipFiles[filepath.Base(event.Name)] {
				// But watch new directories
				if event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						name := filepath.Base(event.Name)
						if !config.SkipDirs[name] {
							w.Add(event.Name)
						}
					}
				}
				continue
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				mu.Lock()
				pending[event.Name] = true
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(debounceDelay, flush)
				mu.Unlock()
			}

			if event.Has(fsnotify.Remove) {
				relPath := relativePath(event.Name, vaultPath)
				db.DeleteByPath(relPath)
				fmt.Fprintf(os.Stderr, "  Removed from index: %s\n", relPath)
			}

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "  [WARN] Watch error: %v\n", err)
		}
	}
}

func reindexFiles(db *store.DB, paths []string, vaultPath string) {
	ec := config.EmbeddingProviderConfig()
	ollamaURL, err := config.OllamaURL()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [ERROR] ollama URL: %v\n", err)
		return
	}
	embedClient, err := embedding.NewProvider(embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ollamaURL,
		Dimensions: ec.Dimensions,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [ERROR] embedding provider: %v\n", err)
		return
	}

	for _, fp := range paths {
		relPath := relativePath(fp, vaultPath)

		// Delete old chunks
		db.DeleteByPath(relPath)

		// Build new records
		records, embeddings, err := indexer.BuildRecordsForFile(fp, relPath, vaultPath, embedClient)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s: %v\n", relPath, err)
			continue
		}

		if len(records) == 0 {
			continue
		}

		if err := db.BulkInsertNotes(records, embeddings); err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] storing %s: %v\n", relPath, err)
			continue
		}

		fmt.Fprintf(os.Stderr, "  Indexed: %s (%d chunks)\n", relPath, len(records))
	}
}

func walkDirs(root string) []string {
	var dirs []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if config.SkipDirs[name] {
				return filepath.SkipDir
			}
			dirs = append(dirs, path)
		}
		return nil
	})
	return dirs
}

func relativePath(filePath, vaultPath string) string {
	rel, err := filepath.Rel(vaultPath, filePath)
	if err != nil {
		return filePath
	}
	return filepath.ToSlash(rel)
}
