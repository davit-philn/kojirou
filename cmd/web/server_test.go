package web

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leotaku/kojirou/cmd/job"
	md "github.com/leotaku/kojirou/mangadex"
)

const testHost = "127.0.0.1:8080"

type env struct {
	t       *testing.T
	srv     *Server
	handler http.Handler
	library string
	site    *httptest.Server
}

func newEnv(t *testing.T, mutate func(*Config)) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	buf := new(bytes.Buffer)
	if err := png.Encode(buf, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/series":
			_, _ = w.Write([]byte(`<div class="c"><a href="/c/2">Chapter 2</a><a href="/c/1">Chapter 1</a></div>`))
		case strings.HasPrefix(r.URL.Path, "/c/"):
			_, _ = w.Write([]byte(`<div id="r"><img src="/i/a.png"><img src="/i/b.png"></div>`))
		case strings.HasPrefix(r.URL.Path, "/i/"):
			_, _ = w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)

	dir := t.TempDir()
	cfg := Config{Addr: testHost, LibraryDir: filepath.Join(dir, "library"), ConfigDir: filepath.Join(dir, "config")}
	if mutate != nil {
		mutate(&cfg)
	}
	srv, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}

	return &env{t: t, srv: srv, handler: srv.Handler(), library: srv.cfg.LibraryDir, site: site}
}

func (e *env) do(method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = testHost
	if method != http.MethodGet {
		req.Header.Set(clientHeader, "1")
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("bad JSON (%d): %v\n%s", rec.Code, err, rec.Body.String())
	}

	return v
}

func (e *env) saveSource(name string) {
	e.t.Helper()
	cfg := map[string]any{
		"base_url":              e.site.URL,
		"chapter_list_selector": ".c a",
		"image_list_selector":   "#r img",
		"title":                 "Fake Series",
	}
	data, _ := json.Marshal(cfg)
	rec := e.do("POST", "/api/sources", map[string]any{"name": name, "config": json.RawMessage(data)})
	if rec.Code != 200 {
		e.t.Fatalf("save source: %d %s", rec.Code, rec.Body)
	}
}

func TestGuardRejectsForeignRequests(t *testing.T) {
	e := newEnv(t, nil)

	tests := []struct {
		name   string
		method string
		path   string
		host   string
		origin string
		client bool
		want   int
	}{
		{"ok", "GET", "/api/config", testHost, "", false, 200},
		{"localhost alias", "GET", "/api/config", "localhost:8080", "", false, 200},
		{"rebound dns name", "GET", "/api/config", "evil.example:8080", "", false, 403},
		{"cross-origin read", "GET", "/api/config", testHost, "https://evil.example", false, 403},
		{"same-origin", "GET", "/api/config", testHost, "http://" + testHost, false, 200},
		{"post without header", "POST", "/api/downloads", testHost, "", false, 403},
		{"post with header", "POST", "/api/downloads", testHost, "", true, 400},
		{"delete without header", "DELETE", "/api/sources/x", testHost, "", false, 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader("{}"))
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.client {
				req.Header.Set(clientHeader, "1")
			}
			rec := httptest.NewRecorder()
			e.handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestStaticIndexIsServed(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("GET", "/", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<") {
		t.Errorf("index: %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Error("static files should not be cached")
	}
}

func TestSearchUsesMangaDex(t *testing.T) {
	var query url.Values
	dexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		_, _ = w.Write([]byte(`{"result":"ok","total":1,"data":[{"id":"11111111-1111-1111-1111-111111111111",
			"attributes":{"title":{"en":"Found"},"description":{}},"relationships":[]}]}`))
	}))
	defer dexSrv.Close()
	base, _ := url.Parse(dexSrv.URL + "/")
	covers, _ := url.Parse("https://c.example/covers/")

	e := newEnv(t, func(c *Config) { c.Dex = md.NewClient().WithBaseURLs(*base, *covers) })
	rec := e.do("GET", "/api/search?q=dragon&lang=vi&adult=1&offset=24", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	res := decode[struct {
		Items []md.SeriesSummary
		Total int
	}](t, rec)
	if res.Total != 1 || res.Items[0].Title != "Found" {
		t.Errorf("unexpected result: %+v", res)
	}
	if query.Get("title") != "dragon" || query.Get("offset") != "24" || len(query["contentRating[]"]) != 4 {
		t.Errorf("query not forwarded: %v", query)
	}

	for _, bad := range []string{"/api/search?lang=../x", "/api/search?order=evil"} {
		if rec := e.do("GET", bad, nil); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400", bad, rec.Code)
		}
	}
}

func TestSourcesCRUDAndValidation(t *testing.T) {
	e := newEnv(t, nil)
	e.saveSource("demo")

	list := decode[[]SavedSource](t, e.do("GET", "/api/sources", nil))
	if len(list) != 1 || list[0].Name != "demo" || list[0].Config.MaxConcurrentDownloads != 4 {
		t.Errorf("list = %+v", list)
	}
	cfg := decode[struct{ Sources []string }](t, e.do("GET", "/api/config", nil))
	if len(cfg.Sources) != 2 || cfg.Sources[0] != "mangadex" {
		t.Errorf("sources in config = %v", cfg.Sources)
	}

	good := json.RawMessage(`{"chapter_list_selector":"a","image_list_selector":"img"}`)
	for _, tt := range []struct {
		name string
		body any
	}{
		{"reserved name", map[string]any{"name": "mangadex", "config": good}},
		{"path-like name", map[string]any{"name": "../x", "config": good}},
		{"bad selector", map[string]any{"name": "x", "config": json.RawMessage(`{"chapter_list_selector":"a[","image_list_selector":"img"}`)}},
		{"unknown field", map[string]any{"name": "x", "config": json.RawMessage(`{"chapter_list_selector":"a","image_list_selector":"img","oops":1}`)}},
		{"unknown request field", map[string]any{"name": "x", "config": good, "extra": 1}},
	} {
		if rec := e.do("POST", "/api/sources", tt.body); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", tt.name, rec.Code, rec.Body)
		}
	}

	if rec := e.do("DELETE", "/api/sources/demo", nil); rec.Code != 200 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := e.do("DELETE", "/api/sources/demo", nil); rec.Code != 404 {
		t.Errorf("second delete: %d", rec.Code)
	}
	if info, err := os.Stat(filepath.Join(e.srv.cfg.ConfigDir, "sources.json")); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Errorf("sources file should exist and be private: %v %v", info, err)
	}
}

func TestTestSourceEndpoint(t *testing.T) {
	e := newEnv(t, nil)
	cfg, _ := json.Marshal(map[string]any{
		"base_url":              e.site.URL,
		"chapter_list_selector": ".c a",
		"image_list_selector":   "#r img",
	})
	rec := e.do("POST", "/api/sources/test", map[string]any{"config": json.RawMessage(cfg), "url": e.site.URL + "/series"})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	res := decode[map[string]any](t, rec)
	if res["chapterCount"] != float64(2) || res["imageCount"] != float64(2) {
		t.Errorf("result = %v", res)
	}

	for _, bad := range []string{"file:///etc/passwd", "ftp://x/y", "not a url"} {
		rec := e.do("POST", "/api/sources/test", map[string]any{"config": json.RawMessage(cfg), "url": bad})
		if rec.Code != 400 {
			t.Errorf("%q: status %d, want 400", bad, rec.Code)
		}
	}
}

func TestSeriesFromSelectorSource(t *testing.T) {
	e := newEnv(t, nil)
	e.saveSource("demo")

	rec := e.do("GET", "/api/series?source=demo&id="+url.QueryEscape(e.site.URL+"/series"), nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	s := decode[seriesDTO](t, rec)
	if s.Title != "Fake Series" || len(s.Volumes) != 1 || len(s.Volumes[0].Chapters) != 2 {
		t.Fatalf("series = %+v", s)
	}
	if s.Volumes[0].Chapters[0].Number != "1" || s.Volumes[0].Chapters[0].ID != e.site.URL+"/c/1" {
		t.Errorf("chapters = %+v", s.Volumes[0].Chapters)
	}

	for _, bad := range []string{
		"/api/series?source=nope&id=http://x/y",
		"/api/series?source=demo&id=file:///etc/passwd",
		"/api/series?id=not-a-uuid",
		"/api/series?source=demo&id=http://x/y&lang=%00",
	} {
		if rec := e.do("GET", bad, nil); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400", bad, rec.Code)
		}
	}
}

func waitFor(t *testing.T, e *env, id string, states ...string) JobInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, j := range decode[[]JobInfo](t, e.do("GET", "/api/downloads", nil)) {
			if j.ID == id {
				for _, s := range states {
					if j.State == s {
						return j
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %v", id, states)

	return JobInfo{}
}

func TestDownloadEndToEndAndLibrary(t *testing.T) {
	e := newEnv(t, nil)
	e.saveSource("demo")
	id := e.site.URL + "/series"

	rec := e.do("POST", "/api/downloads", map[string]any{
		"source": "demo", "id": id, "title": "Fake Series", "lang": "en",
		"chapterIds": []string{e.site.URL + "/c/1"}, "format": "cbz",
	})
	if rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	started := decode[JobInfo](t, rec)
	done := waitFor(t, e, started.ID, "done", "failed")
	if done.State != "done" {
		t.Fatalf("job ended %s: %s", done.State, done.Error)
	}
	if done.Chapters != 1 || done.Title != "Fake Series" || done.Source != "demo" {
		t.Errorf("job = %+v", done)
	}

	lib := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
	if len(lib) != 1 || lib[0].Name != "Fake Series" || len(lib[0].Files) != 1 || lib[0].Files[0].Format != "cbz" {
		t.Fatalf("library = %+v", lib)
	}

	file := e.do("GET", "/api/library/"+url.PathEscape("Fake Series")+"/"+lib[0].Files[0].Name, nil)
	if file.Code != 200 || !strings.HasPrefix(file.Body.String(), "PK") {
		t.Errorf("file: %d, body starts %q", file.Code, firstBytes(file.Body.String()))
	}
	if cd := file.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "Fake Series - ") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func firstBytes(s string) string {
	if len(s) > 8 {
		return s[:8]
	}

	return s
}

func TestLibraryFileCannotEscape(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.library, "Series"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.library, "Series", "0001.cbz"), []byte("PKdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(filepath.Dir(e.library), "secret.cbz")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(e.library, "Series", "0002.cbz")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.WriteFile(filepath.Join(e.library, "Series", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		want int
	}{
		{"/api/library/Series/0001.cbz", 200},
		{"/api/library/Series/0002.cbz", 404},        // symlink out of the library
		{"/api/library/Series/notes.txt", 404},       // not an e-book
		{"/api/library/..%2F/secret.cbz", 404},       // traversal in series
		{"/api/library/Series/..%2Fsecret.cbz", 404}, // traversal in file
		{"/api/library/Series/missing.cbz", 404},
	}
	for _, tt := range tests {
		if rec := e.do("GET", tt.path, nil); rec.Code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.path, rec.Code, tt.want)
		}
	}
	lib := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
	if len(lib) != 1 || len(lib[0].Files) != 1 {
		t.Errorf("library should list only the real e-book: %+v", lib)
	}
}

func TestDownloadRequestValidation(t *testing.T) {
	e := newEnv(t, nil)
	e.saveSource("demo")
	id := e.site.URL + "/series"
	ok := map[string]any{"source": "demo", "id": id, "chapterIds": []string{"x"}}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		m[k] = v
		return m
	}

	tests := map[string]map[string]any{
		"no chapters":   with("chapterIds", []string{}),
		"bad format":    with("format", "pdf"),
		"bad quality":   with("jpegQuality", 500),
		"bad widepage":  with("widepage", "x"),
		"bad saver":     with("dataSaver", "x"),
		"bad language":  with("lang", "e n"),
		"unknown field": with("outDir", "/etc"),
		"unknown src":   with("source", "nope"),
		"mangadex id":   with("source", "mangadex"),
	}
	for name, body := range tests {
		if rec := e.do("POST", "/api/downloads", body); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
}

func TestCancelQueuedAndRunningDownloads(t *testing.T) {
	started := make(chan struct{}, 1)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	e.saveSource("demo")
	body := map[string]any{"source": "demo", "id": e.site.URL + "/series", "chapterIds": []string{"x"}}

	first := decode[JobInfo](t, e.do("POST", "/api/downloads", body))
	<-started
	second := decode[JobInfo](t, e.do("POST", "/api/downloads", body))
	if second.State != "queued" {
		t.Errorf("second job state = %s, want queued", second.State)
	}

	if rec := e.do("DELETE", "/api/downloads/"+second.ID, nil); rec.Code != 200 {
		t.Errorf("cancel queued: %d", rec.Code)
	}
	if rec := e.do("DELETE", "/api/downloads/"+first.ID, nil); rec.Code != 200 {
		t.Errorf("cancel running: %d", rec.Code)
	}
	waitFor(t, e, first.ID, "canceled")
	waitFor(t, e, second.ID, "canceled")
	if rec := e.do("DELETE", "/api/downloads/nope", nil); rec.Code != 404 {
		t.Errorf("unknown job: %d", rec.Code)
	}
}

func TestEmbeddedInterfaceIsComplete(t *testing.T) {
	e := newEnv(t, nil)
	index := e.do("GET", "/", nil).Body.String()
	for _, asset := range []string{"style.css", "app.js"} {
		if !strings.Contains(index, asset) {
			t.Errorf("index.html does not reference %s", asset)
		}
		if rec := e.do("GET", "/"+asset, nil); rec.Code != 200 || rec.Body.Len() == 0 {
			t.Errorf("%s: status %d, %d bytes", asset, rec.Code, rec.Body.Len())
		}
	}
	if rec := e.do("GET", "/api/nope", nil); rec.Code != 404 && rec.Code != 405 {
		t.Errorf("unknown API path: status %d", rec.Code)
	}
}
