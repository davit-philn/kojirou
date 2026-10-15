package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
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
	want := map[string]string{"2": "1", "3": "1", "5": "Special"}
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
