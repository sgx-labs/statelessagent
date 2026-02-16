package store

import (
	"database/sql"
	"fmt"
	pathpkg "path"
	"strings"
	"time"
)

const (
	// ClaimTypeRead is an advisory read dependency on a file.
	ClaimTypeRead = "read"
	// ClaimTypeWrite is an advisory write ownership claim on a file.
	ClaimTypeWrite = "write"
	// DefaultClaimTTL is the default claim expiry window.
	DefaultClaimTTL = 30 * time.Minute
)

// ClaimRecord represents a single active or historical claim row.
type ClaimRecord struct {
	Path      string `json:"path"`
	Agent     string `json:"agent"`
	Type      string `json:"type"`
	ClaimedAt int64  `json:"claimed_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// NormalizeClaimPath validates and normalizes a relative file path for claims.
func NormalizeClaimPath(rawPath string) (string, error) {
	cleanPath := strings.TrimSpace(rawPath)
	if cleanPath == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.ContainsRune(cleanPath, 0) {
		return "", fmt.Errorf("path contains invalid null byte")
	}
	// Normalize separators so traversal checks are consistent across platforms.
	cleanPath = strings.ReplaceAll(cleanPath, "\\", "/")
	// Reject Windows drive-letter paths regardless of host OS (e.g. C:/...).
	if hasWindowsDrivePrefix(cleanPath) {
		return "", fmt.Errorf("path must be relative to the vault")
	}
	if pathpkg.IsAbs(cleanPath) {
		return "", fmt.Errorf("path must be relative to the vault")
	}
	cleanPath = pathpkg.Clean(cleanPath)
	if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, "../") || strings.Contains(cleanPath, "/../") {
		return "", fmt.Errorf("path must stay within the vault")
	}
	if strings.HasPrefix(cleanPath, "/") {
		return "", fmt.Errorf("path must be relative to the vault")
	}
	return cleanPath, nil
}

func hasWindowsDrivePrefix(p string) bool {
	if len(p) < 3 {
		return false
	}
	ch := p[0]
	isLetter := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
	return isLetter && p[1] == ':' && p[2] == '/'
}

func normalizeClaimAgent(agent string) (string, error) {
	cleanAgent := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(agent, "\n", " "), "\r", " "))
	if cleanAgent == "" {
		return "", fmt.Errorf("agent is required")
	}
	if len(cleanAgent) > 128 {
		return "", fmt.Errorf("agent name too long (max 128 chars)")
	}
	return cleanAgent, nil
}

func normalizeClaimType(claimType string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(claimType))
	if t != ClaimTypeRead && t != ClaimTypeWrite {
		return "", fmt.Errorf("claim type must be %q or %q", ClaimTypeRead, ClaimTypeWrite)
	}
	return t, nil
}

// UpsertClaim creates or refreshes an advisory claim with a TTL.
func (db *DB) UpsertClaim(path, agent, claimType string, ttl time.Duration) error {
	cleanPath, err := NormalizeClaimPath(path)
	if err != nil {
		return err
	}
	cleanAgent, err := normalizeClaimAgent(agent)
	if err != nil {
		return err
	}
	cleanType, err := normalizeClaimType(claimType)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		ttl = DefaultClaimTTL
	}

	now := time.Now().Unix()
	expires := time.Now().Add(ttl).Unix()

	db.mu.Lock()
	defer db.mu.Unlock()

	_, err = db.conn.Exec(`
		INSERT INTO claims (path, agent, type, claimed_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path, agent, type)
		DO UPDATE SET claimed_at = excluded.claimed_at, expires_at = excluded.expires_at
	`, cleanPath, cleanAgent, cleanType, now, expires)
	return err
}

// ListActiveClaims returns all unexpired claims.
func (db *DB) ListActiveClaims() ([]ClaimRecord, error) {
	rows, err := db.conn.Query(`
		SELECT path, agent, type, claimed_at, expires_at
		FROM claims
		WHERE expires_at > unixepoch()
		ORDER BY path ASC, type DESC, agent ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var claims []ClaimRecord
	for rows.Next() {
		var c ClaimRecord
		if err := rows.Scan(&c.Path, &c.Agent, &c.Type, &c.ClaimedAt, &c.ExpiresAt); err != nil {
			return nil, err
		}
		claims = append(claims, c)
	}
	return claims, rows.Err()
}

// GetActiveReadClaimsForPath returns active read claims for a file.
// If excludeAgent is provided, matching agent claims are omitted.
func (db *DB) GetActiveReadClaimsForPath(path, excludeAgent string) ([]ClaimRecord, error) {
	cleanPath, err := NormalizeClaimPath(path)
	if err != nil {
		return nil, err
	}

	var rows *sql.Rows
	if strings.TrimSpace(excludeAgent) == "" {
		r, qErr := db.conn.Query(`
			SELECT path, agent, type, claimed_at, expires_at
			FROM claims
			WHERE path = ? AND type = ? AND expires_at > unixepoch()
			ORDER BY agent ASC
		`, cleanPath, ClaimTypeRead)
		if qErr != nil {
			return nil, qErr
		}
		rows = r
	} else {
		r, qErr := db.conn.Query(`
			SELECT path, agent, type, claimed_at, expires_at
			FROM claims
			WHERE path = ? AND type = ? AND expires_at > unixepoch() AND LOWER(agent) != LOWER(?)
			ORDER BY agent ASC
		`, cleanPath, ClaimTypeRead, strings.TrimSpace(excludeAgent))
		if qErr != nil {
			return nil, qErr
		}
		rows = r
	}
	defer rows.Close()

	var claims []ClaimRecord
	for rows.Next() {
		var c ClaimRecord
		if err := rows.Scan(&c.Path, &c.Agent, &c.Type, &c.ClaimedAt, &c.ExpiresAt); err != nil {
			return nil, err
		}
		claims = append(claims, c)
	}
	return claims, rows.Err()
}

// ReleaseClaims removes claims for a path. If agent is empty, all claims for the path are removed.
func (db *DB) ReleaseClaims(path, agent string) (int64, error) {
	cleanPath, err := NormalizeClaimPath(path)
	if err != nil {
		return 0, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	var res interface {
		RowsAffected() (int64, error)
	}
	if strings.TrimSpace(agent) == "" {
		res, err = db.conn.Exec(`DELETE FROM claims WHERE path = ?`, cleanPath)
	} else {
		res, err = db.conn.Exec(`DELETE FROM claims WHERE path = ? AND LOWER(agent) = LOWER(?)`, cleanPath, strings.TrimSpace(agent))
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeExpiredClaims removes expired claims and returns the number deleted.
func (db *DB) PurgeExpiredClaims() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	res, err := db.conn.Exec(`DELETE FROM claims WHERE expires_at <= unixepoch()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
