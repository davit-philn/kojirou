package selector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type site struct {
	srv   *httptest.Server
	pages map[string]string
	hits  int32
}

func newSite(t *testing.T, pages map[string]string) *site {
	t.Helper()
	s := &site{pages: pages}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := s.pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/robots.txt" {
			atomic.AddInt32(&s.hits, 1)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)

	return s
}

func detect(t *testing.T, startURL string) (*Detection, error) {
	t.Helper()
	return Detect(context.Background(), startURL, DetectOptions{Delay: time.Millisecond})
}

// verify checks that the detected configuration really works through the
// normal fetch functions, which is what matters to a user.
func verify(t *testing.T, d *Detection, chapters, images int) {
	t.Helper()
	cfg := d.Config
	got, err := FetchChapterList(context.Background(), d.SeriesURL, cfg)
	if err != nil || len(got) != chapters {
		t.Errorf("chapter list: %d chapters, %v (want %d), selector %q", len(got), err, chapters, cfg.ChapterListSelector)
	}
	imgs, err := FetchImages(context.Background(), d.ChapterURL, cfg)
	if err != nil || len(imgs) != images {
		t.Errorf("images: %d, %v (want %d), selector %q attr %q", len(imgs), err, images, cfg.ImageListSelector, cfg.ImageAttr)
	}
}

func cardHome(series int, chapterPrefix string) string {
	var b strings.Builder
	b.WriteString(`<html><head><title>Home</title></head><body><a href="/">Home</a><a href="/about">About</a><ul class="list_grid">`)
	for i := 1; i <= series; i++ {
		fmt.Fprintf(&b, `<li><div class="book_avatar"><a href="/truyen-tranh/series-%d-1%d"><img src="/t/%d.jpg"></a></div>`+
			`<div class="book_info"><h3><a href="/truyen-tranh/series-%d-1%d">Series %d</a></h3>`+
			`<div class="last_chapter"><a href="/truyen-tranh/series-%d-1%d-chap-%d">%s %d</a></div></div></li>`,
			i, i, i, i, i, i, i, i, 30+i, chapterPrefix, 30+i)
	}
	b.WriteString(`</ul></body></html>`)

	return b.String()
}

func divChapterList(n int, titlePrefix string) string {
	var b strings.Builder
	b.WriteString(`<html><head><title>Series</title></head><body><h1>Series One</h1><div class="works-chapter-list">`)
	for i := n; i >= 1; i-- {
		fmt.Fprintf(&b, `<div class="works-chapter-item"><div class="name-chap"><a href="/truyen-tranh/series-1-11-chap-%d">%s %d</a></div><div class="time-chap">01/01/2026</div></div>`, i, titlePrefix, i)
	}
	b.WriteString(`</div><div class="latest"><a href="/truyen-tranh/other-5-chap-9">Chương 9</a><a href="/truyen-tranh/other-5-chap-8">Chương 8</a><a href="/truyen-tranh/other-5-chap-7">Chương 7</a></div></body></html>`)

	return b.String()
}

const srcChapterPage = `<html><body><img class="logo" src="/logo.png"><div class="story-see-content">
<img class="lazy" src="https://cdn.example.org/a/p1.jpg" alt="Trang 1"><img class="lazy" src="https://cdn.example.org/a/p2.jpg" alt="Trang 2">
<img class="lazy" src="https://cdn.example.org/a/p3.jpg" alt="Trang 3"><img class="lazy" src="https://cdn.example.org/a/p4.jpg" alt="Trang 4">
</div><img class="banner" src="/ads/b.jpg"></body></html>`

func TestDetectCardLayoutFromHomePage(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":                                cardHome(6, "Chương"),
		"/truyen-tranh/series-1-11":        divChapterList(12, "Chương"),
		"/truyen-tranh/series-2-12":        divChapterList(12, "Chương"),
		"/truyen-tranh/series-3-13":        divChapterList(12, "Chương"),
		"/truyen-tranh/series-1-11-chap-6": srcChapterPage,
	})
	// the sample chapter is taken from the middle of the list
	for i := 1; i <= 12; i++ {
		s.pages[fmt.Sprintf("/truyen-tranh/series-1-11-chap-%d", i)] = srcChapterPage
	}

	d, err := detect(t, s.srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	scanned := atomic.LoadInt32(&s.hits) // before verify opens pages itself
	verify(t, d, 12, 4)
	if d.Config.ImageAttr != "src" || d.Config.BaseURL != s.srv.URL || d.Config.Referer != s.srv.URL+"/" {
		t.Errorf("config = %+v", d.Config)
	}
	if d.ChapterCount != 12 || d.ImageCount != 4 {
		t.Errorf("counts = %d, %d", d.ChapterCount, d.ImageCount)
	}
	if scanned != 3 {
		t.Errorf("scanning opened %d pages, want 3 (home page, one series, one chapter)", scanned)
	}
	if !strings.Contains(strings.Join(d.Notes, " "), "volume") {
		t.Errorf("notes should mention volumes: %v", d.Notes)
	}
}

func TestDetectLazyImagesAndListLayout(t *testing.T) {
	var list strings.Builder
	list.WriteString(`<html><body><ul class="chapters">`)
	for i := 8; i >= 1; i-- {
		fmt.Fprintf(&list, `<li class="row"><a href="/read/title/%d">Chapter %d</a></li>`, i, i)
	}
	list.WriteString(`</ul></body></html>`)
	lazy := `<html><body><div id="reader">` +
		`<img src="data:image/gif;base64,R0lGOD" data-original="/img/1.webp"><img src="data:image/gif;base64,R0lGOD" data-original="/img/2.webp">` +
		`<img src="data:image/gif;base64,R0lGOD" data-original="/img/3.webp"></div></body></html>`
	home := `<html><body><div class="grid"><a href="/series/a">A</a><a href="/series/b">B</a><a href="/series/c">C</a><a href="/series/d">D</a></div></body></html>`
	pages := map[string]string{"/": home, "/series/a": list.String(), "/series/b": list.String(), "/series/c": list.String()}
	for i := 1; i <= 8; i++ {
		pages[fmt.Sprintf("/read/title/%d", i)] = lazy
	}
	s := newSite(t, pages)

	d, err := detect(t, s.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	verify(t, d, 8, 3)
	if d.Config.ImageAttr != "data-original" {
		t.Errorf("image_attr = %q, want the lazy attribute only (src is a placeholder)", d.Config.ImageAttr)
	}
}

func TestDetectTableLayoutStartingFromSeriesPage(t *testing.T) {
	var tbl strings.Builder
	tbl.WriteString(`<html><body><table class="list">`)
	for i := 10; i >= 1; i-- {
		fmt.Fprintf(&tbl, `<tr><td class="c"><a href="/r/s-3/ch-%d">Ep. %d</a></td><td>2026</td></tr>`, i, i)
	}
	tbl.WriteString(`</table></body></html>`)
	pages := map[string]string{
		"/manga/s-3": tbl.String(),
	}
	for i := 1; i <= 10; i++ {
		pages[fmt.Sprintf("/r/s-3/ch-%d", i)] = `<html><body><div class="page"><img data-src="/p/1.jpg"></div><div class="page"><img data-src="/p/2.jpg"></div></body></html>`
	}
	s := newSite(t, pages)

	d, err := detect(t, s.srv.URL+"/manga/s-3")
	if err != nil {
		t.Fatal(err)
	}
	verify(t, d, 10, 2)
	if d.SeriesURL != s.srv.URL+"/manga/s-3" {
		t.Errorf("series = %q", d.SeriesURL)
	}
}

func TestDetectFillsVolumeRegexWhenTitlesNameVolumes(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><body><ul class="chs">`)
	for i := 9; i >= 1; i-- {
		fmt.Fprintf(&b, `<li><a href="/c/%d">Tập %d - Chương %d</a></li>`, i, (i-1)/3+1, i)
	}
	b.WriteString(`</ul></body></html>`)
	pages := map[string]string{"/s/x": b.String()}
	for i := 1; i <= 9; i++ {
		pages[fmt.Sprintf("/c/%d", i)] = `<div class="v"><img src="/a/1.png"><img src="/a/2.png"></div>`
	}
	s := newSite(t, pages)

	d, err := detect(t, s.srv.URL+"/s/x")
	if err != nil {
		t.Fatal(err)
	}
	if d.Config.VolumeRegex == "" {
		t.Fatalf("volume_regex not set: %+v", d.Config)
	}
	// the finished configuration must produce volumes through the normal path
	got := volumesOf(t, d.Config, [][2]string{{"Tập 2 - Chương 5", "/c/5"}, {"Tập 1 - Chương 1", "/c/1"}})
	eq(t, got, map[string]string{"5": "2", "1": "1"})
}

func TestDetectRespectsRobots(t *testing.T) {
	pages := map[string]string{
		"/":                         cardHome(4, "Chương"),
		"/truyen-tranh/series-1-11": divChapterList(5, "Chương"),
		"/truyen-tranh/series-2-12": divChapterList(5, "Chương"),
		"/truyen-tranh/series-3-13": divChapterList(5, "Chương"),
		"/robots.txt":               "User-agent: *\nDisallow: /truyen-tranh/\n",
	}
	s := newSite(t, pages)
	_, err := detect(t, s.srv.URL+"/")
	if err == nil || !strings.Contains(err.Error(), "robots.txt") {
		t.Fatalf("expected a robots.txt refusal, got %v", err)
	}
	if hits := atomic.LoadInt32(&s.hits); hits != 1 {
		t.Errorf("only the start page may be opened when robots.txt forbids the rest, got %d pages", hits)
	}

	// an unrelated Disallow does not get in the way
	pages["/robots.txt"] = "User-agent: *\nDisallow: /admin\n"
	for i := 1; i <= 12; i++ {
		pages[fmt.Sprintf("/truyen-tranh/series-1-11-chap-%d", i)] = srcChapterPage
	}
	pages["/truyen-tranh/series-1-11"] = divChapterList(12, "Chương")
	if _, err := detect(t, s.srv.URL+"/"); err != nil {
		t.Errorf("unrelated rules must not block the scan: %v", err)
	}
}

func TestDetectErrorsAreExplained(t *testing.T) {
	tests := []struct {
		name  string
		pages map[string]string
		start string
		want  string
	}{
		{"not a url", nil, "example", "http://"},
		{"nothing to find", map[string]string{"/": `<html><head><title>Plain</title></head><body><a href="/a">a</a></body></html>`}, "/", "no chapter list was found"},
		{"browser check", map[string]string{"/": `<html><head><title>Just a moment...</title></head><body>Checking your browser</body></html>`}, "/", "browser check"},
		{"no images", map[string]string{
			"/s/a": `<ul class="c"><li><a href="/c/1">Chapter 1</a></li><li><a href="/c/2">Chapter 2</a></li><li><a href="/c/3">Chapter 3</a></li></ul>`,
			"/c/2": `<html><body><p>no pictures</p></body></html>`,
		}, "/s/a", "no page images"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := tt.start
			if tt.pages != nil {
				s := newSite(t, tt.pages)
				start = s.srv.URL + tt.start
			}
			_, err := detect(t, start)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestDetectOnlyScansTheSameSite(t *testing.T) {
	home := `<html><body><a href="https://elsewhere.example/series/a">x</a><a href="https://elsewhere.example/series/b">y</a><a href="https://elsewhere.example/series/c">z</a></body></html>`
	s := newSite(t, map[string]string{"/": home})
	_, err := detect(t, s.srv.URL+"/")
	if err == nil || !strings.Contains(err.Error(), "no chapter list") {
		t.Errorf("links to other sites must be ignored, got %v", err)
	}
	if hits := atomic.LoadInt32(&s.hits); hits != 1 {
		t.Errorf("opened %d pages", hits)
	}
}

func TestParseRobots(t *testing.T) {
	text := `# comment
User-agent: googlebot
Disallow: /

User-agent: *
Disallow: /private/
Disallow: /*.json$
Allow: /private/public/

User-agent: kojirou
Disallow: /only-for-us
`
	star := parseRobots(strings.ReplaceAll(text, "User-agent: kojirou\nDisallow: /only-for-us\n", ""), "kojirou")
	for path, want := range map[string]bool{
		"/":                   true,
		"/private/x":          false,
		"/private/public/y":   true, // the longer Allow beats the Disallow
		"/data/list.json":     false,
		"/data/list.json?x=1": true, // $ anchors the end
		"/chapters":           true,
	} {
		if got := star.allowed(path); got != want {
			t.Errorf("* group: allowed(%q) = %v, want %v", path, got, want)
		}
	}

	own := parseRobots(text, "kojirou")
	if own.allowed("/only-for-us/x") || !own.allowed("/private/x") {
		t.Errorf("a group naming the tool replaces the * group")
	}
	if !parseRobots("", "kojirou").allowed("/anything") || !(*robotsRules)(nil).allowed("/x") {
		t.Error("no rules means everything is allowed")
	}
}
