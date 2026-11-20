package selector

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// testdata/bookwalker_series.html holds three volume cards copied from a real
// BookWalker series page, so the markup is the real one.
func TestVolumeCoversFromARealSeriesPage(t *testing.T) {
	data, err := os.ReadFile("testdata/bookwalker_series.html")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := url.Parse("https://bookwalker.com/series/2KMCF5H8GFGG/undead-unluck")
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	covers := volumeCoversFromDoc(page, doc)
	if len(covers) != 3 {
		t.Fatalf("got %d covers, want 3 (one per volume card): %+v", len(covers), covers)
	}
	for i, want := range []string{"1", "2", "3"} {
		if covers[i].Volume != want {
			t.Errorf("cover %d is volume %q, want %q", i, covers[i].Volume, want)
		}
	}
	if want := "https://img.sos-dan.net/720/01K/G/B/GNV852H7KQV40QCFM18MC.jpg"; covers[0].URL != want {
		t.Errorf("volume 1 cover = %q, want the largest jpg %q", covers[0].URL, want)
	}
	for _, c := range covers {
		if !strings.HasPrefix(c.URL, "https://img.sos-dan.net/") || !strings.HasSuffix(c.URL, ".jpg") {
			t.Errorf("cover %+v is not a CDN jpg (a local or placeholder address was used)", c)
		}
	}
}

func TestFindVolumeCoversAndDownload(t *testing.T) {
	img := new(bytes.Buffer)
	if err := png.Encode(img, image.NewGray(image.Rect(0, 0, 6, 9))); err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/robots.txt":
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /private\n"))
		case r.URL.Path == "/series/x":
			var b strings.Builder
			b.WriteString(`<html><body>`)
			for i := 1; i <= 3; i++ {
				fmt.Fprintf(&b, `<div class="card"><a href="/volume/ID%d/series-vol-%d"><img srcset="/c/%d-small.png 80w, /c/%d-big.png 480w" src="./local.png"></a><a href="/volume/ID%d/series-vol-%d">Vol %d</a></div>`, i, i, i, i, i, i, i)
			}
			b.WriteString(`</body></html>`)
			_, _ = w.Write([]byte(b.String()))
		case r.URL.Path == "/private/x":
			_, _ = w.Write([]byte(`<html></html>`))
		case r.URL.Path == "/empty":
			_, _ = w.Write([]byte(`<html><body><a href="/about">About</a><img src="/x.png"></body></html>`))
		case strings.HasSuffix(r.URL.Path, "-big.png"):
			_, _ = w.Write(img.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	covers, err := FindVolumeCovers(context.Background(), srv.URL+"/series/x", nil)
	if err != nil || len(covers) != 3 {
		t.Fatalf("covers = %+v, %v", covers, err)
	}
	if covers[1].Volume != "2" || covers[1].URL != srv.URL+"/c/2-big.png" {
		t.Errorf("volume 2 = %+v, want the widest srcset entry", covers[1])
	}

	images := DownloadVolumeCovers(context.Background(), covers, nil)
	if len(images) != 3 || images["1"] == nil {
		t.Errorf("downloaded %d covers, want 3", len(images))
	}
	broken := DownloadVolumeCovers(context.Background(), []VolumeCover{{Volume: "9", URL: srv.URL + "/nothing.png"}}, nil)
	if len(broken) != 0 {
		t.Errorf("a missing image must be left out, got %d", len(broken))
	}

	if _, err := FindVolumeCovers(context.Background(), srv.URL+"/private/x", nil); err == nil || !strings.Contains(err.Error(), "robots.txt") {
		t.Errorf("robots.txt must be obeyed, got %v", err)
	}
	if _, err := FindVolumeCovers(context.Background(), srv.URL+"/empty", nil); err == nil || !strings.Contains(err.Error(), "no volume covers") {
		t.Errorf("a page without volumes must be explained, got %v", err)
	}
	if _, err := FindVolumeCovers(context.Background(), "file:///etc/passwd", nil); err == nil {
		t.Error("only http(s) addresses are accepted")
	}
}

// A one-volume series: the card's link has no "vol-N" in it, only the label
// "VOL 1" beside it, and the volume's own page leads to the series page.
func TestVolumeCoversOfAOneVolumeSeriesAndFromAVolumePage(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/series/ABC/home-office-romance":
			_, _ = w.Write([]byte(`<html><body><p>1 Volume</p><div class="card"><a href="/volume/XYZ/home-office-romance"><picture><img srcset="/c/small.jpg 80w, /c/large.jpg 480w"></picture></a>` +
				`<span>VOL</span> <span>1</span><span>$</span><span>10.99</span><a href="/volume/XYZ/home-office-romance">Home Office Romance</a></div>` +
				`<div class="more"><a href="/volume/OTHER/another-book"><img src="/c/other.jpg"></a></div></body></html>`))
		case "/volume/XYZ/home-office-romance":
			_, _ = w.Write([]byte(`<html><body><img src="/c/large.jpg"><a href="/series/ABC/home-office-romance">Series</a></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	want := srv.URL + "/c/large.jpg"
	for _, address := range []string{"/series/ABC/home-office-romance", "/volume/XYZ/home-office-romance"} {
		covers, err := FindVolumeCovers(context.Background(), srv.URL+address, nil)
		if err != nil || len(covers) != 1 || covers[0].Volume != "1" || covers[0].URL != want {
			t.Errorf("%s: covers = %+v, %v; want volume 1 with %s", address, covers, err, want)
		}
	}
}

// A shop that hides the covers of adult titles shows one placeholder picture
// for every volume; that is not a cover and must not be used as one.
func TestVolumeCoversIgnoreAPlaceholderSharedByAllVolumes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(&b, `<div><a href="/volume/V%d/title-vol-%d"><img src="/static/mature.png"></a></div>`, i, i)
		}
		// ...except volume 6, which has its own picture.
		b.WriteString(`<div><a href="/volume/V6/title-vol-6"><img src="/c/6.jpg"></a></div>`)
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)

	covers, err := FindVolumeCovers(context.Background(), srv.URL+"/series/x", nil)
	if err != nil || len(covers) != 1 || covers[0].Volume != "6" {
		t.Errorf("covers = %+v, %v; want only volume 6, whose picture is its own", covers, err)
	}

	all := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(&b, `<div><a href="/volume/V%d/title-vol-%d"><img src="/static/mature.png"></a></div>`, i, i)
		}
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(all.Close)
	if _, err := FindVolumeCovers(context.Background(), all.URL+"/series/x", nil); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("err = %v, want an explanation that the covers are hidden", err)
	}
}
