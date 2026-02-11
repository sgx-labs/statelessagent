package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgx-labs/statelessagent/internal/store"
)

// --- CompositeScore edge cases ---

func TestCompositeScore_ZeroWeights(t *testing.T) {
	now := float64(time.Now().Unix())
	score := CompositeScore(1.0, now, 1.0, "note", 0, 0, 0)
	if score != 0 {
		t.Errorf("all-zero weights should give 0, got %.3f", score)
	}
}

func TestCompositeScore_MaxValues(t *testing.T) {
	now := float64(time.Now().Unix())
	score := CompositeScore(1.0, now, 1.0, "decision", 1.0, 1.0, 1.0)
	if score != 1.0 {
		t.Errorf("max values should cap at 1.0, got %.3f", score)
	}
}

func TestCompositeScore_ZeroSemantic(t *testing.T) {
	now := float64(time.Now().Unix())
	score := CompositeScore(0, now, 0.5, "note", 0.5, 0.3, 0.2)
	if score <= 0 {
		t.Errorf("zero semantic with nonzero recency/confidence should be >0, got %.3f", score)
	}
}

func TestCompositeScore_OldContent(t *testing.T) {
	veryOld := float64(time.Now().Unix()) - 365*86400
	score := CompositeScore(0.8, veryOld, 0.5, "note", 0.5, 0.3, 0.2)
	recentScore := CompositeScore(0.8, float64(time.Now().Unix()), 0.5, "note", 0.5, 0.3, 0.2)
	if score >= recentScore {
		t.Errorf("old content should score lower than recent: old=%.3f recent=%.3f", score, recentScore)
	}
}

func TestCompositeScore_TypeBoosting(t *testing.T) {
	veryOld := float64(time.Now().Unix()) - 365*86400
	// Decisions never decay, so old decision should score higher than old note
	decisionScore := CompositeScore(0.5, veryOld, 0.5, "decision", 0.3, 0.5, 0.2)
	noteScore := CompositeScore(0.5, veryOld, 0.5, "note", 0.3, 0.5, 0.2)
	if decisionScore <= noteScore {
		t.Errorf("decision (no decay) should score higher than note when old: decision=%.3f note=%.3f",
			decisionScore, noteScore)
	}
}

func TestCompositeScore_NegativeSemantic(t *testing.T) {
	now := float64(time.Now().Unix())
	score := CompositeScore(-1.0, now, 0.5, "note", 0.5, 0.3, 0.2)
	if score < 0 {
		t.Errorf("negative inputs should be clamped to 0, got %.3f", score)
	}
}

// --- EstimateTokens edge cases ---

func TestEstimateTokens_Empty(t *testing.T) {
	if tokens := EstimateTokens(""); tokens != 0 {
		t.Errorf("empty string should be 0 tokens, got %d", tokens)
	}
}

func TestEstimateTokens_Short(t *testing.T) {
	// 3 chars / 4 = 0 tokens (integer division)
	if tokens := EstimateTokens("abc"); tokens != 0 {
		t.Errorf("3-char string should be 0 tokens, got %d", tokens)
	}
}

func TestEstimateTokens_ExactMultiple(t *testing.T) {
	text := "abcdefgh" // 8 chars / 4 = 2
	if tokens := EstimateTokens(text); tokens != 2 {
		t.Errorf("8-char string should be 2 tokens, got %d", tokens)
	}
}

func TestEstimateTokens_LargeText(t *testing.T) {
	text := strings.Repeat("a", 4000) // 4000 / 4 = 1000
	if tokens := EstimateTokens(text); tokens != 1000 {
		t.Errorf("4000-char string should be 1000 tokens, got %d", tokens)
	}
}

// --- HasRecencyIntent additional tests ---

func TestHasRecencyIntent_CaseInsensitive(t *testing.T) {
	if !HasRecencyIntent("What did I work on RECENTLY?") {
		t.Error("should match case-insensitive 'RECENTLY'")
	}
}

func TestHasRecencyIntent_MultiWordPhrases(t *testing.T) {
	phrases := []string{
		"catch me up on the project",
		"where were we on that task",
		"bring me up to speed",
		"what happened since I left",
		"what was the last session about",
		"let me see the hand-off notes",
		"notes from earlier today",
		"what I left off working on",
	}
	for _, p := range phrases {
		if !HasRecencyIntent(p) {
			t.Errorf("expected recency intent for %q", p)
		}
	}
}

func TestHasRecencyIntent_EmbeddedKeyword(t *testing.T) {
	// "latest" is a keyword even when embedded in a longer sentence
	if !HasRecencyIntent("show me the latest documentation updates") {
		t.Error("should match 'latest' in longer sentence")
	}
}

// --- ComputeRecencyScore edge cases ---

func TestComputeRecencyScore_UnknownType(t *testing.T) {
	now := float64(time.Now().Unix())
	score := ComputeRecencyScore(now, "unknown_type")
	if score < 0.95 {
		t.Errorf("unknown type with recent content should be ~1.0, got %.3f", score)
	}
}

func TestComputeRecencyScore_FutureTimestamp(t *testing.T) {
	future := float64(time.Now().Unix()) + 86400
	score := ComputeRecencyScore(future, "note")
	if score != 1.0 {
		t.Errorf("future timestamp should return 1.0, got %.3f", score)
	}
}

func TestComputeRecencyScore_AllPermanentTypes(t *testing.T) {
	veryOld := float64(time.Now().Unix()) - 3650*86400 // 10 years ago
	for _, ct := range []string{"decision", "hub"} {
		score := ComputeRecencyScore(veryOld, ct)
		if score != 1.0 {
			t.Errorf("permanent type %q should always be 1.0, got %.3f", ct, score)
		}
	}
}

// --- ComputeConfidence edge cases ---

func TestComputeConfidence_UnknownType(t *testing.T) {
	now := float64(time.Now().Unix())
	score := ComputeConfidence("unknown_type", now, 0, false)
	// default baseline is 0.5 (same as "note")
	noteScore := ComputeConfidence("note", now, 0, false)
	if score != noteScore {
		t.Errorf("unknown type should use default baseline like 'note': got %.3f, want %.3f", score, noteScore)
	}
}

func TestComputeConfidence_HighAccessCount(t *testing.T) {
	now := float64(time.Now().Unix())
	score := ComputeConfidence("note", now, 10000, false)
	// Access boost is capped at 0.15
	if score > 1.0 {
		t.Errorf("confidence should never exceed 1.0, got %.3f", score)
	}
}

// --- InferContentType additional tests ---

func TestInferContentType_ExplicitTypeInvalid(t *testing.T) {
	// Invalid explicit type should fall through to path/tag inference
	got := InferContentType("decisions/foo.md", "invalid_type", nil)
	if got != "decision" {
		t.Errorf("invalid explicit type should fall through to path: got %q, want 'decision'", got)
	}
}

func TestInferContentType_ExplicitTypeCaseInsensitive(t *testing.T) {
	got := InferContentType("random.md", "DECISION", nil)
	if got != "decision" {
		t.Errorf("explicit type should be case insensitive: got %q, want 'decision'", got)
	}
}

func TestInferContentType_MOCPath(t *testing.T) {
	got := InferContentType("moc/overview.md", "", nil)
	if got != "hub" {
		t.Errorf("path with 'moc' should infer hub: got %q", got)
	}
}

func TestInferContentType_IndexPath(t *testing.T) {
	got := InferContentType("resources/index.md", "", nil)
	if got != "hub" {
		t.Errorf("path with 'index' should infer hub: got %q", got)
	}
}

func TestInferContentType_HandoffTag(t *testing.T) {
	got := InferContentType("random.md", "", []string{"handoff"})
	if got != "handoff" {
		t.Errorf("tag 'handoff' should infer handoff: got %q", got)
	}
}

// --- Budget report generation with in-memory DB ---

func TestLogInjection(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	LogInjection(db, "sess-1", "context_surfacing", []string{"notes/foo.md"}, "some injected text here")

	records, err := db.GetUsageBySession("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].HookName != "context_surfacing" {
		t.Errorf("hook name = %q, want 'context_surfacing'", records[0].HookName)
	}
	if records[0].EstimatedTokens != EstimateTokens("some injected text here") {
		t.Errorf("estimated tokens mismatch: got %d", records[0].EstimatedTokens)
	}
	if records[0].WasReferenced {
		t.Error("new injection should not be referenced")
	}
}

func TestDetectReferences_NoRecords(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	count := DetectReferences(db, "nonexistent-session", "some text")
	if count != 0 {
		t.Errorf("expected 0 references with no records, got %d", count)
	}
}

func TestDetectReferences_MatchByPath(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	LogInjection(db, "sess-2", "hook1", []string{"notes/architecture.md"}, "some text")
	count := DetectReferences(db, "sess-2", "I referenced notes/architecture.md in my response")
	if count != 1 {
		t.Errorf("expected 1 reference by path, got %d", count)
	}
}

func TestDetectReferences_MatchByFilename(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	LogInjection(db, "sess-3", "hook1", []string{"notes/architecture-overview.md"}, "text")
	count := DetectReferences(db, "sess-3", "The architecture-overview document explains this well")
	if count != 1 {
		t.Errorf("expected 1 reference by filename, got %d", count)
	}
}

func TestDetectReferences_MatchByTitleWords(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	LogInjection(db, "sess-4", "hook1", []string{"notes/2026-01-15-project-design.md"}, "text")
	count := DetectReferences(db, "sess-4", "Based on the project design document, we should proceed")
	if count != 1 {
		t.Errorf("expected 1 reference by title words, got %d", count)
	}
}

func TestDetectReferences_NoMatch(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	LogInjection(db, "sess-5", "hook1", []string{"notes/architecture.md"}, "text")
	count := DetectReferences(db, "sess-5", "This response mentions nothing related at all")
	if count != 0 {
		t.Errorf("expected 0 references, got %d", count)
	}
}

func TestDetectReferences_ShortFilename(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Short filenames (<=3 chars) should not match to avoid false positives
	LogInjection(db, "sess-6", "hook1", []string{"notes/ai.md"}, "text")
	count := DetectReferences(db, "sess-6", "the ai model is working well")
	if count != 0 {
		t.Errorf("short filename 'ai' should not match, got %d", count)
	}
}

func TestGetBudgetReport_NoData(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	report := GetBudgetReport(db, "nonexistent", 5)
	statusMap, ok := report.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", report)
	}
	if statusMap["status"] != "no data" {
		t.Errorf("expected status 'no data', got %q", statusMap["status"])
	}
}

func TestGetBudgetReport_WithData(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert usage records
	LogInjection(db, "sess-a", "context_surfacing", []string{"notes/foo.md"}, strings.Repeat("x", 400))
	LogInjection(db, "sess-a", "context_surfacing", []string{"notes/bar.md"}, strings.Repeat("x", 800))
	LogInjection(db, "sess-a", "session_recovery", []string{"notes/baz.md"}, strings.Repeat("x", 200))

	// Mark one as referenced
	records, _ := db.GetUsageBySession("sess-a")
	if len(records) > 0 {
		db.MarkReferenced(records[0].ID)
	}

	report := GetBudgetReport(db, "sess-a", 0)
	br, ok := report.(BudgetReport)
	if !ok {
		t.Fatalf("expected BudgetReport, got %T", report)
	}

	if br.SessionsAnalyzed != 1 {
		t.Errorf("sessions analyzed = %d, want 1", br.SessionsAnalyzed)
	}
	if br.TotalInjections != 3 {
		t.Errorf("total injections = %d, want 3", br.TotalInjections)
	}
	if br.ReferencedCount != 1 {
		t.Errorf("referenced count = %d, want 1", br.ReferencedCount)
	}
	if br.TotalTokensInjected <= 0 {
		t.Error("total tokens should be > 0")
	}
	if len(br.PerHook) != 2 {
		t.Errorf("expected 2 hook entries, got %d", len(br.PerHook))
	}
	csHook, ok := br.PerHook["context_surfacing"]
	if !ok {
		t.Fatal("expected context_surfacing hook stats")
	}
	if csHook.Injections != 2 {
		t.Errorf("context_surfacing injections = %d, want 2", csHook.Injections)
	}
}

func TestGetBudgetReport_LowUtilizationSuggestion(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert 5 records, none referenced → utilization = 0% < 30%
	for i := 0; i < 5; i++ {
		LogInjection(db, "sess-low", "hook1", []string{"notes/foo.md"}, "text")
	}

	report := GetBudgetReport(db, "sess-low", 0)
	br, ok := report.(BudgetReport)
	if !ok {
		t.Fatalf("expected BudgetReport, got %T", report)
	}

	foundSuggestion := false
	for _, s := range br.Suggestions {
		if strings.Contains(s, "Low utilization") {
			foundSuggestion = true
			break
		}
	}
	if !foundSuggestion {
		t.Error("expected low utilization suggestion")
	}
}

func TestGetBudgetReport_RecentUsage(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	LogInjection(db, "sess-r1", "hook1", []string{"notes/a.md"}, "text")
	LogInjection(db, "sess-r2", "hook1", []string{"notes/b.md"}, "text")

	// Use lastNSessions mode (sessionID="")
	report := GetBudgetReport(db, "", 5)
	br, ok := report.(BudgetReport)
	if !ok {
		t.Fatalf("expected BudgetReport, got %T", report)
	}
	if br.SessionsAnalyzed != 2 {
		t.Errorf("sessions analyzed = %d, want 2", br.SessionsAnalyzed)
	}
}

func TestSaveBudgetReport(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "subdir", "report.json")

	report := BudgetReport{
		SessionsAnalyzed: 1,
		TotalInjections:  5,
		PerHook:          map[string]HookStats{},
	}

	err := SaveBudgetReport(report, outPath)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	var loaded BudgetReport
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.SessionsAnalyzed != 1 {
		t.Errorf("loaded sessions = %d, want 1", loaded.SessionsAnalyzed)
	}
	if loaded.TotalInjections != 5 {
		t.Errorf("loaded injections = %d, want 5", loaded.TotalInjections)
	}
}

func TestSaveBudgetReport_StatusMap(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "report.json")

	report := map[string]string{"status": "no data", "hint": "test"}
	err := SaveBudgetReport(report, outPath)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "no data") {
		t.Error("saved report should contain 'no data'")
	}
}

// --- ExtractDecisionsFromMessages ---

func TestExtractDecisionsFromMessages_AssistantOnly(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "We decided to use React because it is faster."},
		{Role: "assistant", Content: "We decided to use Go because it has better concurrency support."},
	}

	decisions := ExtractDecisionsFromMessages(msgs)
	for _, d := range decisions {
		if d.Role != "assistant" {
			t.Errorf("decision should only come from assistant, got role=%q", d.Role)
		}
	}
	if len(decisions) == 0 {
		t.Error("expected at least one decision from assistant message")
	}
}

func TestExtractDecisionsFromMessages_ShortMessages(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: "OK decided."},
	}
	decisions := ExtractDecisionsFromMessages(msgs)
	if len(decisions) != 0 {
		t.Errorf("messages under 20 chars should be skipped, got %d decisions", len(decisions))
	}
}

func TestExtractDecisionsFromMessages_Empty(t *testing.T) {
	decisions := ExtractDecisionsFromMessages(nil)
	if len(decisions) != 0 {
		t.Errorf("nil messages should return nil, got %d", len(decisions))
	}
}

// --- FormatDecisionEntry ---

func TestFormatDecisionEntry_WithProject(t *testing.T) {
	d := Decision{Text: "Use SQLite for storage", Confidence: "high"}
	entry := FormatDecisionEntry(d, "myproject")
	if !strings.Contains(entry, "myproject") {
		t.Error("entry should contain project name")
	}
	if !strings.Contains(entry, "Use SQLite for storage") {
		t.Error("entry should contain decision text")
	}
	if !strings.Contains(entry, "high") {
		t.Error("entry should contain confidence level")
	}
	dateStr := time.Now().Format("2006-01-02")
	if !strings.Contains(entry, dateStr) {
		t.Errorf("entry should contain today's date %s", dateStr)
	}
}

func TestFormatDecisionEntry_NoProject(t *testing.T) {
	d := Decision{Text: "Use Go modules", Confidence: "medium"}
	entry := FormatDecisionEntry(d, "")
	if strings.Contains(entry, "project:") {
		t.Error("entry without project should not contain project tag")
	}
}

// --- AppendToDecisionLog ---

func TestAppendToDecisionLog_Empty(t *testing.T) {
	count := AppendToDecisionLog(nil, "/tmp/nonexistent-log.md", "test")
	if count != 0 {
		t.Errorf("empty decisions should return 0, got %d", count)
	}
}

func TestAppendToDecisionLog_NewFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "decisions.md")

	decisions := []Decision{
		{Text: "Use nomic-embed-text", Confidence: "high"},
		{Text: "Use SQLite for storage", Confidence: "medium"},
	}

	count := AppendToDecisionLog(decisions, logPath, "test-project")
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "# Decisions & Conclusions") {
		t.Error("new file should have header")
	}
	if !strings.Contains(content, "nomic-embed-text") {
		t.Error("should contain first decision")
	}
	if !strings.Contains(content, "SQLite") {
		t.Error("should contain second decision")
	}
}

func TestAppendToDecisionLog_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "decisions.md")

	os.WriteFile(logPath, []byte("# Decisions & Conclusions\n\nExisting content.\n"), 0o644)

	decisions := []Decision{
		{Text: "Add caching layer", Confidence: "high"},
	}

	count := AppendToDecisionLog(decisions, logPath, "")
	if count != 1 {
		t.Errorf("expected 1 entry, got %d", count)
	}

	data, _ := os.ReadFile(logPath)
	content := string(data)
	if !strings.Contains(content, "Existing content") {
		t.Error("should preserve existing content")
	}
	if !strings.Contains(content, "Add caching layer") {
		t.Error("should append new decision")
	}
}

// --- FormatStaleNotesContext ---

func TestFormatStaleNotesContext_Empty(t *testing.T) {
	result := FormatStaleNotesContext(nil)
	if result != "" {
		t.Errorf("empty notes should return empty string, got %q", result)
	}
}

func TestFormatStaleNotesContext_OverdueNotes(t *testing.T) {
	notes := []StaleNote{
		{Path: "notes/old.md", Title: "Old Note", DaysOverdue: 10, ContentType: "note"},
		{Path: "notes/today.md", Title: "Today Note", DaysOverdue: 0, ContentType: "note"},
		{Path: "notes/upcoming.md", Title: "Upcoming Note", DaysOverdue: -5, ContentType: "note"},
	}

	result := FormatStaleNotesContext(notes)
	if !strings.Contains(result, "OVERDUE") {
		t.Error("should contain 'OVERDUE' for overdue notes")
	}
	if !strings.Contains(result, "due today") {
		t.Error("should contain 'due today' for zero-day notes")
	}
	if !strings.Contains(result, "upcoming") {
		t.Error("should contain 'upcoming' for future notes")
	}
	if !strings.Contains(result, "by 10 days") {
		t.Error("should show days overdue count")
	}
}

func TestFormatStaleNotesContext_LimitsFive(t *testing.T) {
	var notes []StaleNote
	for i := 0; i < 10; i++ {
		notes = append(notes, StaleNote{
			Path: "notes/note.md", Title: "Note", DaysOverdue: i,
		})
	}

	result := FormatStaleNotesContext(notes)
	// Header + 5 notes = 6 lines
	lines := strings.Split(result, "\n")
	if len(lines) != 6 {
		t.Errorf("should limit to 5 notes + header = 6 lines, got %d", len(lines))
	}
}

// --- parseDate ---

func TestParseDate_RFC3339(t *testing.T) {
	d, err := parseDate("2026-01-15T10:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if d.Year() != 2026 || d.Month() != 1 || d.Day() != 15 {
		t.Errorf("parsed date = %v, want 2026-01-15", d)
	}
}

func TestParseDate_DateOnly(t *testing.T) {
	d, err := parseDate("2026-01-15")
	if err != nil {
		t.Fatal(err)
	}
	if d.Year() != 2026 || d.Month() != 1 || d.Day() != 15 {
		t.Errorf("parsed date = %v, want 2026-01-15", d)
	}
}

func TestParseDate_SlashFormat(t *testing.T) {
	d, err := parseDate("2026/01/15")
	if err != nil {
		t.Fatal(err)
	}
	if d.Year() != 2026 || d.Month() != 1 || d.Day() != 15 {
		t.Errorf("parsed date = %v, want 2026-01-15", d)
	}
}

func TestParseDate_Invalid(t *testing.T) {
	_, err := parseDate("not a date")
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

// --- Transcript parsing ---

func TestParseTranscript_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"role":"user","content":"Hello there"}`,
		`{"role":"assistant","content":"Hi! How can I help?"}`,
		`{"role":"assistant","content":[{"type":"text","text":"Some text"},{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/foo.go"}}]}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)

	data := ParseTranscript(path)
	if len(data.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(data.Messages))
	}
	if data.Messages[0].Role != "user" {
		t.Errorf("first message role = %q, want 'user'", data.Messages[0].Role)
	}
	if len(data.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(data.ToolCalls))
	}
	if data.ToolCalls[0].Tool != "Write" {
		t.Errorf("tool name = %q, want 'Write'", data.ToolCalls[0].Tool)
	}
	if len(data.FilesChanged) != 1 {
		t.Errorf("expected 1 file changed, got %d", len(data.FilesChanged))
	}
}

func TestParseTranscript_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	os.WriteFile(path, []byte(""), 0o644)

	data := ParseTranscript(path)
	if len(data.Messages) != 0 {
		t.Errorf("empty transcript should have 0 messages, got %d", len(data.Messages))
	}
}

func TestParseTranscript_NonexistentFile(t *testing.T) {
	data := ParseTranscript("/nonexistent/file.jsonl")
	if len(data.Messages) != 0 {
		t.Errorf("nonexistent file should have 0 messages, got %d", len(data.Messages))
	}
}

func TestParseTranscript_BashFileExtraction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"echo hello > /tmp/output.txt"}}]}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)

	data := ParseTranscript(path)
	found := false
	for _, f := range data.FilesChanged {
		if f == "/tmp/output.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("should extract file from bash redirect, got: %v", data.FilesChanged)
	}
}

func TestParseTranscript_HumanRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"role":"human","content":"Hello there"}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)

	data := ParseTranscript(path)
	if len(data.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(data.Messages))
	}
	if data.Messages[0].Role != "user" {
		t.Errorf("'human' role should be mapped to 'user', got %q", data.Messages[0].Role)
	}
}

// --- GetLastNMessages ---

func TestGetLastNMessages_All(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"role":"user","content":"First"}`,
		`{"role":"assistant","content":"Second"}`,
		`{"role":"user","content":"Third"}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)

	msgs := GetLastNMessages(path, 2, "")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "Second" {
		t.Errorf("first of last 2 should be 'Second', got %q", msgs[0].Content)
	}
}

func TestGetLastNMessages_FilteredByRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"role":"user","content":"First"}`,
		`{"role":"assistant","content":"Second"}`,
		`{"role":"user","content":"Third"}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)

	msgs := GetLastNMessages(path, 10, "user")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 user messages, got %d", len(msgs))
	}
}

// --- GetSessionSummaryInputs ---

func TestGetSessionSummaryInputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"role":"user","content":"Hello"}`,
		`{"role":"assistant","content":"Hi"}`,
		`{"role":"user","content":"Do something"}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)

	inputs := GetSessionSummaryInputs(path)
	count, _ := inputs["message_count"].(int)
	if count != 3 {
		t.Errorf("message_count = %d, want 3", count)
	}
	userMsgs, _ := inputs["user_messages"].([]string)
	if len(userMsgs) != 2 {
		t.Errorf("expected 2 user messages, got %d", len(userMsgs))
	}
}

// --- GenerateHandoffNote ---

func TestGenerateHandoffNote_WithAllFields(t *testing.T) {
	content := GenerateHandoffNote(
		[]string{"Implemented feature X", "Fixed bug Y"},
		[]string{"Use Go modules"},
		"Feature X is 80% complete",
		"Finish feature X testing",
		[]string{"main.go", "config.go"},
		"test-session-id",
		"test-machine",
	)

	checks := []string{
		"content_type: handoff",
		"session_id: test-session-id",
		"machine: test-machine",
		"Implemented feature X",
		"Fixed bug Y",
		"Use Go modules",
		"Feature X is 80% complete",
		"Finish feature X testing",
		"`main.go`",
		"`config.go`",
		"auto-generated",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("handoff note should contain %q", check)
		}
	}
}

func TestGenerateHandoffNote_EmptyFields(t *testing.T) {
	content := GenerateHandoffNote(nil, nil, "", "", nil, "", "")

	if !strings.Contains(content, "(none recorded)") {
		t.Error("empty accomplishments should show '(none recorded)'")
	}
	if !strings.Contains(content, "(not recorded)") {
		t.Error("empty current state should show '(not recorded)'")
	}
	if !strings.Contains(content, "(no specific next steps noted)") {
		t.Error("empty next session should show default text")
	}
	if !strings.Contains(content, "(none)") {
		t.Error("empty files should show '(none)'")
	}
}

func TestGenerateHandoffNote_GeneratesSessionID(t *testing.T) {
	content := GenerateHandoffNote(nil, nil, "", "", nil, "", "")
	// Should auto-generate a session ID in format YYYYMMDD-HHMMSS-hex
	if !strings.Contains(content, "session_id:") {
		t.Error("should contain session_id field")
	}
}

// --- getMachineName ---

func TestGetMachineName_ReturnsHash(t *testing.T) {
	name := getMachineName()
	if !strings.HasPrefix(name, "machine-") {
		t.Errorf("machine name should start with 'machine-', got %q", name)
	}
	// Should be "machine-" + 8 hex chars
	if len(name) != len("machine-")+8 {
		t.Errorf("machine name should be 'machine-' + 8 hex chars, got %q (len %d)", name, len(name))
	}
}

// --- extractBashFilePaths ---

func TestExtractBashFilePaths(t *testing.T) {
	files := make(map[string]bool)

	extractBashFilePaths("echo hello > /tmp/out.txt && cat file | tee /tmp/log.txt", files)
	if !files["/tmp/out.txt"] {
		t.Error("should extract redirect target")
	}
	if !files["/tmp/log.txt"] {
		t.Error("should extract tee target")
	}
}

func TestExtractBashFilePaths_MvCp(t *testing.T) {
	files := make(map[string]bool)
	extractBashFilePaths("mv old.txt new.txt && cp src.txt dst.txt", files)
	if !files["new.txt"] {
		t.Error("should extract mv destination")
	}
	if !files["dst.txt"] {
		t.Error("should extract cp destination")
	}
}

func TestExtractBashFilePaths_Append(t *testing.T) {
	files := make(map[string]bool)
	extractBashFilePaths("echo line >> /tmp/append.txt", files)
	if !files["/tmp/append.txt"] {
		t.Error("should extract append redirect target")
	}
}

// --- extractTextContent ---

func TestExtractTextContent_StringContent(t *testing.T) {
	entry := map[string]interface{}{
		"content": "plain text",
	}
	result := extractTextContent(entry)
	if result != "plain text" {
		t.Errorf("got %q, want 'plain text'", result)
	}
}

func TestExtractTextContent_BlockContent(t *testing.T) {
	entry := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "text", "text": "world"},
		},
	}
	result := extractTextContent(entry)
	if result != "hello\nworld" {
		t.Errorf("got %q, want 'hello\\nworld'", result)
	}
}

func TestExtractTextContent_MixedBlocks(t *testing.T) {
	entry := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "tool_use", "name": "Write"},
		},
	}
	result := extractTextContent(entry)
	if result != "hello" {
		t.Errorf("got %q, want 'hello'", result)
	}
}

func TestExtractTextContent_NilContent(t *testing.T) {
	entry := map[string]interface{}{}
	result := extractTextContent(entry)
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

// --- extractFilesFromTool ---

func TestExtractFilesFromTool_WriteTool(t *testing.T) {
	files := make(map[string]bool)
	extractFilesFromTool("Write", map[string]interface{}{"file_path": "/tmp/test.go"}, files)
	if !files["/tmp/test.go"] {
		t.Error("should extract file_path from Write tool")
	}
}

func TestExtractFilesFromTool_EditTool(t *testing.T) {
	files := make(map[string]bool)
	extractFilesFromTool("Edit", map[string]interface{}{"file_path": "/tmp/edit.go"}, files)
	if !files["/tmp/edit.go"] {
		t.Error("should extract file_path from Edit tool")
	}
}

func TestExtractFilesFromTool_PathFallback(t *testing.T) {
	files := make(map[string]bool)
	extractFilesFromTool("Create", map[string]interface{}{"path": "/tmp/created.go"}, files)
	if !files["/tmp/created.go"] {
		t.Error("should fall back to 'path' field")
	}
}

// --- generateSessionID ---

func TestGenerateSessionID_Format(t *testing.T) {
	id := generateSessionID()
	parts := strings.Split(id, "-")
	// Format: YYYYMMDD-HHMMSS-hex (3 parts with the date being YYYYMMDD)
	if len(parts) < 3 {
		t.Errorf("session ID should have at least 3 parts, got %q", id)
	}
	if len(id) < 20 {
		t.Errorf("session ID should be at least 20 chars, got %q (len %d)", id, len(id))
	}
}

func TestGenerateSessionID_Unique(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()
	if id1 == id2 {
		t.Error("two generated session IDs should be unique")
	}
}
