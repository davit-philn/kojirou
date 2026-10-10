package job

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/leotaku/kojirou/cmd/formats/selector"
	md "github.com/leotaku/kojirou/mangadex"
)

func newSite(t *testing.T) *httptest.Server {
	t.Helper()
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/series":
			_, _ = w.Write([]byte(`<div class="c"><a href="/c/3">Chapter 3</a><a href="/c/2">Chapter 2</a><a href="/c/1">Chapter 1</a></div>`))
		case strings.HasPrefix(r.URL.Path, "/c/"):
			_, _ = w.Write([]byte(`<div id="r"><img src="/i/a.png"><img src="/i/b.png"></div>`))
		case strings.HasPrefix(r.URL.Path, "/i/"):
			_, _ = w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func siteOptions(srv *httptest.Server) Options {
	return Options{
		Identifier: srv.URL + "/series",
		Source: &selector.SourceConfig{
			BaseURL:                srv.URL,
			ChapterListSelector:    ".c a",
			ImageListSelector:      "#r img",
			Title:                  "My Series",
			ChaptersPerVolume:      2,
			MaxConcurrentDownloads: 2,
		},
		Language: "en",
		Format:   FormatCBZ,
	}
}

type recorder struct {
	mu     sync.Mutex
	titles []string
	manga  *md.Manga
}

func (r *recorder) Summary(m *md.Manga) { r.manga = m }
func (r *recorder) Task(title string, vanishing bool) Progress {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.titles = append(r.titles, title)
	return nopProgress{}
}

func entries(t *testing.T, file string) []string {
	t.Helper()
	zr, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close() //nolint:errcheck
	names := make([]string, 0)
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)

	return names
}

func TestRunWritesIntoLibraryDirectory(t *testing.T) {
	srv := newSite(t)
	opts := siteOptions(srv)
	opts.LibraryDir = t.TempDir()
	rec := new(recorder)

	res, err := Run(context.Background(), opts, rec)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(opts.LibraryDir, "My Series"); res.Dir != want || res.Title != "My Series" {
		t.Errorf("result = %+v, want dir %q", res, want)
	}
	if rec.manga == nil || len(rec.manga.Chapters()) != 3 {
		t.Fatalf("summary did not see 3 chapters: %+v", rec.manga)
	}

	// two chapters per volume: chapters 1-2 in volume 1, chapter 3 in volume 2
	if got := entries(t, filepath.Join(res.Dir, "0001.cbz")); len(got) != 4+1 {
		t.Errorf("volume 1 entries = %v", got)
	}
	if got := entries(t, filepath.Join(res.Dir, "0002.cbz")); len(got) != 2+1 {
		t.Errorf("volume 2 entries = %v", got)
	}

	// running again skips existing volumes unless forced
	if _, err := Run(context.Background(), opts, NopReporter{}); err != nil {
		t.Fatal(err)
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	srv := newSite(t)
	opts := siteOptions(srv)
	opts.LibraryDir = t.TempDir()
	opts.DryRun = true

	res, err := Run(context.Background(), opts, NopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Dir != "" {
		t.Errorf("dry run reported dir %q", res.Dir)
	}
	if files, _ := os.ReadDir(opts.LibraryDir); len(files) != 0 {
		t.Errorf("dry run wrote %d entries", len(files))
	}
}

func TestInspectAndChapterIDFilter(t *testing.T) {
	srv := newSite(t)
	opts := siteOptions(srv)

	all, err := Inspect(context.Background(), opts, NopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Chapters()) != 3 {
		t.Fatalf("got %d chapters", len(all.Chapters()))
	}

	opts.ChapterIDs = []string{srv.URL + "/c/2"}
	one, err := Inspect(context.Background(), opts, NopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	chapters := one.Chapters()
	if len(chapters) != 1 || chapters[0].Info.Identifier.String() != "2" {
		t.Errorf("filter by id returned %+v", chapters)
	}
}

func TestChapterRangeFilter(t *testing.T) {
	srv := newSite(t)
	opts := siteOptions(srv)
	opts.Chapters = "1..2"

	m, err := Inspect(context.Background(), opts, NopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m.Chapters()); got != 2 {
		t.Errorf("got %d chapters, want 2", got)
	}
}

func TestValidate(t *testing.T) {
	base := Options{Identifier: "x", Format: FormatCBZ}
	tests := []struct {
		name   string
		mutate func(*Options)
		want   string
	}{
		{"format", func(o *Options) { o.Format = "pdf" }, "unknown format"},
		{"kindle folder with cbz", func(o *Options) { o.KindleFolderMode = true }, "kindle-folder-mode"},
		{"quality", func(o *Options) { o.JPEGQuality = 101 }, "jpeg-quality"},
		{"identifier", func(o *Options) { o.Identifier = "" }, "identifier"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			tt.mutate(&o)
			_, err := Run(context.Background(), o, NopReporter{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestInvalidRank(t *testing.T) {
	srv := newSite(t)
	opts := siteOptions(srv)
	opts.Rank = "views"

	_, err := Inspect(context.Background(), opts, NopReporter{})
	if err == nil || !strings.Contains(err.Error(), "not a valid ranking algorithm") {
		t.Errorf("got %v", err)
	}
}

func TestSafeDirName(t *testing.T) {
	tests := map[string]string{
		"My Series":   "My Series",
		"a/b":         "a／b",
		"..":          "untitled",
		".":           "untitled",
		"":            "untitled",
		"  ":          "untitled",
		"../../etc":   "／..／etc",
		"..hidden..":  "hidden",
		"back\\slash": "back／slash",
	}
	for in, want := range tests {
		got := SafeDirName(in)
		if got != want {
			t.Errorf("SafeDirName(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(got, "/") || got == ".." || got == "." {
			t.Errorf("SafeDirName(%q) = %q is not a single safe path element", in, got)
		}
	}
}
