package consolidate

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/sgx-labs/statelessagent/internal/store"
)

// NoteData holds the fields needed for consolidation grouping.
type NoteData struct {
	ID          int64
	Path        string
	Title       string
	Text        string
	Modified    float64
	ContentType string
	Confidence  float64
	Tags        string // JSON array
}

// GroupNotes clusters notes by semantic similarity using embeddings from the database.
// Falls back to tag/path-based grouping when embeddings are unavailable.
// Only returns groups with 2+ notes.
func GroupNotes(db *store.DB, notes []NoteData, threshold float64) ([][]NoteData, error) {
	if len(notes) < 2 {
		return nil, nil
	}

	if db.HasVectors() {
		groups, err := groupByEmbeddings(db, notes, threshold)
		if err != nil {
			fmt.Fprintf(os.Stderr, "same: consolidate: vector grouping failed (%v), falling back to tag/path grouping\n", err)
			return groupByTagsAndPath(notes), nil
		}
		if len(groups) > 0 {
			return groups, nil
		}
	}

	// Fallback: group by shared tags or directory path.
	return groupByTagsAndPath(notes), nil
}

// groupByEmbeddings clusters notes using cosine similarity of their embedding vectors.
// Uses greedy clustering: pick first unassigned note, find all with similarity > threshold.
func groupByEmbeddings(db *store.DB, notes []NoteData, threshold float64) ([][]NoteData, error) {
	// Load embeddings for all notes.
	type noteWithVec struct {
		note NoteData
		vec  []float32
	}
	var withVecs []noteWithVec

	for _, n := range notes {
		vec, err := getNoteEmbeddingByID(db, n.ID)
		if err != nil || vec == nil {
			continue
		}
		withVecs = append(withVecs, noteWithVec{note: n, vec: vec})
	}

	if len(withVecs) < 2 {
		return nil, nil
	}

	fmt.Fprintf(os.Stderr, "same: consolidate: computing similarity for %d notes with embeddings...\n", len(withVecs))

	// Greedy clustering.
	assigned := make([]bool, len(withVecs))
	var groups [][]NoteData

	for i := 0; i < len(withVecs); i++ {
		if assigned[i] {
			continue
		}
		assigned[i] = true
		group := []NoteData{withVecs[i].note}

		for j := i + 1; j < len(withVecs); j++ {
			if assigned[j] {
				continue
			}
			sim := cosineSimilarity(withVecs[i].vec, withVecs[j].vec)
			if sim >= threshold {
				assigned[j] = true
				group = append(group, withVecs[j].note)
			}
		}

		// Only keep groups with 2+ notes.
		if len(group) >= 2 {
			groups = append(groups, group)
		}
	}

	return groups, nil
}

// getNoteEmbeddingByID retrieves the embedding vector for a note by its database ID.
func getNoteEmbeddingByID(db *store.DB, noteID int64) ([]float32, error) {
	var vecData []byte
	err := db.Conn().QueryRow(
		"SELECT embedding FROM vault_notes_vec WHERE note_id = ?", noteID,
	).Scan(&vecData)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return deserializeFloat32(vecData)
}

// deserializeFloat32 converts raw little-endian bytes back to []float32.
func deserializeFloat32(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid vector data length: %d", len(data))
	}
	n := len(data) / 4
	vec := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(data[i*4 : (i+1)*4])
		vec[i] = math.Float32frombits(bits)
	}
	return vec, nil
}

// cosineSimilarity computes the cosine similarity between two vectors.
// Returns a value between -1 and 1 (1 = identical direction).
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// groupByTagsAndPath groups notes by shared tags (2+ overlap) or path prefix.
// Used as a fallback when no embeddings are available.
func groupByTagsAndPath(notes []NoteData) [][]NoteData {
	// Try tag-based grouping first.
	groups := groupByTags(notes)
	if len(groups) > 0 {
		return groups
	}

	// Fall back to directory-based grouping.
	return groupByDirectory(notes)
}

// groupByTags clusters notes that share 2+ tags.
func groupByTags(notes []NoteData) [][]NoteData {
	type parsedNote struct {
		note NoteData
		tags map[string]bool
	}

	var parsed []parsedNote
	for _, n := range notes {
		tags := parseTags(n.Tags)
		if len(tags) == 0 {
			continue
		}
		tagSet := make(map[string]bool, len(tags))
		for _, t := range tags {
			tagSet[strings.ToLower(t)] = true
		}
		parsed = append(parsed, parsedNote{note: n, tags: tagSet})
	}

	if len(parsed) < 2 {
		return nil
	}

	assigned := make([]bool, len(parsed))
	var groups [][]NoteData

	for i := 0; i < len(parsed); i++ {
		if assigned[i] {
			continue
		}
		assigned[i] = true
		group := []NoteData{parsed[i].note}

		for j := i + 1; j < len(parsed); j++ {
			if assigned[j] {
				continue
			}
			overlap := tagOverlap(parsed[i].tags, parsed[j].tags)
			if overlap >= 2 {
				assigned[j] = true
				group = append(group, parsed[j].note)
			}
		}

		if len(group) >= 2 {
			groups = append(groups, group)
		}
	}

	return groups
}

// groupByDirectory clusters notes in the same directory.
func groupByDirectory(notes []NoteData) [][]NoteData {
	dirMap := make(map[string][]NoteData)
	for _, n := range notes {
		dir := pathDir(n.Path)
		dirMap[dir] = append(dirMap[dir], n)
	}

	var groups [][]NoteData
	for _, group := range dirMap {
		if len(group) >= 2 {
			groups = append(groups, group)
		}
	}
	return groups
}

// tagOverlap counts how many tags two sets have in common.
func tagOverlap(a, b map[string]bool) int {
	count := 0
	for tag := range a {
		if b[tag] {
			count++
		}
	}
	return count
}

// parseTags parses a JSON tags string into a slice.
func parseTags(tagsJSON string) []string {
	var tags []string
	_ = json.Unmarshal([]byte(tagsJSON), &tags)
	return tags
}

// pathDir returns the directory component of a vault-relative path.
// For top-level notes (no directory), returns ".".
func pathDir(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "."
	}
	return path[:idx]
}
