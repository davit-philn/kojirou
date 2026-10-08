package selector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"base_url": "https://example.org",
		"chapter_list_selector": "ul a",
		"image_list_selector": "img",
		"image_attr": "data-src"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrentDownloads != defaultConcurrency {
		t.Errorf("concurrency = %d, want default %d", cfg.MaxConcurrentDownloads, defaultConcurrency)
	}
	if cfg.ImageAttr != "data-src" {
		t.Errorf("image_attr = %q", cfg.ImageAttr)
	}
}

func TestParseConfigErrors(t *testing.T) {
	valid := `"chapter_list_selector":"a","image_list_selector":"img"`
	tests := []struct {
		name, json, want string
	}{
		{"not json", `{`, "parse"},
		{"unknown field", `{` + valid + `,"image_atr":"src"}`, "unknown field"},
		{"missing selector", `{"chapter_list_selector":"a"}`, "image_list_selector must not be empty"},
		{"bad selector", `{"chapter_list_selector":"a[","image_list_selector":"img"}`, "invalid chapter_list_selector"},
		{"too many downloads", `{` + valid + `,"max_concurrent_downloads":50}`, "max_concurrent_downloads"},
		{"negative timeout", `{` + valid + `,"timeout_seconds":-1}`, "timeout_seconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "source.json")
	if err := os.WriteFile(file, []byte(`{"chapter_list_selector":"a","image_list_selector":"img"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(file); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(file + ".missing"); err == nil {
		t.Error("expected an error for a missing file")
	}
}
