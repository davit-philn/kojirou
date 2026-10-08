package selector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

const (
	defaultConcurrency = 4
	maxConcurrency     = 8
)

// LoadConfig reads and validates a JSON source configuration. Unknown
// fields are rejected so that typos do not silently change behavior.
func LoadConfig(pathname string) (SourceConfig, error) {
	data, err := os.ReadFile(pathname)
	if err != nil {
		return SourceConfig{}, fmt.Errorf("read: %w", err)
	}

	return ParseConfig(data)
}

func ParseConfig(data []byte) (SourceConfig, error) {
	var cfg SourceConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return SourceConfig{}, fmt.Errorf("parse: %w", err)
	}

	if _, err := compile("chapter_list_selector", cfg.ChapterListSelector); err != nil {
		return SourceConfig{}, err
	}
	if _, err := compile("image_list_selector", cfg.ImageListSelector); err != nil {
		return SourceConfig{}, err
	}
	if cfg.TimeoutSeconds < 0 {
		return SourceConfig{}, fmt.Errorf("timeout_seconds must not be negative")
	}
	if cfg.ChaptersPerVolume < 0 {
		return SourceConfig{}, fmt.Errorf("chapters_per_volume must not be negative")
	}
	switch {
	case cfg.MaxConcurrentDownloads == 0:
		cfg.MaxConcurrentDownloads = defaultConcurrency
	case cfg.MaxConcurrentDownloads < 0 || cfg.MaxConcurrentDownloads > maxConcurrency:
		return SourceConfig{}, fmt.Errorf("max_concurrent_downloads must be between 1 and %d", maxConcurrency)
	}

	return cfg, nil
}
