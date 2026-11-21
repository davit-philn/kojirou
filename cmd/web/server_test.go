package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/leotaku/kojirou/cmd/multi"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/formats/kindle"
	"github.com/leotaku/kojirou/cmd/formats/wikipedia"
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
		case r.URL.Path == "/fillseries":
			_, _ = w.Write([]byte(`<div class="c"><a href="/c/5">Chapter 5</a><a href="/c/3">Chapter 3</a><a href="/c/2">Chapter 2</a></div>`))
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

func TestFailuresAreLoggedToTheTerminal(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	e := newEnv(t, nil)
	if rec := e.do("GET", "/api/series?id=not-a-uuid", nil); rec.Code != 400 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "/api/series?id=not-a-uuid -> 400") || !strings.Contains(logs.String(), "invalid MangaDex series id") {
		t.Errorf("failure not logged: %q", logs.String())
	}

	logs.Reset()
	if rec := e.do("GET", "/api/config", nil); rec.Code != 200 || logs.Len() != 0 {
		t.Errorf("successful requests should not be logged: %q", logs.String())
	}
}

const mdID = "11111111-1111-4111-8111-111111111111"

// fakeMangaDex serves just enough of the API for listing chapters:
// chapters 1 and 2 in volume 1 and chapter 4 in volume 2, all Vietnamese.
func fakeMangaDex(t *testing.T) *md.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manga/"+mdID+"/feed":
			chapter := func(id, num, vol string) string {
				return `{"id":"` + id + `","type":"chapter","attributes":{"title":"","volume":"` + vol + `","chapter":"` + num +
					`","pages":3,"translatedLanguage":"vi","publishAt":"2024-05-01T10:00:00+00:00"},` +
					`"relationships":[{"id":"22222222-2222-4222-8222-222222222222","type":"scanlation_group"}]}`
			}
			_, _ = w.Write([]byte(`{"result":"ok","total":3,"data":[` +
				chapter("c1", "1", "1") + `,` + chapter("c2", "2", "1") + `,` + chapter("c4", "4", "2") + `]}`))
		case r.URL.Path == "/manga/"+mdID+"/aggregate":
			// MangaDex knows chapter 3 and 6 only here (e.g. English or external ones).
			_, _ = w.Write([]byte(`{"result":"ok","volumes":{` +
				`"1":{"volume":"1","chapters":{"1":{"chapter":"1"},"2":{"chapter":"2"}}},` +
				`"2":{"volume":"2","chapters":{"4":{"chapter":"4"}}},` +
				`"none":{"volume":"none","chapters":{"6":{"chapter":"6"}}}}}`))
		case r.URL.Path == "/manga/"+mdID:
			_, _ = w.Write([]byte(`{"result":"ok","data":{"id":"` + mdID + `","attributes":{"title":{"en":"Main Title"},"description":{}},` +
				`"relationships":[{"id":"a1","type":"author","attributes":{"name":"Writer"}}]}}`))
		case r.URL.Path == "/author":
			_, _ = w.Write([]byte(`{"result":"ok","total":0,"data":[]}`))
		case r.URL.Path == "/group":
			_, _ = w.Write([]byte(`{"result":"ok","total":1,"data":[{"id":"22222222-2222-4222-8222-222222222222","attributes":{"name":"Grp"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	api, _ := url.Parse(srv.URL + "/")
	covers, _ := url.Parse(srv.URL + "/covers/")
	client := md.NewClient().WithBaseURLs(*api, *covers)
	download.SetMangadexClient(client)
	t.Cleanup(func() { download.SetMangadexClient(md.NewClient()) })

	return client
}

func TestSeriesWithFillSource(t *testing.T) {
	dex := fakeMangaDex(t)
	e := newEnv(t, func(c *Config) { c.Dex = dex })
	e.saveSource("demo")

	q := url.Values{"source": {"mangadex"}, "id": {mdID}, "lang": {"vi"}}
	plain := decode[seriesDTO](t, e.do("GET", "/api/series?"+q.Encode(), nil))
	if len(plain.Volumes) != 2 || len(plain.Notes) != 0 {
		t.Fatalf("without a fill source: %+v", plain)
	}

	q.Set("fillSource", "demo")
	q.Set("fillUrl", e.site.URL+"/fillseries")
	rec := e.do("GET", "/api/series?"+q.Encode(), nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[seriesDTO](t, rec)

	numbers := map[string]string{} // chapter number -> volume
	filled := map[string]bool{}
	for _, v := range got.Volumes {
		for _, c := range v.Chapters {
			numbers[c.Number] = v.ID
			filled[c.Number] = c.FromFill
		}
	}
	wantVol := map[string]string{"1": "1", "2": "1", "3": "1", "4": "2", "5": "Special"}
	wantFill := map[string]bool{"1": false, "2": false, "3": true, "4": false, "5": true}
	for n, v := range wantVol {
		if numbers[n] != v || filled[n] != wantFill[n] {
			t.Errorf("chapter %s: volume %q fromFill %v, want volume %q fromFill %v", n, numbers[n], filled[n], v, wantFill[n])
		}
	}
	if len(numbers) != 5 {
		t.Errorf("got chapters %v, want exactly 1-5 (chapter 2 exists on both sources)", numbers)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "chapter 3 ") {
		t.Errorf("notes = %v, want one about chapter 3", got.Notes)
	}
	if got.Title != "Main Title" {
		t.Errorf("title must come from MangaDex, got %q", got.Title)
	}
}

func TestSeriesFillValidation(t *testing.T) {
	fakeMangaDex(t)
	e := newEnv(t, nil)
	e.saveSource("demo")
	page := url.QueryEscape(e.site.URL + "/fillseries")

	for name, query := range map[string]string{
		"only a source":         "source=mangadex&id=" + mdID + "&fillSource=demo",
		"only a url":            "source=mangadex&id=" + mdID + "&fillUrl=" + page,
		"unknown source":        "source=mangadex&id=" + mdID + "&fillSource=nope&fillUrl=" + page,
		"non-http url":          "source=mangadex&id=" + mdID + "&fillSource=demo&fillUrl=file%3A%2F%2F%2Fetc%2Fpasswd",
		"fill on selector main": "source=demo&id=" + page + "&fillSource=demo&fillUrl=" + page,
	} {
		if rec := e.do("GET", "/api/series?"+query, nil); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
}

func TestLinksCRUD(t *testing.T) {
	e := newEnv(t, nil)
	e.saveSource("demo")
	link := map[string]string{"source": "demo", "url": e.site.URL + "/fillseries"}

	if rec := e.do("PUT", "/api/links/"+mdID, link); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	all := decode[map[string]Link](t, e.do("GET", "/api/links", nil))
	if all[mdID].Source != "demo" || all[mdID].URL != link["url"] {
		t.Errorf("links = %v", all)
	}

	for name, tt := range map[string]struct {
		path string
		body map[string]string
	}{
		"bad id":         {"/api/links/not-a-uuid", link},
		"unknown source": {"/api/links/" + mdID, map[string]string{"source": "nope", "url": link["url"]}},
		"bad url":        {"/api/links/" + mdID, map[string]string{"source": "demo", "url": "ftp://x/y"}},
		"missing fields": {"/api/links/" + mdID, map[string]string{}},
	} {
		if rec := e.do("PUT", tt.path, tt.body); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	if rec := e.do("PUT", "/api/links/"+mdID, nil); rec.Code != 400 {
		t.Errorf("empty body: status %d", rec.Code)
	}

	if rec := e.do("DELETE", "/api/links/"+mdID, nil); rec.Code != 200 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := e.do("DELETE", "/api/links/"+mdID, nil); rec.Code != 404 {
		t.Errorf("second delete: %d", rec.Code)
	}
	if all := decode[map[string]Link](t, e.do("GET", "/api/links", nil)); len(all) != 0 {
		t.Errorf("links after delete: %v", all)
	}
}

func TestDownloadRequestCarriesFillSource(t *testing.T) {
	got := make(chan job.Options, 1)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			got <- opts
			return &job.Result{}, nil
		}
	})
	e.saveSource("demo")
	page := e.site.URL + "/fillseries"

	rec := e.do("POST", "/api/downloads", map[string]any{
		"source": "mangadex", "id": mdID, "lang": "vi", "chapterIds": []string{"c1", page},
		"fill": map[string]string{"source": "demo", "url": page},
	})
	if rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	opts := <-got
	if opts.Fill == nil || opts.Fill.URL != page || opts.Fill.Config.ImageListSelector != "#r img" {
		t.Errorf("fill source not passed to the job: %+v", opts.Fill)
	}
	if opts.Source != nil || opts.Identifier != mdID {
		t.Errorf("MangaDex must stay the main source: %+v", opts)
	}

	for name, fill := range map[string]map[string]string{
		"unknown source": {"source": "nope", "url": page},
		"half a fill":    {"source": "demo"},
	} {
		rec := e.do("POST", "/api/downloads", map[string]any{
			"source": "mangadex", "id": mdID, "chapterIds": []string{"c1"}, "fill": fill,
		})
		if rec.Code != 400 {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// scanSite is a small manga-like website with a home page, series pages and
// chapter pages, used to check automatic detection end to end.
func scanSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/":
			_, _ = w.Write([]byte(`<html><head><title>Home</title></head><body><div class="grid">` +
				`<a href="/series/a">A</a><a href="/series/b">B</a><a href="/series/c">C</a><a href="/series/d">D</a></div></body></html>`))
		case strings.HasPrefix(p, "/series/"):
			var b strings.Builder
			b.WriteString(`<html><body><h1>Series</h1><ul class="chapter-list">`)
			for i := 6; i >= 1; i-- {
				fmt.Fprintf(&b, `<li><a href="/read/a/%d">Chương %d</a></li>`, i, i)
			}
			b.WriteString(`</ul></body></html>`)
			_, _ = w.Write([]byte(b.String()))
		case strings.HasPrefix(p, "/read/"):
			_, _ = w.Write([]byte(`<html><body><div class="reader"><img src="/i/1.jpg"><img src="/i/2.jpg"><img src="/i/3.jpg"></div></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestDetectEndpointFindsAndSavesASource(t *testing.T) {
	e := newEnv(t, nil)
	site := scanSite(t)

	// the user must confirm the rights first
	if rec := e.do("POST", "/api/sources/detect", map[string]any{"url": site.URL}); rec.Code != 400 {
		t.Errorf("without confirmation: status %d, want 400", rec.Code)
	}
	if rec := e.do("POST", "/api/sources/detect", map[string]any{"url": "nonsense", "confirmRights": true}); rec.Code != 422 {
		t.Errorf("bad address: status %d, want 422", rec.Code)
	}
	if rec := e.do("POST", "/api/sources/detect", map[string]any{"url": site.URL, "confirmRights": true, "extra": 1}); rec.Code != 400 {
		t.Errorf("unknown field: status %d, want 400", rec.Code)
	}

	rec := e.do("POST", "/api/sources/detect", map[string]any{"url": site.URL + "/", "confirmRights": true})
	if rec.Code != 200 {
		t.Fatalf("detect: %d %s", rec.Code, rec.Body)
	}
	det := decode[struct {
		Config       json.RawMessage `json:"config"`
		SeriesURL    string          `json:"seriesUrl"`
		ChapterURL   string          `json:"chapterUrl"`
		ChapterCount int             `json:"chapterCount"`
		ImageCount   int             `json:"imageCount"`
		Notes        []string        `json:"notes"`
	}](t, rec)
	if det.ChapterCount != 6 || det.ImageCount != 3 || len(det.Notes) == 0 {
		t.Fatalf("detection = %+v", det)
	}

	// the proposal can be saved as it is and then works like any other source
	if rec := e.do("POST", "/api/sources", map[string]any{"name": "scanned", "config": det.Config}); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	rec = e.do("GET", "/api/series?source=scanned&id="+url.QueryEscape(det.SeriesURL), nil)
	if rec.Code != 200 {
		t.Fatalf("series: %d %s", rec.Code, rec.Body)
	}
	series := decode[seriesDTO](t, rec)
	if n := len(series.Volumes[0].Chapters); n != 6 {
		t.Errorf("the saved source lists %d chapters, want 6", n)
	}
}

func TestDetectEndpointExplainsFailures(t *testing.T) {
	e := newEnv(t, nil)
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Nothing here</title></head><body><a href="/x">x</a></body></html>`))
	}))
	defer empty.Close()

	rec := e.do("POST", "/api/sources/detect", map[string]any{"url": empty.URL, "confirmRights": true})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "no chapter list was found") {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestSeriesWithVolumesFromMangaDex(t *testing.T) {
	dex := fakeMangaDex(t)
	e := newEnv(t, func(c *Config) { c.Dex = dex })
	e.saveSource("demo")
	page := e.site.URL + "/fillseries" // chapters 5, 3 and 2

	q := url.Values{"source": {"demo"}, "id": {page}, "lang": {"vi"}, "volumesFrom": {"https://mangadex.org/title/" + mdID + "/slug"}}
	rec := e.do("GET", "/api/series?"+q.Encode(), nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[seriesDTO](t, rec)

	numbers := map[string]string{}
	for _, v := range got.Volumes {
		for _, c := range v.Chapters {
			numbers[c.Number] = v.ID
			if c.FromFill {
				t.Errorf("chapter %s is flagged as filled", c.Number)
			}
		}
	}
	want := map[string]string{"2": "1", "3": "1", "5": "2"} // 5 is newer than every volume: the last one
	for n, v := range want {
		if numbers[n] != v {
			t.Errorf("chapter %s in volume %q, want %q", n, numbers[n], v)
		}
	}
	if len(numbers) != 3 {
		t.Errorf("got chapters %v, want only the website's 2, 3 and 5", numbers)
	}
	if got.Title != "Main Title" || got.VolumesFrom != mdID {
		t.Errorf("title %q volumesFrom %q, want MangaDex's series", got.Title, got.VolumesFrom)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "chapter 3 ") {
		t.Errorf("notes = %v, want one about chapter 3", got.Notes)
	}

	for name, query := range map[string]string{
		"not a series": "source=demo&id=" + url.QueryEscape(page) + "&volumesFrom=nope",
		"on MangaDex":  "source=mangadex&id=" + mdID + "&volumesFrom=" + mdID,
		"with fill":    "source=demo&id=" + url.QueryEscape(page) + "&volumesFrom=" + mdID + "&fillSource=demo&fillUrl=" + url.QueryEscape(page),
	} {
		if rec := e.do("GET", "/api/series?"+query, nil); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
}

func TestDownloadRequestCarriesVolumesFrom(t *testing.T) {
	got := make(chan job.Options, 1)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			got <- opts
			return &job.Result{}, nil
		}
	})
	e.saveSource("demo")
	page := e.site.URL + "/series"

	rec := e.do("POST", "/api/downloads", map[string]any{
		"source": "demo", "id": page, "chapterIds": []string{"x"}, "volumesFrom": mdID,
	})
	if rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if opts := <-got; opts.VolumesFrom != mdID || opts.Source == nil || opts.Identifier != page {
		t.Errorf("volumes-from not passed to the job: %+v", opts)
	}
}

func TestSeriesWithVolumesFromMangaDexAndWikipedia(t *testing.T) {
	dex := fakeMangaDex(t)
	wiki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "query":
			_, _ = w.Write([]byte(`{"query":{"search":[{"title":"List of Main Title chapters"}]}}`))
		default:
			// MangaDex only knows chapters 1, 2 and 4; Wikipedia also knows 3 and 5.
			_, _ = w.Write([]byte(`{"parse":{"wikitext":"== Main Title volumes ==\n{{Graphic novel list\n|VolumeNumber = 1\n|ChapterListCol1 =\n*1. a\n*2. b\n*3. c\n}}\n{{Graphic novel list\n|VolumeNumber = 2\n|ChapterListCol1 =\n*4. d\n*5. e\n}}\n"}}`))
		}
	}))
	t.Cleanup(wiki.Close)
	e := newEnv(t, func(c *Config) { c.Dex = dex; c.Wikipedia = wikipedia.NewWithAPI(wiki.URL) })
	e.saveSource("demo")

	q := url.Values{"source": {"demo"}, "id": {e.site.URL + "/fillseries"}, "lang": {"vi"}, "volumesFrom": {mdID}}
	rec := e.do("GET", "/api/series?"+q.Encode(), nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[seriesDTO](t, rec)

	numbers := map[string]string{}
	for _, v := range got.Volumes {
		for _, c := range v.Chapters {
			numbers[c.Number] = v.ID
		}
	}
	want := map[string]string{"2": "1", "3": "1", "5": "2"} // 3 and 5 only because of Wikipedia
	for n, v := range want {
		if numbers[n] != v {
			t.Errorf("chapter %s in volume %q, want %q", n, numbers[n], v)
		}
	}
	if len(got.Notes) != 0 {
		t.Errorf("notes = %v, want none: nothing was guessed", got.Notes)
	}
	if len(got.Info) != 1 || !strings.Contains(got.Info[0], "List of Main Title chapters") {
		t.Errorf("info = %v, want where the volumes came from", got.Info)
	}
}

func TestDownloadRequestCarriesVolumeOverrides(t *testing.T) {
	got := make(chan job.Options, 1)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			got <- opts
			return &job.Result{}, nil
		}
	})
	e.saveSource("demo")
	page := e.site.URL + "/series"
	start := func(overrides map[string]string) int {
		return e.do("POST", "/api/downloads", map[string]any{
			"source": "demo", "id": page, "chapterIds": []string{"x"}, "volumeOverrides": overrides,
		}).Code
	}

	if code := start(map[string]string{"x": "3", "y": "3.5", "z": "Special"}); code != 202 {
		t.Fatalf("valid overrides: status %d", code)
	}
	if opts := <-got; opts.VolumeOverrides["x"] != "3" || opts.VolumeOverrides["z"] != "Special" || len(opts.VolumeOverrides) != 3 {
		t.Errorf("overrides not passed to the job: %v", opts.VolumeOverrides)
	}
	for name, overrides := range map[string]map[string]string{
		"not a number": {"x": "three"},
		"empty":        {"x": ""},
		"too long":     {"x": "12345678901234567"},
		"negative":     {"x": "-1"},
	} {
		if code := start(overrides); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
}

// catalogueSite serves a list of series with covers, a next page and a search
// result page; covers are only served with the right Referer.
func catalogueSite(t *testing.T) *httptest.Server {
	t.Helper()
	cover := new(bytes.Buffer)
	if err := png.Encode(cover, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		card := func(i int) string {
			return fmt.Sprintf(`<li class="card"><a href="/truyen/series-%d"><img data-original="/covers/%d.png" src="/placeholder.gif"></a><h3>Series %d</h3></li>`, i, i, i)
		}
		switch {
		case r.URL.Path == "/list" && r.URL.Query().Get("page") == "2":
			_, _ = w.Write([]byte(`<ul class="cards">` + card(4) + card(5) + card(6) + `</ul>`))
		case r.URL.Path == "/list":
			_, _ = w.Write([]byte(`<ul class="cards">` + card(1) + card(2) + card(3) + `</ul><a class="next" href="/list?page=2">Next</a>`))
		case r.URL.Path == "/search" && r.URL.Query().Get("q") == "one piece":
			_, _ = w.Write([]byte(`<ul class="cards">` + card(7) + card(8) + card(9) + `</ul>`))
		case strings.HasPrefix(r.URL.Path, "/covers/"):
			if r.Header.Get("Referer") != srv.URL+"/" {
				http.Error(w, "hotlinking", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(cover.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestBrowseACatalogueSource(t *testing.T) {
	site := catalogueSite(t)
	e := newEnv(t, nil)
	cfg, _ := json.Marshal(map[string]any{
		"base_url": site.URL, "referer": site.URL + "/", "chapter_list_selector": "a", "image_list_selector": "img",
		"list_url": "/list", "series_selector": ".card a", "next_page_selector": "a.next", "search_url": "/search?q={q}",
	})
	if rec := e.do("POST", "/api/sources", map[string]any{"name": "shop", "config": json.RawMessage(cfg)}); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	e.saveSource("plain") // not browsable

	conf := decode[map[string]any](t, e.do("GET", "/api/config", nil))
	catalogues, _ := conf["catalogues"].(map[string]any)
	if _, ok := catalogues["shop"]; !ok || len(catalogues) != 1 {
		t.Fatalf("catalogues = %v, want only the browsable source", catalogues)
	}

	if hosts, _ := conf["hosts"].(map[string]any); hosts["shop"] != "127.0.0.1" || hosts["plain"] != "127.0.0.1" {
		t.Errorf("hosts = %v, want each source's website so that a pasted link finds its source", hosts)
	}

	first := decode[browseResponse](t, e.do("GET", "/api/browse?source=shop", nil))
	if len(first.Items) != 3 || first.Items[0].Title != "Series 1" || first.Next != site.URL+"/list?page=2" {
		t.Fatalf("first page = %+v", first)
	}
	if first.Items[0].CoverURL != site.URL+"/covers/1.png" {
		t.Errorf("cover = %q, want the lazy-loading attribute", first.Items[0].CoverURL)
	}

	second := decode[browseResponse](t, e.do("GET", "/api/browse?source=shop&page="+url.QueryEscape(first.Next), nil))
	if len(second.Items) != 3 || second.Items[0].Title != "Series 4" || second.Next != "" {
		t.Errorf("second page = %+v", second)
	}
	found := decode[browseResponse](t, e.do("GET", "/api/browse?source=shop&q=one+piece", nil))
	if len(found.Items) != 3 || found.Items[0].Title != "Series 7" {
		t.Errorf("search = %+v", found)
	}

	// The cover is fetched through the server with the source's Referer.
	rec := e.do("GET", "/api/cover?source=shop&url="+url.QueryEscape(first.Items[0].CoverURL), nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() == 0 {
		t.Errorf("cover: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	// ...but only addresses that a browse result contained.
	for name, target := range map[string]string{"unknown address": site.URL + "/covers/99.png", "other host": "http://127.0.0.1:1/x.png"} {
		if rec := e.do("GET", "/api/cover?source=shop&url="+url.QueryEscape(target), nil); rec.Code != 404 {
			t.Errorf("%s: status %d, want 404", name, rec.Code)
		}
	}

	for name, query := range map[string]string{
		"unknown source":       "source=nope",
		"not browsable":        "source=plain",
		"another website":      "source=shop&page=" + url.QueryEscape("http://evil.example/list"),
		"search text too long": "source=shop&q=" + strings.Repeat("a", 300),
	} {
		if rec := e.do("GET", "/api/browse?"+query, nil); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
}

func TestSeriesOfACustomSourceIsNamedAfterTheCatalogue(t *testing.T) {
	e := newEnv(t, nil)
	e.saveSource("demo")
	id := url.QueryEscape(e.site.URL + "/series")

	got := decode[seriesDTO](t, e.do("GET", "/api/series?source=demo&id="+id+"&title="+url.QueryEscape("Blue Lock"), nil))
	if got.Title != "Blue Lock" {
		t.Errorf("title = %q, want the catalogue's name", got.Title)
	}
	if plain := decode[seriesDTO](t, e.do("GET", "/api/series?source=demo&id="+id, nil)); plain.Title != "Fake Series" {
		t.Errorf("without a title the source's own title is used, got %q", plain.Title)
	}
}

func TestDownloadUsesOnlyCoversThatBrowseReturned(t *testing.T) {
	site := catalogueSite(t)
	got := make(chan job.Options, 4)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			got <- opts
			return &job.Result{}, nil
		}
	})
	cfg, _ := json.Marshal(map[string]any{
		"base_url": site.URL, "referer": site.URL + "/", "chapter_list_selector": "a", "image_list_selector": "img",
		"list_url": "/list", "series_selector": ".card a",
	})
	if rec := e.do("POST", "/api/sources", map[string]any{"name": "shop", "config": json.RawMessage(cfg)}); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	browsed := decode[browseResponse](t, e.do("GET", "/api/browse?source=shop", nil))
	cover := browsed.Items[0].CoverURL
	start := func(cover string) job.Options {
		rec := e.do("POST", "/api/downloads", map[string]any{
			"source": "shop", "id": site.URL + "/truyen/series-1", "chapterIds": []string{"x"}, "coverUrl": cover,
		})
		if rec.Code != 202 {
			t.Fatalf("start: %d %s", rec.Code, rec.Body)
		}

		return <-got
	}

	if opts := start(cover); opts.CoverURL != cover {
		t.Errorf("CoverURL = %q, want the address browse returned", opts.CoverURL)
	}
	if opts := start("http://127.0.0.1:1/secret.png"); opts.CoverURL != "" {
		t.Errorf("CoverURL = %q: an address no browse result contained must be ignored", opts.CoverURL)
	}
}

func TestCoverPagePreviewAndDownloadRequest(t *testing.T) {
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/series/x" {
			_, _ = w.Write([]byte(`<div><a href="/volume/A/x-vol-1"><img srcset="/c/1.png 480w"></a></div><div><a href="/volume/B/x-vol-2"><img srcset="/c/2.png 480w"></a></div>`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(shop.Close)
	got := make(chan job.Options, 2)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			got <- opts
			return &job.Result{}, nil
		}
	})
	e.saveSource("demo")

	preview := decode[struct {
		Covers []struct{ Volume, URL string }
	}](t, e.do("GET", "/api/covers/preview?url="+url.QueryEscape(shop.URL+"/series/x"), nil))
	if len(preview.Covers) != 2 || preview.Covers[1].Volume != "2" || preview.Covers[1].URL != shop.URL+"/c/2.png" {
		t.Errorf("preview = %+v", preview)
	}
	if rec := e.do("GET", "/api/covers/preview?url="+url.QueryEscape(shop.URL+"/nothing"), nil); rec.Code != 422 {
		t.Errorf("a page without covers: status %d, want 422", rec.Code)
	}

	page := e.site.URL + "/series"
	start := func(coverPage string) int {
		return e.do("POST", "/api/downloads", map[string]any{
			"source": "demo", "id": page, "chapterIds": []string{"x"}, "coverPage": coverPage,
		}).Code
	}
	if code := start(shop.URL + "/series/x"); code != 202 {
		t.Fatalf("start: %d", code)
	}
	if opts := <-got; opts.CoverPage != shop.URL+"/series/x" {
		t.Errorf("CoverPage = %q, want the page to be passed to the job", opts.CoverPage)
	}
	for _, bad := range []string{"ftp://x/y", "javascript:alert(1)", "/relative"} {
		if code := start(bad); code != 400 {
			t.Errorf("cover page %q: status %d, want 400", bad, code)
		}
	}
}

// writeComic makes a CBZ whose picture 00001 is marked as the front cover in
// ComicInfo.xml and whose first picture is something else (an advertisement).
func writeComic(t *testing.T, path string, coverIndex int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	zw := zip.NewWriter(f)
	for i := 0; i < 3; i++ {
		w, err := zw.Create(fmt.Sprintf("%05d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		// Every picture has its own size so that the test can tell them apart.
		if err := png.Encode(w, image.NewGray(image.Rect(0, 0, 600+i*100, 900+i*100))); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := zw.Create("ComicInfo.xml")
	_, _ = fmt.Fprintf(w, `<ComicInfo><Pages><Page Image="0"/><Page Image="%d" Type="FrontCover"/></Pages></ComicInfo>`, coverIndex)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryCoverThumbnails(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.library, "Series"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeComic(t, filepath.Join(e.library, "Series", "0001.cbz"), 1)
	if err := os.WriteFile(filepath.Join(e.library, "Series", "0002.azw3"), []byte("mobi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.library, "Series", "0003.cbz"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := e.do("GET", "/api/library/Series/0001.cbz/cover", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("cover: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	img, err := jpeg.Decode(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	// The marked cover is picture 1 (700x1000, shrunk to 240 wide), not the first one (600x900).
	if b := img.Bounds(); b.Dx() != 240 || b.Dy() != 1000*240/700 {
		t.Errorf("thumbnail is %v, want 240x%d (the picture ComicInfo.xml marks as the cover)", b, 1000*240/700)
	}

	// Cached the second time, and only the library's own CBZ files are served.
	if again := e.do("GET", "/api/library/Series/0001.cbz/cover", nil); again.Code != 200 {
		t.Errorf("second request: %d", again.Code)
	}
	for name, path := range map[string]string{
		"a mobi book":       "/api/library/Series/0002.azw3/cover",
		"a broken archive":  "/api/library/Series/0003.cbz/cover",
		"a missing file":    "/api/library/Series/9999.cbz/cover",
		"traversal":         "/api/library/..%2F/0001.cbz/cover",
		"traversal in file": "/api/library/Series/..%2F0001.cbz/cover",
	} {
		if rec := e.do("GET", path, nil); rec.Code != 404 {
			t.Errorf("%s: status %d, want 404", name, rec.Code)
		}
	}
}

// fakeDexWithCovers answers a search for "Blue Lock", describes the series and
// lists its covers: volume 1 and 2 as PNG pictures of 10x15 and 12x18 pixels.
func fakeDexWithCovers(t *testing.T, brokenPictures ...bool) *md.Client {
	t.Helper()
	broken := len(brokenPictures) > 0 && brokenPictures[0]
	const id = "44444444-4444-4444-8444-444444444444"
	picture := func(w, h int) []byte {
		buf := new(bytes.Buffer)
		if err := png.Encode(buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
			t.Fatal(err)
		}

		return buf.Bytes()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manga" && r.URL.Query().Get("title") != "":
			_, _ = w.Write([]byte(`{"result":"ok","total":1,"data":[{"id":"` + id + `","attributes":{"title":{"en":"Blue Lock"},"description":{}},"relationships":[]}]}`))
		case r.URL.Path == "/manga/"+id:
			_, _ = w.Write([]byte(`{"result":"ok","data":{"id":"` + id + `","attributes":{"title":{"en":"Blue Lock"},"description":{}},"relationships":[]}}`))
		case r.URL.Path == "/author":
			_, _ = w.Write([]byte(`{"result":"ok","total":0,"data":[]}`))
		case r.URL.Path == "/cover":
			_, _ = w.Write([]byte(`{"result":"ok","total":2,"data":[` +
				`{"id":"c1","attributes":{"volume":"1","fileName":"one.png"}},{"id":"c2","attributes":{"volume":"2","fileName":"two.png"}}]}`))
		case broken && strings.HasPrefix(r.URL.Path, "/covers/"):
			http.NotFound(w, r)
		case r.URL.Path == "/covers/"+id+"/one.png":
			_, _ = w.Write(picture(10, 15))
		case r.URL.Path == "/covers/"+id+"/two.png":
			_, _ = w.Write(picture(12, 18))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	api, _ := url.Parse(srv.URL + "/")
	covers, _ := url.Parse(srv.URL + "/covers")
	client := md.NewClient().WithBaseURLs(*api, *covers)
	download.SetMangadexClient(client)
	t.Cleanup(func() { download.SetMangadexClient(md.NewClient()) })

	return client
}

func TestUpdateCoversOfTheLibrary(t *testing.T) {
	dex := fakeDexWithCovers(t)
	e := newEnv(t, func(c *Config) { c.Dex = dex })
	old := time.Now().Add(-time.Hour)
	for _, series := range []string{"Blue Lock", "Mystery Series"} {
		if err := os.MkdirAll(filepath.Join(e.library, series), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"0001.cbz", "0002.cbz"} {
			path := filepath.Join(e.library, series, file)
			writeComic(t, path, 1)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}

	post := func(body map[string]any) (libraryCoversResult, int) {
		rec := e.do("POST", "/api/library/covers", body)
		if rec.Code != 200 {
			return libraryCoversResult{}, rec.Code
		}

		return decode[libraryCoversResult](t, rec), rec.Code
	}

	// The name decides: "Blue Lock" is the title of the only search result.
	got, code := post(map[string]any{"series": "Blue Lock"})
	if code != 200 || got.Status != "updated" || got.Updated != 2 || got.Matched != "Blue Lock" {
		t.Fatalf("Blue Lock: %d %+v", code, got)
	}
	thumb := e.do("GET", "/api/library/Blue%20Lock/0002.cbz/cover", nil)
	img, err := jpeg.Decode(thumb.Body)
	if err != nil || img.Bounds().Dx() != 12 || img.Bounds().Dy() != 18 {
		t.Errorf("volume 2 now shows a %v cover (%v), want MangaDex's 12x18 picture", img, err)
	}

	// Running it again is harmless, and the series is remembered.
	again, _ := post(map[string]any{"series": "Blue Lock"})
	if again.Updated != 2 {
		t.Errorf("second run: %+v", again)
	}
	if meta, _ := e.srv.meta.Get("Blue Lock"); meta.MangaDexID == "" {
		t.Error("the MangaDex series was not remembered")
	}

	// A name that matches nothing exactly asks for a choice and changes nothing.
	unmatched, _ := post(map[string]any{"series": "Mystery Series"})
	if unmatched.Status != "unmatched" || len(unmatched.Candidates) != 1 || unmatched.Candidates[0].Title != "Blue Lock" || unmatched.Updated != 0 {
		t.Errorf("Mystery Series: %+v", unmatched)
	}
	chosen, _ := post(map[string]any{"series": "Mystery Series", "mangadex": "https://mangadex.org/title/44444444-4444-4444-8444-444444444444/blue-lock"})
	if chosen.Updated != 2 {
		t.Errorf("with the series chosen: %+v", chosen)
	}

	for name, body := range map[string]map[string]any{
		"unknown series": {"series": "Nope"},
		"bad link":       {"series": "Blue Lock", "mangadex": "not a link"},
		"bad cover page": {"series": "Blue Lock", "coverPage": "ftp://x"},
		"traversal":      {"series": "../Blue Lock"},
	} {
		if _, code := post(body); code != 404 && code != 400 {
			t.Errorf("%s: status %d, want 404 or 400", name, code)
		}
	}
}

func TestUpdateCoversSkipsFilesThatAreBeingWritten(t *testing.T) {
	dex := fakeDexWithCovers(t)
	e := newEnv(t, func(c *Config) { c.Dex = dex })
	if err := os.MkdirAll(filepath.Join(e.library, "Blue Lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeComic(t, filepath.Join(e.library, "Blue Lock", "0001.cbz"), 1) // just written

	got := decode[libraryCoversResult](t, e.do("POST", "/api/library/covers", map[string]any{"series": "Blue Lock"}))
	if got.Updated != 0 || !strings.Contains(strings.Join(got.Notes, " "), "being written") {
		t.Errorf("a file written a moment ago must be left alone: %+v", got)
	}
}

// When the picture server cannot be reached, the update gives up after two
// covers in a row instead of waiting out every volume.
func TestUpdateCoversGivesUpWhenPicturesCannotBeDownloaded(t *testing.T) {
	dex := fakeDexWithCovers(t, true)
	e := newEnv(t, func(c *Config) { c.Dex = dex })
	old := time.Now().Add(-time.Hour)
	if err := os.MkdirAll(filepath.Join(e.library, "Blue Lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"0001.cbz", "0002.cbz"} {
		path := filepath.Join(e.library, "Blue Lock", file)
		writeComic(t, path, 1)
		_ = os.Chtimes(path, old, old)
	}

	got := decode[libraryCoversResult](t, e.do("POST", "/api/library/covers", map[string]any{"series": "Blue Lock"}))
	notes := strings.Join(got.Notes, " | ")
	if got.Updated != 0 || got.Status != "nothing" || !strings.Contains(notes, "cover download failed") || !strings.Contains(notes, "unreachable") {
		t.Errorf("result = %+v, want the failure explained", got)
	}
}

func TestDownloadRequestCarriesTheDeviceSettings(t *testing.T) {
	got := make(chan job.Options, 2)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			got <- opts
			return &job.Result{}, nil
		}
	})
	e.saveSource("demo")
	start := func(extra map[string]any) int {
		body := map[string]any{"source": "demo", "id": e.site.URL + "/series", "chapterIds": []string{"x"}}
		for k, v := range extra {
			body[k] = v
		}

		return e.do("POST", "/api/downloads", body).Code
	}

	if code := start(map[string]any{"device": "kindle-pw3", "fitMode": "smart", "enhance": true, "widepage": "rotate"}); code != 202 {
		t.Fatalf("valid settings: status %d", code)
	}
	if opts := <-got; opts.Device != "kindle-pw3" || opts.FitMode != "smart" || !opts.Enhance || opts.Widepage != kindle.WidepagePolicyRotate {
		t.Errorf("settings not passed on: %+v", opts)
	}
	for name, extra := range map[string]map[string]any{
		"unknown device":   {"device": "palm-pilot"},
		"unknown fit mode": {"device": "kindle-pw3", "fitMode": "squash"},
	} {
		if code := start(extra); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
}

func TestPreviewShowsPagesAsTheyWillBeWritten(t *testing.T) {
	e := newEnv(t, nil)
	e.saveSource("demo")
	rec := e.do("POST", "/api/preview", map[string]any{
		"source": "demo", "id": e.site.URL + "/series", "chapterIds": []string{e.site.URL + "/c/1"},
		"device": "kindle-pw3", "fitMode": "fit", "enhance": true,
	})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[struct {
		Pages  []previewPageJSON
		Screen struct{ Width, Height int }
	}](t, rec)
	if len(got.Pages) != 2 || got.Screen.Width != 1072 || got.Screen.Height != 1448 {
		t.Fatalf("preview = %d pages, screen %+v", len(got.Pages), got.Screen)
	}
	raw := strings.TrimPrefix(got.Pages[0].After, "data:image/jpeg;base64,")
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 536 || cfg.Height != 724 {
		t.Errorf("the preview of a page on the screen is %dx%d (%v), want 536x724 (half of 1072x1448)", cfg.Width, cfg.Height, err)
	}

	if rec := e.do("POST", "/api/preview", map[string]any{"source": "demo", "id": e.site.URL + "/series", "chapterIds": []string{}}); rec.Code != 400 {
		t.Errorf("no chapter: status %d, want 400", rec.Code)
	}
	if rec := e.do("POST", "/api/preview", map[string]any{"source": "demo", "id": e.site.URL + "/series", "chapterIds": []string{"x"}, "device": "nope"}); rec.Code != 400 {
		t.Errorf("bad device: status %d, want 400", rec.Code)
	}
}

func TestConvertALibrarySeriesForADevice(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.library, "Series"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeComic(t, filepath.Join(e.library, "Series", "0001.cbz"), 1)
	original, _ := os.ReadFile(filepath.Join(e.library, "Series", "0001.cbz"))

	rec := e.do("POST", "/api/library/convert", map[string]any{"series": "Series", "device": "kindle-pw3", "fitMode": "fit", "enhance": true})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[convertResult](t, rec)
	if got.Converted != 1 || got.Failed != 0 || got.Output != "Series [kindle-pw3]" {
		t.Fatalf("result = %+v", got)
	}

	out := filepath.Join(e.library, "Series [kindle-pw3]", "0001.cbz")
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close() //nolint:errcheck
	pictures := 0
	var infoXML string
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close() //nolint:errcheck
		if f.Name == "ComicInfo.xml" {
			infoXML = string(data)
			continue
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != 1072 || cfg.Height != 1448 {
			t.Errorf("%s is %dx%d (%v), want 1072x1448", f.Name, cfg.Width, cfg.Height, err)
		}
		pictures++
	}
	if pictures != 3 || !strings.Contains(infoXML, `Type="FrontCover"`) {
		t.Errorf("%d pictures, ComicInfo %q: the cover must stay the cover", pictures, infoXML)
	}
	if after, _ := os.ReadFile(filepath.Join(e.library, "Series", "0001.cbz")); !bytes.Equal(after, original) {
		t.Error("the original file must not be changed")
	}
	lib := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
	if len(lib) != 2 {
		t.Errorf("the converted copy should show as its own series: %+v", lib)
	}

	// One page, before and after.
	rawPage := e.do("GET", "/api/library/Series/0001.cbz/preview?page=0&raw=1", nil)
	fitted := e.do("GET", "/api/library/Series/0001.cbz/preview?page=0&device=kindle-pw3&fitMode=fit", nil)
	rawCfg, err1 := jpeg.DecodeConfig(rawPage.Body)
	fitCfg, err2 := jpeg.DecodeConfig(fitted.Body)
	if err1 != nil || err2 != nil || rawCfg.Width != 360 || fitCfg.Width != 536 || fitCfg.Height != 724 {
		t.Errorf("preview sizes: raw %dx%d (%v), fitted %dx%d (%v)", rawCfg.Width, rawCfg.Height, err1, fitCfg.Width, fitCfg.Height, err2)
	}

	for name, body := range map[string]map[string]any{
		"nothing to apply": {"series": "Series"},
		"bad device":       {"series": "Series", "device": "nope"},
		"unknown series":   {"series": "Nope", "device": "kindle-pw3"},
	} {
		if rec := e.do("POST", "/api/library/convert", body); rec.Code != 400 && rec.Code != 404 {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	if rec := e.do("GET", "/api/library/Series/9999.cbz/preview", nil); rec.Code != 404 {
		t.Errorf("a missing file: status %d, want 404", rec.Code)
	}
}

// bannerPage is a 200 wide picture of 280 rows of its own content, with a
// 60 row banner on top ("top") or at the bottom ("bottom") if asked.
func bannerPage(seed int, banner string) image.Image {
	const w, content, bannerH = 200, 280, 60
	h := content
	if banner != "" {
		h += bannerH
	}
	g := image.NewGray(image.Rect(0, 0, w, h))
	block := func(kind, bx, by int) uint8 {
		z := uint64(kind)*0x9E3779B97F4A7C15 + uint64(bx)*0xBF58476D1CE4E5B9 + uint64(by)*0x94D049BB133111EB
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		return uint8(30 + (z^(z>>31))%200)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var v uint8
			switch {
			case banner == "top" && y < bannerH:
				v = block(900, x*16/w, y*16/w)
			case banner == "bottom" && y >= h-bannerH:
				v = block(901, x*16/w, (y-(h-bannerH))*16/w)
			default:
				yy := y
				if banner == "top" {
					yy -= bannerH
				}
				v = block(seed, x*12/w, yy*12/w)
			}
			g.Pix[y*g.Stride+x] = v
		}
	}

	return g
}

func TestCleanAdvertisingStripsFromALibrarySeries(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.library, "Series"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.library, "Series", "0001.cbz")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	banners := []string{"top", "", "bottom", "top", "", "bottom"} // two chapters of three pages
	for i, banner := range banners {
		w, _ := zw.Create(fmt.Sprintf("%05d.png", i))
		if err := png.Encode(w, bannerPage(i+1, banner)); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := zw.Create("ComicInfo.xml")
	_, _ = w.Write([]byte(`<ComicInfo><Series>Series</Series></ComicInfo>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close() //nolint:errcheck
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
	before, _ := os.ReadFile(path)

	// Looking first changes nothing.
	look := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "Series"}))
	if look.Pages != 4 || look.Strips != 2 || len(look.Files) != 1 || look.Before == "" || look.After == "" {
		t.Fatalf("scan = %+v", look)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Fatal("scanning must not change any file")
	}

	done := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "Series", "apply": true}))
	if done.Pages != 4 {
		t.Fatalf("apply = %+v", done)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close() //nolint:errcheck
	heights := map[string]int{}
	for _, file := range zr.File {
		if file.Name == "ComicInfo.xml" {
			continue
		}
		rc, _ := file.Open()
		cfg, _, err := image.DecodeConfig(rc)
		rc.Close() //nolint:errcheck
		if err != nil {
			t.Fatal(err)
		}
		heights[file.Name] = cfg.Height
	}
	for name, h := range heights {
		if h < 275 || h > 280 {
			t.Errorf("%s is %d high, want 275 to 280 (every banner cut, content kept)", name, h)
		}
	}
	if len(heights) != 6 {
		t.Errorf("%d pictures left, want 6", len(heights))
	}
	if got := e.srv.ads.Get("series:Series"); len(got) != 2 {
		t.Errorf("%d strips remembered, want 2", len(got))
	}

	// Nothing is left to cut the second time.
	again := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "Series", "apply": true}))
	if again.Pages != 0 {
		t.Errorf("second run found %d pages with a strip", again.Pages)
	}
	if rec := e.do("POST", "/api/library/ads", map[string]any{"series": "Nope"}); rec.Code != 404 {
		t.Errorf("unknown series: status %d", rec.Code)
	}
}

func TestDownloadRequestAsksToStripAdsOnlyForCustomSources(t *testing.T) {
	got := make(chan job.Options, 2)
	e := newEnv(t, func(c *Config) {
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			got <- opts
			return &job.Result{}, nil
		}
	})
	e.saveSource("demo")
	if rec := e.do("POST", "/api/downloads", map[string]any{"source": "demo", "id": e.site.URL + "/series", "chapterIds": []string{"x"}, "stripAds": true}); rec.Code != 202 {
		t.Fatalf("status %d", rec.Code)
	}
	if opts := <-got; !opts.StripAds || opts.OnAdStrips == nil {
		t.Errorf("stripAds not passed on: %+v", opts)
	}
	if rec := e.do("POST", "/api/downloads", map[string]any{"source": "mangadex", "id": mdID, "chapterIds": []string{"x"}, "stripAds": true}); rec.Code != 202 {
		t.Fatalf("status %d", rec.Code)
	}
	if opts := <-got; opts.StripAds {
		t.Error("MangaDex pages carry no ads of a website, so nothing is cut")
	}
}

func TestWholeAdvertisingPagesAreRemovedFromALibrarySeries(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.library, "Ads"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Three chapters' worth of story pages, each ending with the same advert.
	names := []string{}
	for c := 0; c < 3; c++ {
		path := filepath.Join(e.library, "Ads", fmt.Sprintf("%04d.cbz", c+1))
		f, _ := os.Create(path)
		zw := zip.NewWriter(f)
		for i := 0; i < 4; i++ {
			seed := c*10 + i + 1
			if i == 3 {
				seed = 777 // the advertising page, identical in every file
			}
			w, _ := zw.Create(fmt.Sprintf("%05d.png", i))
			if err := png.Encode(w, bannerPage(seed, "")); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		f.Close() //nolint:errcheck
		old := time.Now().Add(-time.Hour)
		_ = os.Chtimes(path, old, old)
		names = append(names, path)
	}
	done := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "Ads", "apply": true}))
	if done.Dropped != 3 {
		t.Fatalf("apply = %+v, want 3 advertising pages dropped", done)
	}
	for _, path := range names {
		zr, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(zr.File) != 3 {
			t.Errorf("%s has %d entries, want 3", filepath.Base(path), len(zr.File))
		}
		zr.Close() //nolint:errcheck
	}
}

func TestConvertedCopiesAreNotConvertedAgain(t *testing.T) {
	for name, want := range map[string]bool{
		"Blue Lock [kindle-pw3]": true, "Blue Lock [converted]": true,
		"Blue Lock": false, "Blue Lock [Digital]": false,
	} {
		if got := isConvertedName(name); got != want {
			t.Errorf("isConvertedName(%q) = %v, want %v", name, got, want)
		}
	}
	e := newEnv(t, nil)
	dir := filepath.Join(e.library, "Blue Lock [kindle-pw3]")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(filepath.Join(dir, "0001.cbz"))
	zw := zip.NewWriter(f)
	w, _ := zw.Create("00000.png")
	_ = png.Encode(w, bannerPage(1, ""))
	_ = zw.Close()
	f.Close() //nolint:errcheck
	if rec := e.do("POST", "/api/library/convert", map[string]any{"series": "Blue Lock [kindle-pw3]", "device": "kindle-pw3"}); rec.Code != 400 {
		t.Errorf("converting a converted copy: status %d, want 400", rec.Code)
	}
}

func TestAPageMarkedAsAnAdvertisementIsFoundAndRemoved(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.library, "One"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.library, "One", "0001.cbz")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	for i := 0; i < 3; i++ {
		w, _ := zw.Create(fmt.Sprintf("%05d.png", i))
		seed := i + 1
		if i == 0 {
			seed = 555
		}
		if err := png.Encode(w, bannerPage(seed, "")); err != nil {
			t.Fatal(err)
		}
	}
	_ = zw.Close()
	f.Close() //nolint:errcheck
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)

	if res := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "One"})); res.Pages != 0 {
		t.Fatalf("a single occurrence cannot be found by itself: %+v", res)
	}
	res := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "One", "markFile": "0001.cbz", "markPage": 1, "apply": true}))
	if res.Dropped != 1 {
		t.Fatalf("marked page: %+v", res)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close() //nolint:errcheck
	if len(zr.File) != 2 {
		t.Errorf("%d pictures left, want 2", len(zr.File))
	}
}

func TestLongLibraryOperationsReportProgress(t *testing.T) {
	e := newEnv(t, nil)
	dir := filepath.Join(e.library, "Prog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"0001.cbz", "0002.cbz"} {
		f, _ := os.Create(filepath.Join(dir, n))
		zw := zip.NewWriter(f)
		w, _ := zw.Create("00000.png")
		_ = png.Encode(w, bannerPage(1, ""))
		_ = zw.Close()
		f.Close() //nolint:errcheck
	}
	rec := e.do("POST", "/api/library/convert", map[string]any{"series": "Prog", "device": "kindle-pw3", "progressId": "abc123"})
	if rec.Code != 200 {
		t.Fatalf("convert: %d %s", rec.Code, rec.Body)
	}
	got := decode[progressState](t, e.do("GET", "/api/progress/abc123", nil))
	if got.Total != 2 || got.Done != 2 {
		t.Errorf("progress = %+v, want 2 of 2", got)
	}
	if none := decode[progressState](t, e.do("GET", "/api/progress/unknown", nil)); none.Total != 0 {
		t.Errorf("unknown id: %+v", none)
	}
}

func TestACoverRepeatedInEveryVolumeIsNotTakenForAnAdvertisement(t *testing.T) {
	e := newEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(e.library, "Covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	for c := 0; c < 3; c++ {
		path := filepath.Join(e.library, "Covers", fmt.Sprintf("%04d.cbz", c+1))
		f, _ := os.Create(path)
		zw := zip.NewWriter(f)
		for i := 0; i < 4; i++ {
			seed := c*10 + i + 1
			if i == 0 {
				seed = 888 // the same cover in every file
			}
			w, _ := zw.Create(fmt.Sprintf("%05d.png", i))
			if err := png.Encode(w, bannerPage(seed, "")); err != nil {
				t.Fatal(err)
			}
		}
		w, _ := zw.Create("ComicInfo.xml")
		_, _ = w.Write([]byte(`<ComicInfo><Pages><Page Image="0" Type="FrontCover"/></Pages></ComicInfo>`))
		_ = zw.Close()
		f.Close() //nolint:errcheck
		old := time.Now().Add(-time.Hour)
		_ = os.Chtimes(path, old, old)
	}
	res := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "Covers"}))
	if res.Pages != 0 || res.Dropped != 0 {
		t.Fatalf("the cover was taken for an advertisement: %+v", res)
	}
}

// makeCBZ writes a CBZ of n pages (page i is bannerPage(seeds[i])) and makes it look old.
func makeCBZ(t *testing.T, path string, seeds ...int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for i, seed := range seeds {
		w, _ := zw.Create(fmt.Sprintf("%05d.png", i))
		if err := png.Encode(w, bannerPage(seed, "")); err != nil {
			t.Fatal(err)
		}
	}
	_ = zw.Close()
	f.Close() //nolint:errcheck
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
}

func TestManageLibrary(t *testing.T) {
	e := newEnv(t, nil)
	a := filepath.Join(e.library, "Demo", "0001.cbz")
	makeCBZ(t, a, 1, 2, 3, 4)
	makeCBZ(t, filepath.Join(e.library, "Demo", "0002.cbz"), 5, 6)

	// Pages: count and a thumbnail.
	pages := decode[map[string]int](t, e.do("GET", "/api/library/Demo/0001.cbz/pages", nil))
	if pages["count"] != 4 {
		t.Fatalf("pages = %v", pages)
	}
	if rec := e.do("GET", "/api/library/Demo/0001.cbz/page/2?w=100", nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("page thumbnail: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := e.do("GET", "/api/library/Demo/0001.cbz/page/9", nil); rec.Code != 404 {
		t.Errorf("page 9: %d", rec.Code)
	}

	// Removing pages backs the file up first and renumbers.
	res := decode[removePagesResult](t, e.do("POST", "/api/library/pages/remove", map[string]any{"series": "Demo", "file": "0001.cbz", "pages": []int{2, 3}}))
	if res.Removed != 2 {
		t.Fatalf("removed %+v", res)
	}
	if n, _ := pageCount(a); n != 2 {
		t.Errorf("%d pages left, want 2", n)
	}
	if _, err := os.Stat(filepath.Join(e.library, backupDir, "Demo", "0001.cbz")); err != nil {
		t.Errorf("no backup: %v", err)
	}
	if rec := e.do("POST", "/api/library/pages/remove", map[string]any{"series": "Demo", "file": "0001.cbz", "pages": []int{1, 2}}); rec.Code != 400 {
		t.Errorf("removing every page: %d, want 400", rec.Code)
	}
	list := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
	if len(list) != 1 || !list[0].Files[0].HasBackup || list[0].Files[1].HasBackup {
		t.Errorf("library = %+v (and .backup must not show as a series)", list)
	}

	// Restoring brings the 4 pages back.
	if rec := e.do("POST", "/api/library/backup", map[string]any{"series": "Demo", "action": "restore"}); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if n, _ := pageCount(a); n != 4 {
		t.Errorf("%d pages after restoring, want 4", n)
	}

	// Rename the series and a file.
	if rec := e.do("POST", "/api/library/rename", map[string]any{"series": "Demo", "name": "Demo 2"}); rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/api/library/rename", map[string]any{"series": "Demo 2", "file": "0002.cbz", "name": "Vol 2"}); rec.Code != 200 {
		t.Fatalf("rename file: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(e.library, "Demo 2", "Vol 2.cbz")); err != nil {
		t.Errorf("file not renamed: %v", err)
	}
	if rec := e.do("POST", "/api/library/rename", map[string]any{"series": "Demo 2", "file": "0001.cbz", "name": "../x"}); rec.Code != 400 {
		t.Errorf("bad file name: %d", rec.Code)
	}

	// Export to a folder.
	dest := t.TempDir()
	out := decode[map[string]any](t, e.do("POST", "/api/library/export", map[string]any{"series": "Demo 2", "dest": dest}))
	if out["done"].(float64) != 2 {
		t.Errorf("export = %v", out)
	}
	if _, err := os.Stat(filepath.Join(dest, "Demo 2", "Vol 2.cbz")); err != nil {
		t.Errorf("not exported: %v", err)
	}
	if rec := e.do("POST", "/api/library/export", map[string]any{"series": "Demo 2", "dest": "relative/dir"}); rec.Code != 400 {
		t.Errorf("relative dest: %d", rec.Code)
	}

	// Delete one file, then the series.
	if rec := e.do("POST", "/api/library/delete", map[string]any{"series": "Demo 2", "file": "Vol 2.cbz"}); rec.Code != 200 {
		t.Fatalf("delete file: %d", rec.Code)
	}
	del := decode[manageResult](t, e.do("POST", "/api/library/delete", map[string]any{"series": "Demo 2"}))
	if del.Done != 1 {
		t.Errorf("delete series = %+v", del)
	}
	if _, err := os.Stat(filepath.Join(e.library, "Demo 2")); !os.IsNotExist(err) {
		t.Errorf("the empty folder is still there: %v", err)
	}
}

func TestCuttingAdvertisementsKeepsABackup(t *testing.T) {
	e := newEnv(t, nil)
	for c := 0; c < 3; c++ {
		makeCBZ(t, filepath.Join(e.library, "Ads", fmt.Sprintf("%04d.cbz", c+1)), c*10+1, c*10+2, c*10+3, 777)
	}
	res := decode[adsResult](t, e.do("POST", "/api/library/ads", map[string]any{"series": "Ads", "apply": true}))
	if res.Dropped != 3 {
		t.Fatalf("%+v", res)
	}
	list := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
	for _, f := range list[0].Files {
		if !f.HasBackup {
			t.Errorf("%s has no backup", f.Name)
		}
	}
	if rec := e.do("POST", "/api/library/backup", map[string]any{"series": "Ads", "action": "restore"}); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if n, _ := pageCount(filepath.Join(e.library, "Ads", "0001.cbz")); n != 4 {
		t.Errorf("restored file has %d pages, want 4", n)
	}
}

func TestMakeAZW3FromTheCBZFilesOfASeries(t *testing.T) {
	e := newEnv(t, nil)
	makeCBZ(t, filepath.Join(e.library, "Mobi", "0001.cbz"), 1, 2, 3)
	res := decode[convertResult](t, e.do("POST", "/api/library/mobi", map[string]any{"series": "Mobi"}))
	if res.Converted != 1 || res.Failed != 0 {
		t.Fatalf("%+v", res)
	}
	data, err := os.ReadFile(filepath.Join(e.library, "Mobi", "0001.azw3"))
	if err != nil || len(data) < 100 || string(data[60:68]) != "BOOKMOBI" {
		t.Fatalf("not an AZW3: %v (%d bytes)", err, len(data))
	}
	list := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
	if len(list[0].Files) != 2 {
		t.Errorf("files = %+v", list[0].Files)
	}
}

func TestLibraryRemembersWhereASeriesCameFrom(t *testing.T) {
	var lib string
	e := newEnv(t, func(c *Config) {
		lib = c.LibraryDir
		c.Runner = func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error) {
			makeCBZ(t, filepath.Join(lib, "Origin", "0001.cbz"), 1, 2)
			return &job.Result{Title: "Origin", Dir: filepath.Join(lib, "Origin")}, nil
		}
	})
	rec := e.do("POST", "/api/downloads", map[string]any{"id": "9b3c2f00-0000-4000-8000-000000000001", "chapterIds": []string{"x"}})
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
		if len(list) == 1 && list[0].Source == "mangadex" && list[0].SourceID != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the library did not learn where the series came from")
}

// multiSite is a small website with a search and one series page whose
// chapters are the given numbers.
func multiSite(t *testing.T, title string, chapters ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/list" || r.URL.Path == "/search":
			_, _ = fmt.Fprintf(w, `<div class="card"><a href="/s/1">%s</a></div><div class="card"><a href="/s/2">Unrelated</a></div>`, title)
		case r.URL.Path == "/s/1":
			for _, n := range chapters {
				_, _ = fmt.Fprintf(w, `<div class="c"><a href="/c/%s">Chapter %s</a></div>`, n, n)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestSearchSeveralSourcesAtOnceAndMergeTheirChapters(t *testing.T) {
	e := newEnv(t, nil)
	sites := map[string]*httptest.Server{
		"alpha": multiSite(t, "Blue Lock", "5", "3", "2", "1"),
		"beta":  multiSite(t, "BLUE LOCK (Tiếng Việt)", "6", "5", "4"),
		"gamma": multiSite(t, "Something Else", "1"),
	}
	for name, site := range sites {
		cfg, _ := json.Marshal(map[string]any{
			"base_url": site.URL, "chapter_list_selector": ".c a", "image_list_selector": "img",
			"list_url": "/list", "series_selector": ".card a", "search_url": "/search?q={q}",
		})
		if rec := e.do("POST", "/api/sources", map[string]any{"name": name, "config": json.RawMessage(cfg)}); rec.Code != 200 {
			t.Fatalf("save %s: %d %s", name, rec.Code, rec.Body)
		}
	}
	e.saveSource("plain") // has no search

	res := decode[multiSearchResult](t, e.do("GET", "/api/multi/search?q=blue+lock", nil))
	status := map[string]string{}
	for _, s := range res.Sources {
		status[s.Source] = s.Status
	}
	if status["alpha"] != "ok" || status["beta"] != "ok" || status["plain"] != "skipped" {
		t.Errorf("statuses = %v", status)
	}
	if len(res.Groups) == 0 || res.Groups[0].Sources != 2 {
		t.Fatalf("groups = %+v, want the first to be found by alpha and beta", res.Groups)
	}
	var members []map[string]string
	for _, m := range res.Groups[0].Members {
		members = append(members, map[string]string{"source": m.Source, "id": m.ID, "title": m.Title})
	}
	if rec := e.do("GET", "/api/multi/search?q=", nil); rec.Code != 400 {
		t.Errorf("empty query: %d", rec.Code)
	}

	out := decode[multiChaptersResult](t, e.do("POST", "/api/multi/chapters", map[string]any{"members": members, "lang": "en"}))
	if out.Table.First != "1" || out.Table.Last != "6" {
		t.Fatalf("table = %+v", out.Table)
	}
	if want := []string{"4"}; len(out.Table.Gaps) != 0 {
		t.Errorf("gaps = %v: beta has 4, so only %v could be missing", out.Table.Gaps, want)
	}
	stats := map[string]multi.SourceStat{}
	for _, s := range out.Table.Stats {
		stats[s.Source] = s
	}
	if len(stats["alpha"].Missing) != 1 || stats["alpha"].Missing[0] != "4" || len(stats["beta"].Missing) != 1 || stats["beta"].Missing[0] != "1-3" {
		t.Errorf("stats = %+v", stats)
	}
	// A source that cannot be reached is reported but does not break the rest.
	members = append(members, map[string]string{"source": "alpha", "id": "http://127.0.0.1:1/x", "title": "x"})
	bad := decode[multiChaptersResult](t, e.do("POST", "/api/multi/chapters", map[string]any{"members": members, "lang": "en"}))
	errs := 0
	for _, m := range bad.Members {
		if m.Error != "" {
			errs++
		}
	}
	if errs != 1 || len(bad.Table.Rows) != 6 {
		t.Errorf("errors = %d, rows = %d", errs, len(bad.Table.Rows))
	}
}

// pageSite is a website whose series page lists the given chapters, each with
// one picture of its own colour (so that the page comes from this site).
func pageSite(t *testing.T, shade uint8, chapters ...string) *httptest.Server {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 40, 60))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, img); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/series":
			for _, n := range chapters {
				_, _ = fmt.Fprintf(w, `<div class="c"><a href="/c/%s">Chapter %s</a></div>`, n, n)
			}
		case strings.HasPrefix(r.URL.Path, "/c/"):
			_, _ = w.Write([]byte(`<div id="r"><img src="/i/a.png"></div>`))
		case strings.HasPrefix(r.URL.Path, "/i/"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestDownloadASeriesFromSeveralSourcesAtOnce(t *testing.T) {
	e := newEnv(t, nil)
	a := pageSite(t, 50, "1", "2", "4")
	b := pageSite(t, 200, "2", "3", "4", "5")
	for name, site := range map[string]*httptest.Server{"one": a, "two": b} {
		cfg, _ := json.Marshal(map[string]any{"base_url": site.URL, "chapter_list_selector": ".c a", "image_list_selector": "#r img"})
		if rec := e.do("POST", "/api/sources", map[string]any{"name": name, "config": json.RawMessage(cfg)}); rec.Code != 200 {
			t.Fatalf("save %s: %d %s", name, rec.Code, rec.Body)
		}
	}
	body := map[string]any{
		"lang": "en", "format": "cbz",
		"multi": map[string]any{
			"title": "Joined", "chaptersPerVolume": 2,
			"members": []map[string]string{{"source": "one", "id": a.URL + "/series"}, {"source": "two", "id": b.URL + "/series"}},
			"choice":  map[string]int{"4": 1},
		},
		"chapters": "1-4",
	}
	rec := e.do("POST", "/api/downloads", body)
	if rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	done := waitFor(t, e, decode[JobInfo](t, rec).ID, "done", "failed")
	if done.State != "done" || done.Source != "multi" || done.Title != "Joined" {
		t.Fatalf("job = %+v", done)
	}
	lib := decode[[]LibrarySeries](t, e.do("GET", "/api/library", nil))
	if len(lib) != 1 || lib[0].Name != "Joined" || len(lib[0].Files) != 2 {
		t.Fatalf("library = %+v, want chapters 1-4 in two volumes of two chapters", lib)
	}
	if lib[0].Source != "multi" || lib[0].SourceID != "Joined" {
		t.Errorf("origin = %q %q", lib[0].Source, lib[0].SourceID)
	}
	// Chapter 4 was chosen from "two" (light), chapter 1 only exists on "one" (dark).
	shade := func(file string, page int) uint8 {
		img, err := readPage(filepath.Join(e.library, "Joined", file), page)
		if err != nil {
			t.Fatal(err)
		}
		g, _, _, _ := img.At(5, 5).RGBA()
		return uint8(g >> 8)
	}
	var first, second string
	for _, f := range lib[0].Files {
		if first == "" {
			first = f.Name
		} else {
			second = f.Name
		}
	}
	if shade(first, 0) > 100 || shade(second, 1) < 150 {
		t.Errorf("pages come from the wrong sources: %d %d", shade(first, 0), shade(second, 1))
	}

	for name, bad := range map[string]map[string]any{
		"unknown source": {"multi": map[string]any{"title": "x", "members": []map[string]string{{"source": "nope", "id": "http://x/y"}}}},
		"bad choice":     {"multi": map[string]any{"title": "x", "members": []map[string]string{{"source": "one", "id": a.URL + "/series"}}, "choice": map[string]int{"1": 5}}},
		"bad range":      {"multi": map[string]any{"title": "x", "members": []map[string]string{{"source": "one", "id": a.URL + "/series"}}}, "chapters": "1;drop"},
	} {
		if rec := e.do("POST", "/api/downloads", bad); rec.Code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
}
