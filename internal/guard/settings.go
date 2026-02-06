package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GuardConfig holds user-level guard preferences.
// Stored at ~/.config/same/config.json under the "guard" key.
type GuardConfig struct {
	Enabled     bool           `json:"enabled"`
	PII         PIIConfig      `json:"pii"`
	Blocklist   ToggleBlock    `json:"blocklist"`
	PathFilter  ToggleBlock    `json:"path_filter"`
	SoftMode    string         `json:"soft_mode"` // "block" or "warn"
	PushProtect PushProtectCfg `json:"push_protect"`
}

// PushProtectCfg controls push protection (requires same push-allow before git push).
type PushProtectCfg struct {
	Enabled bool `json:"enabled"`
	Timeout int  `json:"timeout"` // seconds, default 60
}

// PIIConfig controls which PII pattern families are active.
type PIIConfig struct {
	Enabled  bool        `json:"enabled"`
	Patterns PIIPatterns `json:"patterns"`
}

// PIIPatterns maps user-facing pattern keys to on/off.
type PIIPatterns struct {
	Email      bool `json:"email"`
	Phone      bool `json:"phone"`
	SSN        bool `json:"ssn"`
	LocalPath  bool `json:"local_path"`
	APIKey     bool `json:"api_key"`
	AWSKey     bool `json:"aws_key"`
	PrivateKey bool `json:"private_key"`
}

// ToggleBlock is a simple enabled toggle for a feature group.
type ToggleBlock struct {
	Enabled bool `json:"enabled"`
}

// DefaultGuardConfig returns the default guard configuration (everything on, block mode).
func DefaultGuardConfig() GuardConfig {
	return GuardConfig{
		Enabled: true,
		PII: PIIConfig{
			Enabled: true,
			Patterns: PIIPatterns{
				Email:      true,
				Phone:      true,
				SSN:        true,
				LocalPath:  true,
				APIKey:     true,
				AWSKey:     true,
				PrivateKey: true,
			},
		},
		Blocklist:   ToggleBlock{Enabled: true},
		PathFilter:  ToggleBlock{Enabled: true},
		SoftMode:    "block",
		PushProtect: PushProtectCfg{Enabled: false, Timeout: 60}, // off by default, user opts in
	}
}

// userFacingKeyToPatternNames maps user-facing config keys to internal pattern names.
var userFacingKeyToPatternNames = map[string][]string{
	"email":       {"email"},
	"phone":       {"us_phone"},
	"ssn":         {"ssn"},
	"local_path":  {"local_path_unix", "local_path_windows"},
	"api_key":     {"api_key_assignment", "sk_key"},
	"aws_key":     {"aws_key"},
	"private_key": {"private_key_header"},
}

// EnabledPatternNames returns the set of internal pattern names that are enabled.
func (c *GuardConfig) EnabledPatternNames() map[string]bool {
	if !c.Enabled || !c.PII.Enabled {
		return nil
	}
	enabled := make(map[string]bool)
	pats := c.PII.Patterns

	addIfEnabled := func(on bool, key string) {
		if on {
			for _, name := range userFacingKeyToPatternNames[key] {
				enabled[name] = true
			}
		}
	}

	addIfEnabled(pats.Email, "email")
	addIfEnabled(pats.Phone, "phone")
	addIfEnabled(pats.SSN, "ssn")
	addIfEnabled(pats.LocalPath, "local_path")
	addIfEnabled(pats.APIKey, "api_key")
	addIfEnabled(pats.AWSKey, "aws_key")
	addIfEnabled(pats.PrivateKey, "private_key")

	return enabled
}

// SetKey sets a user-facing setting by key name. Returns error for unknown keys.
func (c *GuardConfig) SetKey(key, value string) error {
	boolVal := value == "on" || value == "true" || value == "yes"

	switch key {
	case "guard":
		c.Enabled = boolVal
	case "pii":
		c.PII.Enabled = boolVal
	case "blocklist":
		c.Blocklist.Enabled = boolVal
	case "path-filter", "path_filter":
		c.PathFilter.Enabled = boolVal
	case "soft-mode", "soft_mode":
		if value == "block" || value == "warn" {
			c.SoftMode = value
		} else {
			return fmt.Errorf("soft-mode must be 'block' or 'warn', got %q", value)
		}
	case "email":
		c.PII.Patterns.Email = boolVal
	case "phone":
		c.PII.Patterns.Phone = boolVal
	case "ssn":
		c.PII.Patterns.SSN = boolVal
	case "local_path":
		c.PII.Patterns.LocalPath = boolVal
	case "api_key":
		c.PII.Patterns.APIKey = boolVal
	case "aws_key":
		c.PII.Patterns.AWSKey = boolVal
	case "private_key":
		c.PII.Patterns.PrivateKey = boolVal
	case "push-protect", "push_protect":
		c.PushProtect.Enabled = boolVal
	case "push-timeout", "push_timeout":
		var timeout int
		if _, err := fmt.Sscanf(value, "%d", &timeout); err != nil {
			return fmt.Errorf("push-timeout must be a number (seconds), got %q", value)
		}
		if timeout < 10 || timeout > 300 {
			return fmt.Errorf("push-timeout must be between 10 and 300 seconds")
		}
		c.PushProtect.Timeout = timeout
	default:
		return fmt.Errorf("unknown setting key: %q", key)
	}
	return nil
}

// guardConfigPath returns the path to the user-level guard config.
func guardConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "same", "config.json")
}

// configFile is the full user config file structure (extends existing).
type configFile struct {
	MachineName string       `json:"machine_name,omitempty"`
	Guard       *GuardConfig `json:"guard,omitempty"`
}

// LoadGuardConfig loads the guard config from ~/.config/same/config.json.
// Returns defaults if file doesn't exist or guard key is absent.
func LoadGuardConfig() GuardConfig {
	data, err := os.ReadFile(guardConfigPath())
	if err != nil {
		return DefaultGuardConfig()
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultGuardConfig()
	}
	if cfg.Guard == nil {
		return DefaultGuardConfig()
	}
	return *cfg.Guard
}

// SaveGuardConfig writes the guard config back to ~/.config/same/config.json,
// preserving other fields (e.g. machine_name).
func SaveGuardConfig(gc GuardConfig) error {
	path := guardConfigPath()

	// Read existing to preserve other keys
	var cfg configFile
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &cfg)
	}

	cfg.Guard = &gc

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
