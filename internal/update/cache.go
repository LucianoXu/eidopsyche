package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Cache is the on-disk update-check state.
type Cache struct {
	CheckedAt      time.Time `json:"checked_at"`
	LatestVersion  string    `json:"latest_version"`
	CurrentVersion string    `json:"current_version"`
	LastShownAt    time.Time `json:"last_shown_at"`
}

// cachePath returns the per-user cache file path, honouring XDG_CACHE_HOME.
// On error (e.g., HOME unset), returns an empty string — callers treat this
// as "no cache" and skip persistence.
func cachePath() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "eidos", "update-check.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "eidos", "update-check.json")
}

// loadCache reads the cache file. Missing file is not an error — returns
// (nil, nil). Malformed file is treated the same way: callers will overwrite
// it on the next refresh.
func loadCache() (*Cache, error) {
	p := cachePath()
	if p == "" {
		return nil, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, nil
	}
	return &c, nil
}

// saveCache atomically writes the cache file (write-then-rename).
func saveCache(c *Cache) error {
	p := cachePath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
