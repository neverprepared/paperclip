package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Clipboard represents a single named sync clipboard
type Clipboard struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// RelayConfig holds Ably relay settings.
// The API key is stored in the system keychain, not here.
type RelayConfig struct {
	Clipboards []Clipboard `json:"clipboards"`
}

// EnabledClipboards returns only the clipboards that are enabled
func (r *RelayConfig) EnabledClipboards() []Clipboard {
	var out []Clipboard
	for _, c := range r.Clipboards {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

// Config holds the persistent configuration for paperclip
type Config struct {
	PollMs  int         `json:"poll_ms"`
	Verbose bool        `json:"verbose"`
	Relay   RelayConfig `json:"relay"`
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		PollMs: 500,
	}
}

// Dir returns the config directory path, creating it if needed
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library", "Application Support", "Paperclip")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// Path returns the full path to the config file
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads config from disk, returning defaults if file doesn't exist.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return DefaultConfig(), err
	}
	return LoadFrom(p)
}

// LoadFrom reads config from the given path, returning defaults if not found.
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return DefaultConfig(), err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return DefaultConfig(), err
	}
	return cfg, nil
}

// Save writes config to disk.
func Save(cfg *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(p, cfg)
}

// SaveTo writes config to the given path.
func SaveTo(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
