package job

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
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

func TestVolumeOverridesMoveChaptersByHand(t *testing.T) {
	srv := newSite(t)
	opts := siteOptions(srv) // two chapters per volume: 1 and 2 in volume 1, 3 in volume 2
	volumes := func(opts Options) map[string]string {
		manga, err := Inspect(context.Background(), opts, NopReporter{})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, c := range manga.Chapters() {
			got[c.Info.Identifier.String()] = c.Info.VolumeIdentifier.String()
		}

		return got
	}
	if got := volumes(opts); got["1"] != "1" || got["3"] != "2" {
		t.Fatalf("without overrides: %v", got)
	}

	opts.VolumeOverrides = map[string]string{
		srv.URL + "/c/1": "7",
		srv.URL + "/c/3": "Special",
		srv.URL + "/c/9": "4", // unknown chapters are ignored
	}
	got := volumes(opts)
	if got["1"] != "7" || got["2"] != "1" || got["3"] != "Special" || len(got) != 3 {
		t.Errorf("with overrides: %v, want chapter 1 in volume 7, 2 unchanged, 3 in Special", got)
	}

	opts.VolumeOverrides = map[string]string{srv.URL + "/c/1": "abc"}
	if _, err := Inspect(context.Background(), opts, NopReporter{}); err == nil {
		t.Error("a volume that is no number must be rejected")
	}
}

func TestSeriesCoverBecomesTheCoverOfEveryVolume(t *testing.T) {
	srv := newSite(t)
	opts := siteOptions(srv) // chapters 1 and 2 in volume 1, chapter 3 in volume 2
	opts.CoverURL = srv.URL + "/i/cover.png"

	covers := func(opts Options) (md.ImageList, *md.Manga) {
		src := newContentSource(opts)
		manga, err := inspect(context.Background(), src, opts, NopReporter{})
		if err != nil {
			t.Fatal(err)
		}
		list, err := getCovers(context.Background(), src, opts, NopReporter{}, manga)
		if err != nil {
			t.Fatal(err)
		}

		return list, manga
	}
	list, manga := covers(opts)
	if len(list) != 2 || len(manga.Volumes) != 2 {
		t.Fatalf("got %d covers for %d volumes, want one per volume", len(list), len(manga.Volumes))
	}
	for _, c := range list {
		if c.Image == nil {
			t.Error("a cover has no image")
		}
	}

	// A cover that cannot be fetched must not stop the download.
	opts.CoverURL = srv.URL + "/missing.png"
	if list, _ := covers(opts); len(list) != 0 {
		t.Errorf("got %d covers from a broken address, want none", len(list))
	}
	broken := newContentSource(opts)
	brokenManga, err := inspect(context.Background(), broken, opts, NopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	var steps recordedSteps
	if _, err := getCovers(context.Background(), broken, opts, &steps, brokenManga); err != nil {
		t.Fatal(err)
	}
	if problem := broken.(interface{ CoverProblem() string }).CoverProblem(); !strings.Contains(problem, "series cover could not be downloaded") {
		t.Errorf("problem = %q, want the reason the cover is missing", problem)
	}
	if got := steps.cancelled["Covers"]; !strings.Contains(got, "series cover could not be downloaded") {
		t.Errorf("the Covers step ended with %q, want it to say why there is no cover", got)
	}
	opts.CoverURL = ""
	if list, _ := covers(opts); len(list) != 0 {
		t.Errorf("got %d covers without an address, want none", len(list))
	}
}

func TestCoverPageReplacesTheCoversOfMatchingVolumes(t *testing.T) {
	srv := newSite(t)
	// A second server plays the shop with a page of volume cards; its volume 2 matches.
	shopImage := new(bytes.Buffer)
	if err := png.Encode(shopImage, image.NewGray(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	var shop *httptest.Server
	shop = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/series/x":
			var b strings.Builder
			for i := 1; i <= 4; i++ {
				b.WriteString(`<div><a href="/volume/V` + string(rune('0'+i)) + `/x-vol-` + string(rune('0'+i)) + `"><img srcset="/c/` + string(rune('0'+i)) + `.png 480w"></a></div>`)
			}
			_, _ = w.Write([]byte(b.String()))
		case strings.HasPrefix(r.URL.Path, "/c/"):
			_, _ = w.Write(shopImage.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(shop.Close)

	opts := siteOptions(srv) // volumes 1 and 2
	opts.CoverPage = shop.URL + "/series/x"
	src := newContentSource(opts)
	manga, err := inspect(context.Background(), src, opts, NopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	covers, err := getCovers(context.Background(), src, opts, NopReporter{}, manga)
	if err != nil {
		t.Fatal(err)
	}
	if len(covers) != 2 {
		t.Fatalf("got %d covers, want one for each of the 2 volumes (the shop's volumes 3 and 4 do not exist here)", len(covers))
	}
	for _, c := range covers {
		if b := c.Image.Bounds(); b.Dx() != 8 || b.Dy() != 12 {
			t.Errorf("cover of volume %v is %v, not the shop's picture", c.VolumeIdentifier, b)
		}
	}

	// An unreadable page must not stop the download: no covers, no error.
	opts.CoverPage = shop.URL + "/nothing"
	if covers, err := getCovers(context.Background(), src, opts, NopReporter{}, manga); err != nil || len(covers) != 0 {
		t.Errorf("broken cover page: %d covers, %v", len(covers), err)
	}

	opts.CoverPage = "ftp://example.org/x"
	if _, err := Inspect(context.Background(), opts, NopReporter{}); err == nil {
		t.Error("a cover page that is no http(s) address must be rejected")
	}
}

// recordedSteps is a Reporter that remembers how each step was cancelled.
type recordedSteps struct{ cancelled map[string]string }

type recordedStep struct {
	steps *recordedSteps
	title string
}

func (r *recordedSteps) Summary(*md.Manga) {}
func (r *recordedSteps) Task(title string, _ bool) Progress {
	return recordedStep{r, title}
}

func (s recordedStep) Increase(int) {}
func (s recordedStep) Add(int)      {}
func (s recordedStep) NewProxyWriter(w io.Writer) io.Writer {
	return w
}
func (s recordedStep) Done() {}
func (s recordedStep) Cancel(message string) {
	if s.steps.cancelled == nil {
		s.steps.cancelled = map[string]string{}
	}
	s.steps.cancelled[s.title] = message
}
