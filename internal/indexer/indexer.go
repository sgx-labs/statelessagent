package indexer

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sgx-labs/statelessagent/internal/config"
	"github.com/sgx-labs/statelessagent/internal/embedding"
	"github.com/sgx-labs/statelessagent/internal/graph"
	"github.com/sgx-labs/statelessagent/internal/llm"
	"github.com/sgx-labs/statelessagent/internal/memory"
	"github.com/sgx-labs/statelessagent/internal/store"
)

// Version is set by cmd/same to record which SAME version performed the reindex.
var Version string

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
	Content    []byte
	Path       string
	Err        error
}

var errNoEmbeddingsForFile = errors.New("no embeddings generated for file")

// Reindex walks the vault, builds records, embeds them, and stores in the database.
func Reindex(db *store.DB, force bool) (*Stats, error) {
	return ReindexWithProgress(db, force, nil)
}

// ReindexWithProgress is like Reindex but accepts an optional progress callback.
func ReindexWithProgress(db *store.DB, force bool, progress ProgressFunc) (*Stats, error) {
	vaultPath := config.VaultPath()
	ec := config.EmbeddingProviderConfig()
	provCfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
	}
	// For ollama provider, use the legacy [ollama] URL if no base_url is set
	if (provCfg.Provider == "ollama" || provCfg.Provider == "") && provCfg.BaseURL == "" {
		ollamaURL, err := config.OllamaURL()
		if err != nil {
			return nil, fmt.Errorf("ollama URL: %w", err)
		}
		provCfg.BaseURL = ollamaURL
	}
	embedClient, err := embedding.NewProvider(provCfg)
	if err != nil {
		return nil, fmt.Errorf("embedding provider: %w", err)
	}

	// Initialize Graph Extractor
	graphDB := graph.NewDB(db.Conn())
	extractor := graph.NewExtractor(graphDB)

	// Configure optional graph LLM extraction according to policy.
	switch config.GraphLLMMode() {
	case "off":
		// Regex-only graph extraction.
	case "local-only":
		if chatClient, err := llm.NewClientWithOptions(llm.Options{LocalOnly: true}); err == nil {
			if model, modelErr := chatClient.PickBestModel(); modelErr == nil && model != "" {
				extractor.SetLLM(chatClient, model)
			}
		}
	case "on":
		if chatClient, err := llm.NewClient(); err == nil {
			if model, modelErr := chatClient.PickBestModel(); modelErr == nil && model != "" {
				extractor.SetLLM(chatClient, model)
			}
		}
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

	// Build work queue of files that need indexing.
	// In incremental mode, we read file content to check the hash. Cache the
	// content so buildRecords doesn't need to re-read it (saves one syscall per file).
	type fileWork struct {
		path    string
		relPath string
		content []byte // cached content from hash check (nil in force mode)
	}
	var work []fileWork
	const largeNoteThreshold = 30 * 1024 // 30KB
	for _, fp := range mdFiles {
		relPath := relativePath(fp, vaultPath)

		if !force {
			content, err := os.ReadFile(fp)
			if err != nil {
				stats.Errors++
				continue
			}
			if len(content) > largeNoteThreshold {
				fmt.Fprintf(os.Stderr, "same: warning: %s is %dKB — large notes reduce search quality\n",
					relPath, len(content)/1024)
			}
			hash := sha256Hash(string(content))
			if existing, ok := existingHashes[relPath]; ok && existing == hash {
				stats.SkippedUnchanged++
				continue
			}
			work = append(work, fileWork{path: fp, relPath: relPath, content: content})
		} else {
			work = append(work, fileWork{path: fp, relPath: relPath})
		}
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
				records, embeddings, content, err := buildRecordsWithContent(w.path, w.relPath, vaultPath, embedClient, w.content)
				resultCh <- embResult{
					Records:    records,
					Embeddings: embeddings,
					Content:    content,
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
	embeddingFileFailures := 0
	for result := range resultCh {
		if result.Err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s: %v\n", result.Path, result.Err)
			if errors.Is(result.Err, errNoEmbeddingsForFile) {
				embeddingFileFailures++
			}
			stats.Errors++
			continue
		}
		if len(result.Records) == 0 {
			continue
		}

		// For incremental mode, delete old chunks for this path first
		if !force {
			if err := db.DeleteByPath(result.Path); err != nil {
				fmt.Fprintf(os.Stderr, "  [ERROR] delete %s: %v\n", result.Path, err)
				stats.Errors++
				continue
			}
		}

		insertedIDs, err := db.BulkInsertNotes(result.Records, result.Embeddings)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] storing %s: %v\n", result.Path, err)
			stats.Errors++
			continue
		}

		// Graph Extraction
		if rootID, ok := insertedIDs[result.Path]; ok {
			agent := ""
			if len(result.Records) > 0 {
				agent = result.Records[0].Agent
			}
			// Best effort extraction
			_ = extractor.ExtractFromNote(rootID, result.Path, string(result.Content), agent)
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

	// If every file selected for full reindex failed to produce embeddings,
	// surface an error so caller can fall back to keyword-only mode.
	if len(work) > 0 && stats.NewlyIndexed == 0 && embeddingFileFailures == len(work) {
		return nil, fmt.Errorf("embedding backend unavailable: failed to embed any indexed files")
	}

	// Record embedding metadata so mismatch guard can detect config changes.
	// Use embedClient.Model() (resolved name) so it matches CheckEmbeddingMeta
	// which also uses client.Model(). Previously stored ec.Model which could be
	// an empty string, causing false mismatch errors.
	embedName := embedClient.Name()
	embedModel := embedClient.Model()
	embedDims := embedClient.Dimensions()
	_ = db.SetEmbeddingMeta(embedName, embedModel, embedDims)
	_ = db.SetMeta("index_mode", "full")

	// Record reindex timestamp and version for doctor diagnostics
	_ = db.SetMeta("last_reindex_time", time.Now().UTC().Format(time.RFC3339))
	if Version != "" {
		_ = db.SetMeta("same_version", Version)
	}

	// Rebuild FTS5 index after bulk insert
	_ = db.RebuildFTS()

	// Prune old usage data (90 days)
	_, _ = db.PruneUsageData(90)

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
	result["embedding_dimensions"] = config.EmbeddingDim()
	enrichStats(result)
	return result
}

// enrichStats adds database file size and last reindex time.
func enrichStats(result map[string]interface{}) {
	dbPath := config.DBPath()
	if info, err := os.Stat(dbPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		result["db_size_mb"] = fmt.Sprintf("%.1f", sizeMB)
		result["db_path"] = filepath.Base(dbPath)
	}

	// Last reindex time from index_stats.json mtime
	statsPath := filepath.Join(config.DataDir(), "index_stats.json")
	if info, err := os.Stat(statsPath); err == nil {
		result["last_reindex"] = info.ModTime().Format("2006-01-02 15:04:05")
	}
}

// IndexSingleFile indexes (or re-indexes) a single file into the database.
// Deletes any existing chunks for the file's relative path, then inserts new ones.
// This avoids the overhead of a full vault reindex when only one file changed.
func IndexSingleFile(database *store.DB, filePath, relPath, vaultPath string, embedClient embedding.Provider) error {
	records, embeddings, content, err := buildRecords(filePath, relPath, vaultPath, embedClient)
	if err != nil {
		return fmt.Errorf("build records: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	// Remove old chunks for this path before inserting new ones
	if err := database.DeleteByPath(relPath); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	insertedIDs, err := database.BulkInsertNotes(records, embeddings)
	if err != nil {
		return fmt.Errorf("insert notes: %w", err)
	}

	// Graph Extraction
	// Basic extractor without LLM for single-file update speed
	graphDB := graph.NewDB(database.Conn())
	extractor := graph.NewExtractor(graphDB)
	if rootID, ok := insertedIDs[relPath]; ok {
		agent := ""
		if len(records) > 0 {
			agent = records[0].Agent
		}
		_ = extractor.ExtractFromNote(rootID, relPath, string(content), agent)
	}

	// Rebuild FTS for the updated content
	_ = database.RebuildFTS()

	return nil
}

// IndexSingleFileLite indexes (or re-indexes) a single file without embeddings.
// Used by watcher mode when provider="none" (keyword-only mode).
func IndexSingleFileLite(database *store.DB, filePath, relPath, vaultPath string) error {
	records, content, err := buildRecordsLite(filePath, relPath, vaultPath)
	if err != nil {
		return fmt.Errorf("build records lite: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	if err := database.DeleteByPath(relPath); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	insertedIDs, err := database.BulkInsertNotesLite(records)
	if err != nil {
		return fmt.Errorf("insert notes lite: %w", err)
	}

	graphDB := graph.NewDB(database.Conn())
	extractor := graph.NewExtractor(graphDB)
	if rootID, ok := insertedIDs[relPath]; ok {
		agent := ""
		if len(records) > 0 {
			agent = records[0].Agent
		}
		_ = extractor.ExtractFromNote(rootID, relPath, string(content), agent)
	}

	_ = database.RebuildFTS()
	return nil
}

// BuildRecordsForFile builds note records and embeddings for a single file.
// Exported for use by the watcher.
func BuildRecordsForFile(filePath, relPath, vaultPath string, embedClient embedding.Provider) ([]store.NoteRecord, [][]float32, error) {
	recs, embs, _, err := buildRecords(filePath, relPath, vaultPath, embedClient)
	return recs, embs, err
}

func buildRecords(filePath, relPath, vaultPath string, embedClient embedding.Provider) ([]store.NoteRecord, [][]float32, []byte, error) {
	return buildRecordsWithContent(filePath, relPath, vaultPath, embedClient, nil)
}

// buildRecordsWithContent builds records, optionally using pre-read content to avoid a second read.
func buildRecordsWithContent(filePath, relPath, vaultPath string, embedClient embedding.Provider, cachedContent []byte) ([]store.NoteRecord, [][]float32, []byte, error) {
	content := cachedContent
	if content == nil {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read file: %w", err)
		}
	}

	parsed := ParseNote(string(content))
	meta := parsed.Meta
	body := parsed.Body

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stat file: %w", err)
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
	embedFailures := 0

	for i, chunk := range chunks {
		embedText := title + "\n" + chunk.Text
		if len(embedText) > config.MaxEmbedChars {
			embedText = embedText[:config.MaxEmbedChars]
		}

		vec, err := embedClient.GetDocumentEmbedding(embedText)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] Failed to embed %s chunk %d: %v\n", relPath, i, err)
			embedFailures++
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
			Agent:        strings.TrimSpace(meta.Agent),
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

	if len(chunks) > 0 && len(records) == 0 && embedFailures == len(chunks) {
		return nil, nil, content, fmt.Errorf("%w: %s", errNoEmbeddingsForFile, relPath)
	}

	return records, embeddings, content, nil
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
		if strings.HasSuffix(d.Name(), ".md") && !config.SkipFiles[d.Name()] {
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

// ReindexLite indexes vault notes WITHOUT generating embeddings (FTS5-only mode).
// Used when Ollama is unavailable. Notes are parsed, chunked, and stored for keyword search.
func ReindexLite(db *store.DB, force bool, progress ProgressFunc) (*Stats, error) {
	vaultPath := config.VaultPath()
	mdFiles := walkVault(vaultPath)
	stats := &Stats{
		TotalFiles: len(mdFiles),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	var existingHashes map[string]string
	if !force {
		var err error
		existingHashes, err = db.GetContentHashes()
		if err != nil {
			existingHashes = make(map[string]string)
		}
	}

	if force {
		if err := db.DeleteAllNotes(); err != nil {
			return nil, fmt.Errorf("clear existing data: %w", err)
		}
	}

	for i, fp := range mdFiles {
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
				if progress != nil {
					progress(i+1, stats.TotalFiles, relPath)
				}
				continue
			}
		}

		if !force {
			if err := db.DeleteByPath(relPath); err != nil {
				fmt.Fprintf(os.Stderr, "  [ERROR] delete %s: %v\n", relPath, err)
				stats.Errors++
				continue
			}
		}

		records, content, err := buildRecordsLite(fp, relPath, vaultPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s: %v\n", relPath, err)
			stats.Errors++
			continue
		}

		if len(records) > 0 {
			insertedIDs, err := db.BulkInsertNotesLite(records)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [ERROR] storing %s: %v\n", relPath, err)
				stats.Errors++
				continue
			}

			// Graph Extraction
			graphDB := graph.NewDB(db.Conn())
			extractor := graph.NewExtractor(graphDB)
			if rootID, ok := insertedIDs[relPath]; ok {
				agent := ""
				if len(records) > 0 {
					agent = records[0].Agent
				}
				_ = extractor.ExtractFromNote(rootID, relPath, string(content), agent)
			}
		}

		stats.NewlyIndexed++
		if progress != nil {
			progress(i+1, stats.TotalFiles, relPath)
		}
	}

	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	stats.NotesInIndex = noteCount
	stats.ChunksInIndex = chunkCount

	_ = db.SetMeta("last_reindex_time", time.Now().UTC().Format(time.RFC3339))
	_ = db.SetMeta("index_mode", "lite")
	if Version != "" {
		_ = db.SetMeta("same_version", Version)
	}

	_ = db.RebuildFTS()
	saveStats(stats)

	return stats, nil
}

// buildRecordsLite builds note records WITHOUT embeddings.
func buildRecordsLite(filePath, relPath, vaultPath string) ([]store.NoteRecord, []byte, error) {
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

	var chunks []Chunk
	if len(body) > config.ChunkTokenThreshold {
		chunks = ChunkByHeadings(body)
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
	for i, chunk := range chunks {
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
			Agent:        strings.TrimSpace(meta.Agent),
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
	}

	return records, content, nil
}

func saveStats(stats *Stats) {
	dataDir := config.DataDir()
	os.MkdirAll(dataDir, 0o755)
	data, _ := json.MarshalIndent(stats, "", "  ")
	os.WriteFile(filepath.Join(dataDir, "index_stats.json"), data, 0o644)
}
