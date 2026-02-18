package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AuditEntry is a single line in the append-only audit log.
type AuditEntry struct {
	Timestamp  string      `json:"timestamp"`
	Action     string      `json:"action"` // "scan", "review_add", "review_remove"
	FilesCount int         `json:"files_count,omitempty"`
	Passed     bool        `json:"passed"`
	Violations int         `json:"violations,omitempty"`
	Details    interface{} `json:"details,omitempty"`
}

// auditLogPath returns the path to the audit log.
func auditLogPath(vaultPath string) string {
	return filepath.Join(vaultPath, ".same", "publish-audit.log")
}

// AppendAudit appends an entry to the audit log (JSONL format).
func AppendAudit(vaultPath string, entry AuditEntry) error {
	path := auditLogPath(vaultPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}
