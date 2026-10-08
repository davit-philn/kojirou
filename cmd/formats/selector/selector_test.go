package selector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const chapterPage = `<html><body><ul class="chapters">
  <li><a href="/read/1">Chapter 1</a></li>
  <li><a href="/read/2"> Chapter 2 </a></li>
  <li><span>no link</span></li>
  <li><a href="javascript:void(0)">bad</a></li>
</ul></body></html>`

const readerPage = `<html><body><div id="reader">
  <img data-original="/img/1.jpg" src="/lazy.gif">
  <img data-original="https://cdn.example.org/2.jpg">
  <img src="/no-attr.jpg">
  <img data-original="data:image/png;base64,AAAA">
</div></body></html>`

func newServer(t *testing.T) (*httptest.Server, *http.Request) {
	t.Helper()
	var last http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = *r.Clone(context.Background())
		switch r.URL.Path {
		case "/manga":
			_, _ = w.Write([]byte(chapterPage))
		case "/read/1":
			_, _ = w.Write([]byte(readerPage))
		case "/redirect":
			http.Redirect(w, r, "/sub/dir/read", http.StatusFound)
		case "/sub/dir/read":
			_, _ = w.Write([]byte(`<img class="p" src="page.jpg">`))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &last
}

func TestFetchChapterList(t *testing.T) {
	srv, _ := newServer(t)
	cfg := SourceConfig{BaseURL: srv.URL, ChapterListSelector: ".chapters li"}

	got, err := FetchChapterList(context.Background(), "/manga", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []Chapter{
		{Title: "Chapter 1", URL: srv.URL + "/read/1"},
		{Title: "Chapter 2", URL: srv.URL + "/read/2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFetchChapterListSelectsAnchorsDirectly(t *testing.T) {
	srv, _ := newServer(t)
	cfg := SourceConfig{ChapterListSelector: ".chapters a[href^='/read']"}

	got, err := FetchChapterList(context.Background(), srv.URL+"/manga", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Chapter 1" {
		t.Errorf("unexpected chapters: %+v", got)
	}
}

func TestFetchImages(t *testing.T) {
	srv, _ := newServer(t)
	cfg := SourceConfig{
		BaseURL:           srv.URL,
		ImageListSelector: "#reader img",
		ImageAttr:         "data-original",
	}

	got, err := FetchImages(context.Background(), "/read/1", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{srv.URL + "/img/1.jpg", "https://cdn.example.org/2.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFetchImagesDefaultsToSrc(t *testing.T) {
	srv, _ := newServer(t)
	cfg := SourceConfig{ImageListSelector: "#reader img[src^='/no']"}

	got, err := FetchImages(context.Background(), srv.URL+"/read/1", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{srv.URL + "/no-attr.jpg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRelativeLinksResolveAgainstRedirectTarget(t *testing.T) {
	srv, _ := newServer(t)
	cfg := SourceConfig{BaseURL: srv.URL, ImageListSelector: "img.p"}

	got, err := FetchImages(context.Background(), "/redirect", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{srv.URL + "/sub/dir/page.jpg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHeadersAreSent(t *testing.T) {
	srv, last := newServer(t)
	cfg := SourceConfig{
		BaseURL:             srv.URL,
		UserAgent:           "test-agent/1.0",
		Referer:             "https://example.org/",
		ChapterListSelector: ".chapters li",
	}

	if _, err := FetchChapterList(context.Background(), "/manga", cfg); err != nil {
		t.Fatal(err)
	}
	if got := last.Header.Get("User-Agent"); got != "test-agent/1.0" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := last.Header.Get("Referer"); got != "https://example.org/" {
		t.Errorf("Referer = %q", got)
	}
}

func TestContextCancellation(t *testing.T) {
	srv, _ := newServer(t)
	cfg := SourceConfig{ChapterListSelector: "a"}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := FetchChapterList(ctx, srv.URL+"/slow", cfg)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("cancellation did not interrupt the request")
	}
}

func TestTimeout(t *testing.T) {
	srv, _ := newServer(t)
	cfg := SourceConfig{ChapterListSelector: "a", TimeoutSeconds: 1}

	start := time.Now()
	if _, err := FetchChapterList(context.Background(), srv.URL+"/slow", cfg); err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("client timeout was not applied")
	}
}

func TestErrors(t *testing.T) {
	srv, _ := newServer(t)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
		want string
	}{
		{"empty selector", func() error {
			_, err := FetchChapterList(ctx, srv.URL+"/manga", SourceConfig{})
			return err
		}, "must not be empty"},
		{"invalid selector", func() error {
			_, err := FetchImages(ctx, srv.URL+"/read/1", SourceConfig{ImageListSelector: "div["})
			return err
		}, "invalid image_list_selector"},
		{"status", func() error {
			_, err := FetchChapterList(ctx, srv.URL+"/missing", SourceConfig{ChapterListSelector: "a"})
			return err
		}, "404"},
		{"no match", func() error {
			_, err := FetchChapterList(ctx, srv.URL+"/manga", SourceConfig{ChapterListSelector: ".nothing"})
			return err
		}, "no chapters matched"},
		{"relative url without base", func() error {
			_, err := FetchImages(ctx, "/read/1", SourceConfig{ImageListSelector: "img"})
			return err
		}, "absolute http(s)"},
		{"non-http scheme", func() error {
			_, err := FetchImages(ctx, "file:///etc/passwd", SourceConfig{ImageListSelector: "img"})
			return err
		}, "absolute http(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestSourceReusesClient(t *testing.T) {
	srv, _ := newServer(t)
	s := New(SourceConfig{BaseURL: srv.URL, ChapterListSelector: ".chapters li"})
	if s.Client() == nil {
		t.Fatal("nil client")
	}
	got, err := s.FetchChapterList(context.Background(), "/manga")
	if err != nil || len(got) != 2 {
		t.Errorf("got %v, %v", got, err)
	}
}
