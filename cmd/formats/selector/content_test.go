package selector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

type (
	mdChapterList = md.ChapterList
	mdChapterInfo = md.ChapterInfo
)

func mdIdent(s string) md.Identifier { return md.NewIdentifier(s) }

type nopProgress struct{}

func (nopProgress) Increase(int) {}
func (nopProgress) Add(int)      {}
func (nopProgress) NewProxyWriter(w io.Writer) io.Writer {
	return w
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func providerFor(srv *httptest.Server, mutate func(*SourceConfig)) *Provider {
	cfg := SourceConfig{
		BaseURL:                srv.URL,
		ChapterListSelector:    ".chapters a",
		ImageListSelector:      "#reader img",
		MaxConcurrentDownloads: 2,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	p := NewProvider(cfg, language.English)
	p.retryWait = time.Millisecond

	return p
}

func TestProviderChapters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// newest first, as most sites list them
		_, _ = w.Write([]byte(`<div class="chapters">
			<a href="/c/3">Chapter 3</a>
			<a href="/c/2.5">Chapter 2.5: Extra</a>
			<a href="/c/1">Prologue</a></div>`))
	}))
	defer srv.Close()

	p := providerFor(srv, func(c *SourceConfig) { c.ChaptersPerVolume = 2 })
	got, err := p.Chapters(context.Background(), srv.URL+"/series/my-manga")
	if err != nil {
		t.Fatal(err)
	}

	type row struct{ ident, vol, id string }
	// Chapters are numbered from the bottom of the list; with two chapters
	// per volume, positions 1-2 form volume 1 and position 3 volume 2.
	// "Prologue" has no number, so its position (1) is used.
	want := []row{
		{"3", "2", srv.URL + "/c/3"},
		{"2.5", "1", srv.URL + "/c/2.5"},
		{"1", "1", srv.URL + "/c/1"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d chapters", len(got))
	}
	for i, w := range want {
		g := got[i].Info
		if g.Identifier.String() != w.ident || g.VolumeIdentifier.String() != w.vol || g.ID != w.id {
			t.Errorf("chapter %d = %v/%v/%v, want %+v", i, g.Identifier, g.VolumeIdentifier, g.ID, w)
		}
	}

	manga, _ := p.Skeleton(context.Background(), srv.URL+"/series/my-manga/")
	if manga.Info.Title != "my-manga" {
		t.Errorf("title = %q", manga.Info.Title)
	}
}

func TestPagesLimitsConcurrency(t *testing.T) {
	var active, peak int32
	img := pngBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/c/1" {
			var html string
			for i := 0; i < 12; i++ {
				html += fmt.Sprintf(`<img src="/i/%d.png">`, i)
			}
			_, _ = w.Write([]byte(`<div id="reader">` + html + `</div>`))
			return
		}
		n := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		_, _ = w.Write(img)
	}))
	defer srv.Close()

	p := providerFor(srv, nil)
	list := chaptersFor(t, srv.URL+"/c/1")

	got, err := p.Pages(context.Background(), list, nopProgress{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Errorf("got %d images, want 12", len(got))
	}
	if peak > 2 {
		t.Errorf("peak concurrency %d exceeds the limit of 2", peak)
	}
	if peak < 2 {
		t.Errorf("peak concurrency %d: downloads did not run in parallel", peak)
	}
}

func TestDownloadRetriesOnTooManyRequests(t *testing.T) {
	var calls int32
	img := pngBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(img)
	}))
	defer srv.Close()

	p := providerFor(srv, nil)
	if _, err := p.download(context.Background(), srv.URL+"/x.png", srv.URL); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDownloadGivesUpAndDoesNotRetryClientErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	p := providerFor(srv, nil)

	if _, err := p.download(context.Background(), srv.URL+"/gone", srv.URL); err == nil || calls != 1 {
		t.Errorf("404: err=%v calls=%d, want one call and an error", err, calls)
	}
	atomic.StoreInt32(&calls, 0)
	if _, err := p.download(context.Background(), srv.URL+"/busy", srv.URL); err == nil || calls != maxAttempts {
		t.Errorf("503: err=%v calls=%d, want %d calls and an error", err, calls, maxAttempts)
	}
}

func TestPagesCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/c/1" {
			_, _ = w.Write([]byte(`<div id="reader"><img src="/a.png"><img src="/b.png"></div>`))
			return
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := providerFor(srv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := p.Pages(ctx, chaptersFor(t, srv.URL+"/c/1"), nopProgress{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("cancellation did not stop the downloads")
	}
}

func chaptersFor(t *testing.T, chapterURL string) mdChapterList {
	t.Helper()
	return mdChapterList{{Info: mdChapterInfo{
		ID:               chapterURL,
		Identifier:       mdIdent("1"),
		VolumeIdentifier: mdIdent("1"),
	}}}
}
