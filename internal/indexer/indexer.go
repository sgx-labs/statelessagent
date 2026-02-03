package indexer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// Stats holds reindex statistics.
type Stats struct {
	TotalFiles       int    `json:"total_files"`
	NewlyIndexed     int    `json:"newly_indexed"`
	SkippedUnchanged int    `json:"skipped_unchanged"`
	Errors           int    `json:"errors"`
	NotesInIndex     int    `json:"total_notes_in_index"`
	ChunksInIndex    int    `json:"total_chunks_in_index"`
	Timestamp        string `json:"timestamp"`
}

// ProgressFunc is called during indexing to report progress.
// current is the number of files processed so far, total is the total count,
// and path is the file being processed.
type ProgressFunc func(current, total int, path string)

// embResult holds the result of embedding a single file.
type embResult struct {
	Records    []store.NoteRecord
	Embeddings [][]float32
	Path       string
	Err        error
}

// Reindex walks the vault, builds records, embeds them, and stores in the database.
func Reindex(db *store.DB, force bool) (*Stats, error) {
	return ReindexWithProgress(db, force, nil)
}

// ReindexWithProgress is like Reindex but accepts an optional progress callback.
func ReindexWithProgress(db *store.DB, force bool, progress ProgressFunc) (*Stats, error) {
	vaultPath := config.VaultPath()
	ec := config.EmbeddingProviderConfig()
	embedClient, err := embedding.NewProvider(embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    config.OllamaURL(),
		Dimensions: ec.Dimensions,
	})
	if err != nil {
		return nil, fmt.Errorf("embedding provider: %w", err)
	}

	mdFiles := walkVault(vaultPath)
	stats := &Stats{
		TotalFiles: len(mdFiles),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	// Get existing hashes for incremental mode
	var existingHashes map[string]string
	if !force {
		var err error
		existingHashes, err = db.GetContentHashes()
		if err != nil {
			existingHashes = make(map[string]string)
		}
	}

	// If force, clear everything first
	if force {
		if err := db.DeleteAllNotes(); err != nil {
			return nil, fmt.Errorf("clear existing data: %w", err)
		}
	}

	// Build work queue of files that need indexing
	type fileWork struct {
		path    string
		relPath string
	}
	var work []fileWork
	for _, fp := range mdFiles {
		relPath := relativePath(fp, vaultPath)

		if !force {
			content, err := os.ReadFile(fp)
			if err != nil {
				stats.Errors++
				continue
			}
			hash := sha256Hash(string(content))
			if existing, ok := existingHashes[relPath]; ok && existing == hash {
				stats.SkippedUnchanged++
				continue
			}
		}

		work = append(work, fileWork{path: fp, relPath: relPath})
	}

	// Process files with a worker pool (4 goroutines)
	const numWorkers = 4
	workCh := make(chan fileWork, len(work))
	resultCh := make(chan embResult, len(work))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range workCh {
				records, embeddings, err := buildRecords(w.path, w.relPath, vaultPath, embedClient)
				resultCh <- embResult{
					Records:    records,
					Embeddings: embeddings,
					Path:       w.relPath,
					Err:        err,
				}
			}
		}()
	}

	for _, w := range work {
		workCh <- w
	}
	close(workCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results and insert
	for result := range resultCh {
		if result.Err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s: %v\n", result.Path, result.Err)
			stats.Errors++
			continue
		}
		if len(result.Records) == 0 {
			continue
		}

		// For incremental mode, delete old chunks for this path first
		if !force {
			db.DeleteByPath(result.Path)
		}

		if err := db.BulkInsertNotes(result.Records, result.Embeddings); err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] storing %s: %v\n", result.Path, err)
			stats.Errors++
			continue
		}

		stats.NewlyIndexed++
		processed := stats.NewlyIndexed + stats.SkippedUnchanged + stats.Errors
		if progress != nil {
			progress(processed, stats.TotalFiles, result.Path)
		} else {
			fmt.Fprintf(os.Stderr, "  [%d/%d] Indexed: %s (%d chunks)\n",
				processed, stats.TotalFiles, result.Path, len(result.Records))
		}
	}

	// Update final counts
	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	stats.NotesInIndex = noteCount
	stats.ChunksInIndex = chunkCount

	// Save stats to file
	saveStats(stats)

	return stats, nil
}

// GetStats reads the last saved index stats.
func GetStats(db *store.DB) map[string]interface{} {
	statsPath := filepath.Join(config.DataDir(), "index_stats.json")
	data, err := os.ReadFile(statsPath)
	if err != nil {
		// Try to get live counts
		noteCount, err1 := db.NoteCount()
		chunkCount, err2 := db.ChunkCount()
		if err1 == nil && err2 == nil {
			result := map[string]interface{}{
				"total_notes_in_index":  noteCount,
				"total_chunks_in_index": chunkCount,
				"status":                "live query (no saved stats)",
			}
			enrichStats(result)
			return result
		}
		return map[string]interface{}{
			"status": "no index found",
			"hint":   "run 'same reindex' first",
		}
	}

	var result map[string]interface{}
	json.Unmarshal(data, &result)
	result["embedding_model"] = config.EmbeddingModel
	result["embedding_dimensions"] = config.EmbeddingDim
	enrichStats(result)
	return result
}

// enrichStats adds database file size and last reindex time.
func enrichStats(result map[string]interface{}) {
	dbPath := config.DBPath()
	if info, err := os.Stat(dbPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		result["db_size_mb"] = fmt.Sprintf("%.1f", sizeMB)
		result["db_path"] = dbPath
	}

	// Last reindex time from index_stats.json mtime
	statsPath := filepath.Join(config.DataDir(), "index_stats.json")
	if info, err := os.Stat(statsPath); err == nil {
		result["last_reindex"] = info.ModTime().Format("2006-01-02 15:04:05")
	}
}

// BuildRecordsForFile builds note records and embeddings for a single file.
// Exported for use by the watcher.
func BuildRecordsForFile(filePath, relPath, vaultPath string, embedClient embedding.Provider) ([]store.NoteRecord, [][]float32, error) {
	return buildRecords(filePath, relPath, vaultPath, embedClient)
}

func buildRecords(filePath, relPath, vaultPath string, embedClient embedding.Provider) ([]store.NoteRecord, [][]float32, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read file: %w", err)
	}

	parsed := ParseNote(string(content))
	meta := parsed.Meta
	body := parsed.Body

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file: %w", err)
	}
	mtime := float64(info.ModTime().Unix())
	contentHash := sha256Hash(body)

	title := meta.Title
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(filePath), ".md")
	}

	tagsJSON, _ := json.Marshal(meta.Tags)
	if meta.Tags == nil {
		tagsJSON = []byte("[]")
	}

	contentType := memory.InferContentType(relPath, meta.ContentType, meta.Tags)
	reviewBy := strings.TrimSpace(meta.ReviewBy)
	confidence := memory.ComputeConfidence(contentType, mtime, 0, reviewBy != "")

	// Determine chunks
	var chunks []Chunk
	if len(body) > config.ChunkTokenThreshold {
		chunks = ChunkByHeadings(body)
		// If any chunk is still too large, split further
		var final []Chunk
		for _, c := range chunks {
			if len(c.Text) > config.MaxEmbedChars {
				final = append(final, ChunkBySize(c.Text, config.MaxEmbedChars)...)
			} else {
				final = append(final, c)
			}
		}
		chunks = final
	} else {
		chunks = []Chunk{{Heading: "(full)", Text: body}}
	}

	var records []store.NoteRecord
	var embeddings [][]float32

	for i, chunk := range chunks {
		embedText := title + "\n" + chunk.Text
		if len(embedText) > config.MaxEmbedChars {
			embedText = embedText[:config.MaxEmbedChars]
		}

		vec, err := embedClient.GetDocumentEmbedding(embedText)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] Failed to embed %s chunk %d: %v\n", relPath, i, err)
			continue
		}

		text := chunk.Text
		if len(text) > 10000 {
			text = text[:10000]
		}

		records = append(records, store.NoteRecord{
			Path:         relPath,
			Title:        title,
			Tags:         string(tagsJSON),
			Domain:       meta.Domain,
			Workstream:   meta.Workstream,
			ChunkID:      i,
			ChunkHeading: chunk.Heading,
			Text:         text,
			Modified:     mtime,
			ContentHash:  contentHash,
			ContentType:  contentType,
			ReviewBy:     reviewBy,
			Confidence:   confidence,
			AccessCount:  0,
		})
		embeddings = append(embeddings, vec)
	}

	return records, embeddings, nil
}

// WalkVault returns all markdown file paths in the vault, respecting skip dirs.
func WalkVault(vaultPath string) []string {
	return walkVault(vaultPath)
}

// CountMarkdownFiles returns the number of .md files in a directory.
func CountMarkdownFiles(dir string) int {
	return len(walkVault(dir))
}

func walkVault(vaultPath string) []string {
	var files []string
	filepath.WalkDir(vaultPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if config.SkipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func relativePath(filePath, vaultPath string) string {
	rel, err := filepath.Rel(vaultPath, filePath)
	if err != nil {
		return filePath
	}
	return filepath.ToSlash(rel)
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

func saveStats(stats *Stats) {
	dataDir := config.DataDir()
	os.MkdirAll(dataDir, 0o755)
	data, _ := json.MarshalIndent(stats, "", "  ")
	os.WriteFile(filepath.Join(dataDir, "index_stats.json"), data, 0o644)
}
