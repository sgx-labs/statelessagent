package indexer

import (
	"context"
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

// ErrCanceled is returned when indexing is canceled via context.
var ErrCanceled = errors.New("indexing canceled")

// Stats holds reindex statistics.
type Stats struct {
	TotalFiles       int    `json:"total_files"`
	NewlyIndexed     int    `json:"newly_indexed"`
	SkippedUnchanged int    `json:"skipped_unchanged"`
	Errors           int    `json:"errors"`
	NotesInIndex     int    `json:"total_notes_in_index"`
	ChunksInIndex    int    `json:"total_chunks_in_index"`
	Timestamp        string `json:"timestamp"`
	Canceled         bool   `json:"canceled,omitempty"`
}

// EmbeddingProgress reports the state of a background embedding backfill.
type EmbeddingProgress struct {
	Completed int // Number of notes successfully embedded
	Failed    int // Number of notes that failed embedding
	Total     int // Total notes that need embedding
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
	Meta       NoteMeta
	Err        error
}

var errNoEmbeddingsForFile = errors.New("no embeddings generated for file")

// errEmbeddingSkipped signals that embedding failed for all chunks (likely due
// to size) but records were still built and should be stored via the lite path
// (FTS5 only). The embResult.Records slice is populated; embResult.Embeddings
// is nil.
var errEmbeddingSkipped = errors.New("embedding skipped")

// Reindex walks the vault, builds records, embeds them, and stores in the database.
func Reindex(db *store.DB, force bool) (*Stats, error) {
	return ReindexWithProgress(context.Background(), db, force, nil)
}

// ReindexWithProgress is like Reindex but accepts a context for cancellation
// and an optional progress callback.
func ReindexWithProgress(ctx context.Context, db *store.DB, force bool, progress ProgressFunc) (*Stats, error) {
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
		chatClient, err := llm.NewClientWithOptions(llm.Options{LocalOnly: true})
		if err != nil {
			fmt.Fprintf(os.Stderr, "same: graph LLM unavailable: %v (using regex extraction)\n", err)
		} else {
			model := config.ChatModel()
			if model == "" {
				if m, modelErr := chatClient.PickBestModel(); modelErr != nil || model == "" {
					if modelErr != nil {
						fmt.Fprintf(os.Stderr, "same: no chat model found for graph extraction (using regex)\n")
					}
					model = m
				}
			}
			if model != "" {
				extractor.SetLLM(chatClient, model)
			}
		}
	case "on":
		chatClient, err := llm.NewClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "same: graph LLM unavailable: %v (using regex extraction)\n", err)
		} else {
			model := config.ChatModel()
			if model == "" {
				if m, modelErr := chatClient.PickBestModel(); modelErr != nil || model == "" {
					if modelErr != nil {
						fmt.Fprintf(os.Stderr, "same: no chat model found for graph extraction (using regex)\n")
					}
					model = m
				}
			}
			if model != "" {
				extractor.SetLLM(chatClient, model)
			}
		}
	}

	mdFiles := walkVault(vaultPath)
	stats := &Stats{
		TotalFiles: len(mdFiles),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	// In incremental mode, load existing hashes to skip unchanged files.
	// In force mode, all files are re-indexed — but we do NOT delete upfront
	// to avoid data loss if the reindex fails partway through.
	var existingHashes map[string]string
	if !force {
		var err error
		existingHashes, err = db.GetContentHashes()
		if err != nil {
			existingHashes = make(map[string]string)
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
	currentPaths := make(map[string]bool, len(mdFiles))
	const largeNoteThreshold = 30 * 1024 // 30KB
	for _, fp := range mdFiles {
		relPath := relativePath(fp, vaultPath)
		currentPaths[relPath] = true

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

	// Fail fast when embeddings are unavailable, instead of emitting per-file
	// embedding errors across the whole vault before lite fallback kicks in.
	if len(work) > 0 {
		if err := preflightEmbeddingProvider(embedClient); err != nil {
			return nil, embedding.HumanizeError(fmt.Errorf("embedding backend unavailable: %w", err))
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
				// Check for cancellation before processing
				select {
				case <-ctx.Done():
					resultCh <- embResult{Path: w.relPath, Err: ctx.Err()}
					continue
				default:
				}
				records, embeddings, content, meta, err := buildRecordsWithContent(w.path, w.relPath, vaultPath, embedClient, w.content)
				resultCh <- embResult{
					Records:    records,
					Embeddings: embeddings,
					Content:    content,
					Path:       w.relPath,
					Meta:       meta,
					Err:        err,
				}
			}
		}()
	}

	// Send work items, stopping early on cancellation
sendLoop:
	for _, w := range work {
		select {
		case <-ctx.Done():
			break sendLoop
		case workCh <- w:
		}
	}
	close(workCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results and insert.
	// Graph extraction is deferred to a second pass so that all embedding
	// requests complete before any LLM generation requests are issued. This
	// prevents Ollama from swapping between the embedding model and the chat
	// model concurrently, which causes timeouts on resource-constrained machines.
	type graphWork struct {
		rootID  int64
		path    string
		content []byte
		agent   string
	}
	var pendingGraph []graphWork

	canceled := false
	embeddingFileFailures := 0
	for result := range resultCh {
		// Check for cancellation
		if ctx.Err() != nil {
			if !canceled {
				canceled = true
				stats.Canceled = true
			}
			// Drain remaining results without processing
			continue
		}
		if result.Err != nil {
			// Embedding skipped: all chunks failed to embed but records were
			// built. Store them via the lite (FTS5-only) path so the note
			// remains keyword-searchable.
			if errors.Is(result.Err, errEmbeddingSkipped) && len(result.Records) > 0 {
				fileName := filepath.Base(result.Path)
				fmt.Fprintf(os.Stderr, "  \u26a0 Skipped embedding for %s (chunk too large for model context)\n", fileName)
				fmt.Fprintf(os.Stderr, "    Note is still keyword-searchable.\n")

				if delErr := db.DeleteByPath(result.Path); delErr != nil {
					fmt.Fprintf(os.Stderr, "  [ERROR] delete %s: %v\n", result.Path, delErr)
					stats.Errors++
					continue
				}
				insertedIDs, insertErr := db.BulkInsertNotesLite(result.Records)
				if insertErr != nil {
					fmt.Fprintf(os.Stderr, "  [ERROR] storing %s (lite): %v\n", result.Path, insertErr)
					stats.Errors++
					continue
				}
				recordFrontmatterProvenance(db, result.Path, result.Meta)
				if rootID, ok := insertedIDs[result.Path]; ok {
					agent := ""
					if len(result.Records) > 0 {
						agent = result.Records[0].Agent
					}
					pendingGraph = append(pendingGraph, graphWork{
						rootID:  rootID,
						path:    result.Path,
						content: result.Content,
						agent:   agent,
					})
				}
				stats.NewlyIndexed++
				processed := stats.NewlyIndexed + stats.SkippedUnchanged + stats.Errors
				if progress != nil {
					progress(processed, stats.TotalFiles, result.Path)
				} else {
					fmt.Fprintf(os.Stderr, "  [%d/%d] Indexed (keyword-only): %s (%d chunks)\n",
						processed, stats.TotalFiles, result.Path, len(result.Records))
				}
				continue
			}

			fmt.Fprintf(os.Stderr, "  [ERROR] %s: %v\n", result.Path, embedding.HumanizeError(result.Err))
			if errors.Is(result.Err, errNoEmbeddingsForFile) {
				embeddingFileFailures++
			}
			stats.Errors++
			continue
		}
		if len(result.Records) == 0 {
			continue
		}

		// Always delete old chunks for this path before inserting new ones.
		// In force mode this replaces the old upfront DeleteAllNotes approach,
		// which was unsafe: if reindex failed mid-way, the vault was empty.
		if err := db.DeleteByPath(result.Path); err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] delete %s: %v\n", result.Path, err)
			stats.Errors++
			continue
		}

		insertedIDs, err := db.BulkInsertNotes(result.Records, result.Embeddings)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] storing %s: %v\n", result.Path, err)
			stats.Errors++
			continue
		}

		recordFrontmatterProvenance(db, result.Path, result.Meta)

		// Defer graph extraction to after all embeddings are done
		if rootID, ok := insertedIDs[result.Path]; ok {
			agent := ""
			if len(result.Records) > 0 {
				agent = result.Records[0].Agent
			}
			pendingGraph = append(pendingGraph, graphWork{
				rootID:  rootID,
				path:    result.Path,
				content: result.Content,
				agent:   agent,
			})
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

	// Second pass: graph extraction runs after all embeddings are complete.
	// This avoids concurrent embedding + LLM model usage on Ollama.
	if !canceled {
		for _, gw := range pendingGraph {
			if ctx.Err() != nil {
				break
			}
			if discovered, err := extractor.ExtractFromNote(gw.rootID, gw.path, string(gw.content), gw.agent); err == nil {
				recordDiscoveredSources(db, gw.path, vaultPath, discovered)
			}
		}
	}

	// If canceled, return partial stats
	if canceled {
		noteCount, _ := db.NoteCount()
		chunkCount, _ := db.ChunkCount()
		stats.NotesInIndex = noteCount
		stats.ChunksInIndex = chunkCount
		return stats, ErrCanceled
	}

	// In force mode, remove stale entries for files no longer on disk.
	if force {
		if indexed, err := db.GetContentHashes(); err == nil {
			for path := range indexed {
				if !currentPaths[path] {
					_ = db.DeleteByPath(path)
				}
			}
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
	if err := db.SetEmbeddingMeta(embedName, embedModel, embedDims); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] set embedding metadata: %v\n", err)
	}
	if err := db.SetMeta("index_mode", "full"); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] set index metadata: %v\n", err)
	}

	// Record reindex timestamp and version for doctor diagnostics
	if err := db.SetMeta("last_reindex_time", time.Now().UTC().Format(time.RFC3339)); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] set last reindex time: %v\n", err)
	}
	if Version != "" {
		if err := db.SetMeta("same_version", Version); err != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] set SAME version metadata: %v\n", err)
		}
	}

	// Best-effort: unload the embedding model to free GPU/CPU memory.
	if unloader, ok := embedClient.(embedding.Unloader); ok {
		unloader.UnloadModel()
	}

	// Rebuild FTS5 index after bulk insert
	if err := db.RebuildFTS(); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] FTS rebuild: %v\n", err)
	}

	// Prune old usage data (90 days)
	_, _ = db.PruneUsageData(90)

	// Save stats to file
	saveStats(stats)

	return stats, nil
}

func preflightEmbeddingProvider(embedClient embedding.Provider) error {
	_, err := embedClient.GetDocumentEmbedding("same embedding preflight")
	if err != nil {
		return fmt.Errorf("preflight embedding probe failed: %w", err)
	}
	return nil
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
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		noteCount, err1 := db.NoteCount()
		chunkCount, err2 := db.ChunkCount()
		result = map[string]interface{}{
			"status": "stats file unreadable; using live query",
		}
		if err1 == nil && err2 == nil {
			result["total_notes_in_index"] = noteCount
			result["total_chunks_in_index"] = chunkCount
		} else {
			result["hint"] = "run 'same reindex' first"
		}
	}
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
	records, embeddings, content, meta, err := buildRecords(filePath, relPath, vaultPath, embedClient)
	liteOnly := false
	if err != nil {
		if errors.Is(err, errEmbeddingSkipped) && len(records) > 0 {
			// Embedding failed for all chunks but records were built.
			// Fall back to FTS5-only storage so the note is keyword-searchable.
			fileName := filepath.Base(relPath)
			fmt.Fprintf(os.Stderr, "  \u26a0 Skipped embedding for %s (chunk too large for model context)\n", fileName)
			fmt.Fprintf(os.Stderr, "    Note is still keyword-searchable.\n")
			liteOnly = true
		} else {
			return fmt.Errorf("build records: %w", err)
		}
	}
	if len(records) == 0 {
		return nil
	}

	// Remove old chunks for this path before inserting new ones
	if err := database.DeleteByPath(relPath); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	var insertedIDs map[string]int64
	if liteOnly {
		insertedIDs, err = database.BulkInsertNotesLite(records)
	} else {
		insertedIDs, err = database.BulkInsertNotes(records, embeddings)
	}
	if err != nil {
		return fmt.Errorf("insert notes: %w", err)
	}

	recordFrontmatterProvenance(database, relPath, meta)

	// Graph Extraction
	// Basic extractor without LLM for single-file update speed
	graphDB := graph.NewDB(database.Conn())
	extractor := graph.NewExtractor(graphDB)
	if rootID, ok := insertedIDs[relPath]; ok {
		agent := ""
		if len(records) > 0 {
			agent = records[0].Agent
		}
		if discovered, extractErr := extractor.ExtractFromNote(rootID, relPath, string(content), agent); extractErr == nil {
			recordDiscoveredSources(database, relPath, vaultPath, discovered)
		}
	}

	// Rebuild FTS for the updated content
	_ = database.RebuildFTS()

	return nil
}

// IndexSingleFileLite indexes (or re-indexes) a single file without embeddings.
// Used by watcher mode when provider="none" (keyword-only mode).
func IndexSingleFileLite(database *store.DB, filePath, relPath, vaultPath string) error {
	records, content, meta, err := buildRecordsLite(filePath, relPath, vaultPath)
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

	recordFrontmatterProvenance(database, relPath, meta)

	graphDB := graph.NewDB(database.Conn())
	extractor := graph.NewExtractor(graphDB)
	if rootID, ok := insertedIDs[relPath]; ok {
		agent := ""
		if len(records) > 0 {
			agent = records[0].Agent
		}
		if discovered, extractErr := extractor.ExtractFromNote(rootID, relPath, string(content), agent); extractErr == nil {
			recordDiscoveredSources(database, relPath, vaultPath, discovered)
		}
	}

	_ = database.RebuildFTS()
	return nil
}

// BuildRecordsForFile builds note records and embeddings for a single file.
// Exported for use by the watcher.
func BuildRecordsForFile(filePath, relPath, vaultPath string, embedClient embedding.Provider) ([]store.NoteRecord, [][]float32, error) {
	recs, embs, _, _, err := buildRecords(filePath, relPath, vaultPath, embedClient)
	return recs, embs, err
}

func buildRecords(filePath, relPath, vaultPath string, embedClient embedding.Provider) ([]store.NoteRecord, [][]float32, []byte, NoteMeta, error) {
	return buildRecordsWithContent(filePath, relPath, vaultPath, embedClient, nil)
}

// buildRecordsWithContent builds records, optionally using pre-read content to avoid a second read.
func buildRecordsWithContent(filePath, relPath, vaultPath string, embedClient embedding.Provider, cachedContent []byte) ([]store.NoteRecord, [][]float32, []byte, NoteMeta, error) {
	content := cachedContent
	if content == nil {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, nil, nil, NoteMeta{}, fmt.Errorf("read file: %w", err)
		}
	}

	parsed := ParseNote(string(content))
	meta := parsed.Meta
	body := parsed.Body

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, nil, NoteMeta{}, fmt.Errorf("stat file: %w", err)
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
	confidence := memory.ComputeConfidence(contentType, mtime, 0, reviewBy != "", "unknown")

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

	// Collect all embed texts for batch embedding
	embedTexts := make([]string, len(chunks))
	for i, chunk := range chunks {
		embedText := title + "\n" + chunk.Text
		if len(embedText) > config.MaxEmbedChars {
			embedText = embedText[:config.MaxEmbedChars]
		}
		embedTexts[i] = embedText
	}

	// Batch embed all chunks at once
	allVecs, batchErr := embedClient.GetDocumentEmbeddings(embedTexts)

	var records []store.NoteRecord
	var embeddings [][]float32
	embedFailures := 0

	if batchErr != nil {
		// Batch failed — fall back to individual embedding per chunk
		fmt.Fprintf(os.Stderr, "  [WARN] Batch embed failed for %s, falling back to sequential: %v\n", relPath, batchErr)
		for i, chunk := range chunks {
			vec, err := embedClient.GetDocumentEmbedding(embedTexts[i])
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
	} else {
		// Batch succeeded — map vectors back to records
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
			embeddings = append(embeddings, allVecs[i])
		}
	}

	if len(chunks) > 0 && embedFailures == len(chunks) {
		// All chunks failed embedding. Build records without embeddings so the
		// caller can store them via the lite (FTS5-only) path. The note remains
		// keyword-searchable even though semantic search won't work for it.
		var liteRecords []store.NoteRecord
		for i, chunk := range chunks {
			text := chunk.Text
			if len(text) > 10000 {
				text = text[:10000]
			}
			liteRecords = append(liteRecords, store.NoteRecord{
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
		return liteRecords, nil, content, meta, fmt.Errorf("%w: %s", errEmbeddingSkipped, relPath)
	}

	return records, embeddings, content, meta, nil
}

// WalkVault returns all markdown file paths in the vault, respecting skip dirs.
func WalkVault(vaultPath string) []string {
	return walkVault(vaultPath)
}

// WalkVaultWithIgnore returns all markdown file paths, respecting both skip dirs
// and .sameignore patterns. This is the preferred entry point for callers that
// want full ignore support.
func WalkVaultWithIgnore(vaultPath string) []string {
	ip := LoadSameignore(vaultPath)
	return walkVaultWithIgnore(vaultPath, ip)
}

// CountMarkdownFiles returns the number of .md files in a directory.
func CountMarkdownFiles(dir string) int {
	return len(walkVault(dir))
}

func walkVault(vaultPath string) []string {
	ip := LoadSameignore(vaultPath)
	return walkVaultWithIgnore(vaultPath, ip)
}

func walkVaultWithIgnore(vaultPath string, ip *IgnorePatterns) []string {
	vaultAbs, _ := filepath.Abs(vaultPath)
	// Canonicalize the vault root so that macOS /var → /private/var
	// (and similar symlinked roots) compare correctly with EvalSymlinks results.
	realVault, evalVaultErr := filepath.EvalSymlinks(vaultAbs)
	if evalVaultErr != nil {
		realVault = vaultAbs
	}
	var files []string
	if err := filepath.WalkDir(vaultPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// SECURITY: skip symlinks to prevent reading files outside the vault
		info, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(vaultPath, path)
		relPath = filepath.ToSlash(relPath)

		if d.IsDir() {
			name := d.Name()
			if config.SkipDirs[name] {
				return filepath.SkipDir
			}
			// Check .sameignore for directory patterns
			if ip != nil && relPath != "." && ip.ShouldIgnore(relPath, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") && !config.SkipFiles[d.Name()] {
			// Check .sameignore for file patterns
			if ip != nil && ip.ShouldIgnore(relPath, false) {
				return nil
			}
			// SECURITY: verify resolved path stays inside vault
			realPath, evalErr := filepath.EvalSymlinks(path)
			if evalErr == nil {
				if !strings.HasPrefix(realPath, realVault+string(filepath.Separator)) && realPath != realVault {
					return nil
				}
			}
			files = append(files, path)
		}
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: vault walk failed for %s: %v\n", vaultPath, err)
	}
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

// recordDiscoveredSources records graph-extracted source references as provenance.
// Best-effort: errors are logged but don't propagate.
func recordDiscoveredSources(database *store.DB, notePath, vaultPath string, discovered []graph.DiscoveredSource) {
	if len(discovered) == 0 {
		return
	}
	vaultAbs, _ := filepath.Abs(vaultPath)
	realVault, err := filepath.EvalSymlinks(vaultAbs)
	if err != nil {
		realVault = vaultAbs
	}
	var sources []store.NoteSource
	for _, d := range discovered {
		hash := ""
		if d.SourceType == "file" || d.SourceType == "note" {
			// SECURITY: validate source path stays inside vault
			clean := filepath.ToSlash(filepath.Clean(d.SourcePath))
			if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) || strings.Contains(clean, "\x00") {
				continue
			}
			fullPath := filepath.Join(vaultPath, d.SourcePath)
			// Check resolved path doesn't escape vault via symlinks
			realPath, evalErr := filepath.EvalSymlinks(fullPath)
			if evalErr != nil {
				// File doesn't exist or can't resolve — still record with empty hash
			} else if !strings.HasPrefix(realPath, realVault+string(filepath.Separator)) && realPath != realVault {
				continue
			} else if content, err := os.ReadFile(fullPath); err == nil {
				hash = sha256Hash(string(content))
			}
		}
		sources = append(sources, store.NoteSource{
			SourcePath: d.SourcePath,
			SourceType: d.SourceType,
			SourceHash: hash,
		})
	}
	if err := database.RecordSources(notePath, sources); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] record discovered sources for %s: %v\n", notePath, err)
	}
}

// recordFrontmatterProvenance records provenance from frontmatter fields.
// This handles imported notes whose original source is outside the vault
// (e.g., Claude memory files at ~/.claude/memory/).
// Unlike recordDiscoveredSources, this allows absolute paths because
// import provenance is set by SAME's own import command, not user input.
func recordFrontmatterProvenance(database *store.DB, notePath string, meta NoteMeta) {
	if meta.ProvenanceSource == "" {
		return
	}
	// SECURITY: Only trust provenance_source frontmatter for notes in the
	// imports/ directory. These notes were created by `same import`, a trusted
	// local command. Notes created by MCP save_note or manual editing could
	// contain attacker-controlled provenance_source values pointing at
	// sensitive files outside the vault.
	if !strings.HasPrefix(notePath, "imports/") {
		return
	}

	hash := meta.ProvenanceHash
	if hash == "" {
		// Try to compute hash from current file state
		if content, err := os.ReadFile(meta.ProvenanceSource); err == nil {
			hash = sha256Hash(string(content))
		}
	}

	source := store.NoteSource{
		SourcePath: meta.ProvenanceSource,
		SourceType: "file",
		SourceHash: hash,
	}
	if err := database.RecordSources(notePath, []store.NoteSource{source}); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] record frontmatter provenance for %s: %v\n", notePath, err)
	}
}

// liteResult holds the result of parsing a single file for lite indexing.
type liteResult struct {
	Records []store.NoteRecord
	Content []byte
	RelPath string
	Meta    NoteMeta
	Err     error
}

// ReindexLite indexes vault notes WITHOUT generating embeddings (FTS5-only mode).
// Used when Ollama is unavailable. Notes are parsed, chunked, and stored for keyword search.
// Uses a worker pool (4 goroutines) for parallel file I/O and parsing, matching
// the concurrency model of the full Reindex function.
func ReindexLite(ctx context.Context, db *store.DB, force bool, progress ProgressFunc) (*Stats, error) {
	vaultPath := config.VaultPath()
	mdFiles := walkVault(vaultPath)
	stats := &Stats{
		TotalFiles: len(mdFiles),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	// In incremental mode, load existing hashes to skip unchanged files.
	// In force mode, all files are re-indexed — but we do NOT delete upfront
	// to avoid data loss if the reindex fails partway through.
	var existingHashes map[string]string
	if !force {
		var err error
		existingHashes, err = db.GetContentHashes()
		if err != nil {
			existingHashes = make(map[string]string)
		}
	}

	// Build work queue, filtering unchanged files (same as full Reindex).
	type fileWork struct {
		path    string
		relPath string
	}
	var work []fileWork
	currentPaths := make(map[string]bool, len(mdFiles))
	for _, fp := range mdFiles {
		relPath := relativePath(fp, vaultPath)
		currentPaths[relPath] = true

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

	// Process files with a worker pool (4 goroutines) for parallel file
	// I/O and markdown parsing. DB writes remain serial (SQLite constraint).
	const numWorkers = 4
	workCh := make(chan fileWork, len(work))
	resultCh := make(chan liteResult, len(work))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range workCh {
				select {
				case <-ctx.Done():
					resultCh <- liteResult{RelPath: w.relPath, Err: ctx.Err()}
					continue
				default:
				}
				records, content, meta, err := buildRecordsLite(w.path, w.relPath, vaultPath)
				resultCh <- liteResult{
					Records: records,
					Content: content,
					RelPath: w.relPath,
					Meta:    meta,
					Err:     err,
				}
			}
		}()
	}

	// Send work items
sendLoop:
	for _, w := range work {
		select {
		case <-ctx.Done():
			break sendLoop
		case workCh <- w:
		}
	}
	close(workCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results and insert (serial DB writes)
	canceled := false
	graphDB := graph.NewDB(db.Conn())
	extractor := graph.NewExtractor(graphDB)

	for result := range resultCh {
		if ctx.Err() != nil {
			if !canceled {
				canceled = true
				stats.Canceled = true
			}
			continue
		}
		if result.Err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s: %v\n", result.RelPath, result.Err)
			stats.Errors++
			continue
		}

		if len(result.Records) == 0 {
			continue
		}

		// Always delete old chunks for this path before inserting new ones.
		// In force mode this replaces the old upfront DeleteAllNotes approach,
		// which was unsafe: if reindex failed mid-way, the vault was empty.
		if err := db.DeleteByPath(result.RelPath); err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] delete %s: %v\n", result.RelPath, err)
			stats.Errors++
			continue
		}

		insertedIDs, err := db.BulkInsertNotesLite(result.Records)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERROR] storing %s: %v\n", result.RelPath, err)
			stats.Errors++
			continue
		}

		recordFrontmatterProvenance(db, result.RelPath, result.Meta)

		// Graph Extraction
		if rootID, ok := insertedIDs[result.RelPath]; ok {
			agent := ""
			if len(result.Records) > 0 {
				agent = result.Records[0].Agent
			}
			if discovered, extractErr := extractor.ExtractFromNote(rootID, result.RelPath, string(result.Content), agent); extractErr == nil {
				recordDiscoveredSources(db, result.RelPath, vaultPath, discovered)
			}
		}

		stats.NewlyIndexed++
		processed := stats.NewlyIndexed + stats.SkippedUnchanged + stats.Errors
		if progress != nil {
			progress(processed, stats.TotalFiles, result.RelPath)
		}
	}

	if canceled {
		noteCount, _ := db.NoteCount()
		chunkCount, _ := db.ChunkCount()
		stats.NotesInIndex = noteCount
		stats.ChunksInIndex = chunkCount
		return stats, ErrCanceled
	}

	// In force mode, remove stale entries for files no longer on disk.
	// This replaces the old upfront DeleteAllNotes approach which risked
	// data loss if the reindex failed partway through.
	if force {
		if indexed, err := db.GetContentHashes(); err == nil {
			for path := range indexed {
				if !currentPaths[path] {
					_ = db.DeleteByPath(path)
				}
			}
		}
	}

	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	stats.NotesInIndex = noteCount
	stats.ChunksInIndex = chunkCount

	if err := db.SetMeta("last_reindex_time", time.Now().UTC().Format(time.RFC3339)); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] set last reindex time: %v\n", err)
	}
	if err := db.SetMeta("index_mode", "lite"); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] set index metadata: %v\n", err)
	}
	if Version != "" {
		if err := db.SetMeta("same_version", Version); err != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] set SAME version metadata: %v\n", err)
		}
	}

	if err := db.RebuildFTS(); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] FTS rebuild: %v\n", err)
	}
	saveStats(stats)

	return stats, nil
}

// ReindexProgressive indexes the vault in two phases:
//
// Phase 1 (fast): Insert all notes into vault_notes + FTS5 (no embeddings).
// Search is immediately available via keyword/FTS5 fallback.
//
// Phase 2 (slow): Backfill embeddings one note at a time. Search progressively
// upgrades from keyword-only to hybrid as embeddings arrive.
//
// If the embedding provider is unavailable, Phase 1 completes and Phase 2 is
// skipped (equivalent to ReindexLite). If Phase 2 is canceled, the FTS5 index
// remains intact and embeddings resume on next run via BackfillEmbeddings.
func ReindexProgressive(ctx context.Context, db *store.DB, force bool, liteProgress ProgressFunc, embedProgress EmbeddingProgressFunc) (*Stats, *EmbeddingProgress, error) {
	// Phase 1: FTS5-only indexing (fast)
	stats, err := ReindexLite(ctx, db, force, liteProgress)
	if err != nil {
		return stats, nil, err
	}

	// Set index mode to progressive (between lite and full)
	if err := db.SetMeta("index_mode", "progressive"); err != nil {
		fmt.Fprintf(os.Stderr, "  [WARN] set index mode: %v\n", err)
	}

	// Phase 2: Embedding backfill
	// Create embedding client — if it fails, FTS5 index is still usable
	ec := config.EmbeddingProviderConfig()
	provCfg := embedding.ProviderConfig{
		Provider:   ec.Provider,
		Model:      ec.Model,
		APIKey:     ec.APIKey,
		BaseURL:    ec.BaseURL,
		Dimensions: ec.Dimensions,
	}
	if (provCfg.Provider == "ollama" || provCfg.Provider == "") && provCfg.BaseURL == "" {
		if ollamaURL, urlErr := config.OllamaURL(); urlErr == nil {
			provCfg.BaseURL = ollamaURL
		}
	}

	embedClient, err := embedding.NewProvider(provCfg)
	if err != nil {
		// Embedding provider not available — Phase 1 results stand as lite mode
		return stats, nil, nil
	}

	// Preflight check before committing to the embedding pass
	if err := preflightEmbeddingProvider(embedClient); err != nil {
		// Embedding provider not responding — stay in lite mode
		return stats, nil, nil
	}

	// Run embedding backfill
	embResult, embErr := BackfillEmbeddings(ctx, db, embedClient, embedProgress)

	// Best-effort: unload the embedding model from Ollama to free GPU/CPU memory.
	// This prevents a stale runner process from consuming resources after reindex.
	if unloader, ok := embedClient.(embedding.Unloader); ok {
		unloader.UnloadModel()
	}

	if embErr != nil && !errors.Is(embErr, ErrCanceled) {
		return stats, embResult, fmt.Errorf("embedding backfill: %w", embErr)
	}

	// Record embedding metadata after first successful embedding
	if embResult != nil && embResult.Completed > 0 {
		embedName := embedClient.Name()
		embedModel := embedClient.Model()
		embedDims := embedClient.Dimensions()
		if metaErr := db.SetEmbeddingMeta(embedName, embedModel, embedDims); metaErr != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] set embedding metadata: %v\n", metaErr)
		}
	}

	// Update index mode based on completion
	if embResult != nil && embResult.Completed == embResult.Total && embResult.Total > 0 {
		if metaErr := db.SetMeta("index_mode", "full"); metaErr != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] set index mode: %v\n", metaErr)
		}
	}

	// Update final stats counts (may have changed during embedding)
	noteCount, _ := db.NoteCount()
	chunkCount, _ := db.ChunkCount()
	stats.NotesInIndex = noteCount
	stats.ChunksInIndex = chunkCount

	if errors.Is(embErr, ErrCanceled) {
		stats.Canceled = true
		return stats, embResult, ErrCanceled
	}

	return stats, embResult, nil
}

// EmbeddingProgressFunc is called during embedding backfill to report progress.
type EmbeddingProgressFunc func(completed, total int)

// BackfillEmbeddings generates embeddings for notes that were indexed without
// vectors (FTS5-only). It processes notes one at a time to avoid overloading
// the embedding provider. Returns progress stats and nil error on completion.
// If the context is canceled, returns partial progress and ErrCanceled.
func BackfillEmbeddings(ctx context.Context, db *store.DB, embedClient embedding.Provider, progress EmbeddingProgressFunc) (*EmbeddingProgress, error) {
	ids, err := db.UnembeddedNoteIDs()
	if err != nil {
		return nil, fmt.Errorf("get unembedded notes: %w", err)
	}

	result := &EmbeddingProgress{
		Total: len(ids),
	}

	if len(ids) == 0 {
		return result, nil
	}

	for _, noteID := range ids {
		// Check cancellation before each note
		select {
		case <-ctx.Done():
			return result, ErrCanceled
		default:
		}

		note, err := db.GetNoteByID(noteID)
		if err != nil || note == nil {
			result.Failed++
			continue
		}

		// Build the same embed text used by buildRecordsWithContent
		embedText := note.Title + "\n" + note.Text
		if len(embedText) > config.MaxEmbedChars {
			embedText = embedText[:config.MaxEmbedChars]
		}

		vec, err := embedClient.GetDocumentEmbedding(embedText)
		if err != nil {
			fileName := filepath.Base(note.Path)
			fmt.Fprintf(os.Stderr, "  \u26a0 Skipped embedding for %s (chunk %d): %v\n",
				fileName, note.ChunkID, embedding.HumanizeError(err))
			fmt.Fprintf(os.Stderr, "    Note is still keyword-searchable.\n")
			result.Failed++
			continue
		}

		if err := db.InsertEmbeddingForNote(noteID, vec); err != nil {
			fmt.Fprintf(os.Stderr, "  [WARN] insert embedding %s (chunk %d): %v\n",
				note.Path, note.ChunkID, err)
			result.Failed++
			continue
		}

		result.Completed++

		if progress != nil {
			progress(result.Completed, result.Total)
		}
	}

	return result, nil
}

// buildRecordsLite builds note records WITHOUT embeddings.
func buildRecordsLite(filePath, relPath, vaultPath string) ([]store.NoteRecord, []byte, NoteMeta, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, NoteMeta{}, fmt.Errorf("read file: %w", err)
	}

	parsed := ParseNote(string(content))
	meta := parsed.Meta
	body := parsed.Body

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, NoteMeta{}, fmt.Errorf("stat file: %w", err)
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
	confidence := memory.ComputeConfidence(contentType, mtime, 0, reviewBy != "", "unknown")

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

	return records, content, meta, nil
}

func saveStats(stats *Stats) {
	dataDir := config.DataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to create stats directory %q: %v\n", dataDir, err)
		return
	}
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to encode index stats: %v\n", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dataDir, "index_stats.json"), data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "same: warning: failed to write index stats: %v\n", err)
	}
}
