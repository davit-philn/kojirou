package selector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

const (
	maxDetectPages   = 8
	detectUserAgent  = "kojirou"
	detectDelay      = 400 * time.Millisecond
	maxRobotsBytes   = 512 << 10
	minChapterLinks  = 3
	maxSeriesToTry   = 3
	volumeShareToSet = 0.5
)

var (
	chapterWord = regexp.MustCompile(`(?i)(chương|chuong|chapter|chap|chapitre|episode|\bch\b\.?|\bep\b\.?|#)\s*\d`)
	chapterHref = regexp.MustCompile(`(?i)(chap(ter)?|chuong|episode|\bep)[-_/ ]?\d`)
	volumeTitle = regexp.MustCompile(`(?i)(?:tập|tap|vol(?:ume)?\.?)\s*(\d+)`)
	notContent  = regexp.MustCompile(`(?i)logo|avatar|banner|icon|sprite|emoji|advert|\bads?\b`)
	imageExt    = regexp.MustCompile(`(?i)\.(jpe?g|png|webp|gif|avif)(\?|$)`)
	lazyAttrs   = []string{"data-original", "data-src", "data-lazy-src", "data-url", "src"}
)

// DetectOptions tunes Detect; the zero value is fine.
type DetectOptions struct {
	// Client sends the requests; defaults to a client with a 20 second timeout.
	Client *http.Client
	// Delay waits between page requests (default 400ms).
	Delay time.Duration
}

// Detection is what Detect found out about a website.
type Detection struct {
	Config       SourceConfig `json:"config"`
	SeriesURL    string       `json:"seriesUrl"`
	ChapterURL   string       `json:"chapterUrl"`
	ChapterCount int          `json:"chapterCount"`
	ImageCount   int          `json:"imageCount"`
	Notes        []string     `json:"notes"`
}

// Detect works out a source configuration for a website, starting from its
// home page (or from the page of one series). It opens at most a handful of
// pages: the start page, one or more series pages and one chapter page. It
// obeys robots.txt and only reads plain HTML.
func Detect(ctx context.Context, startURL string, opts DetectOptions) (*Detection, error) {
	start, err := url.Parse(strings.TrimSpace(startURL))
	if err != nil || (start.Scheme != "http" && start.Scheme != "https") || start.Host == "" {
		return nil, errors.New("enter the address of the website, starting with http:// or https://")
	}
	client := opts.Client
	if client == nil {
		client = NewClient(SourceConfig{UserAgent: detectUserAgent, TimeoutSeconds: 20})
	}
	delay := opts.Delay
	if delay == 0 {
		delay = detectDelay
	}

	d := &detector{ctx: ctx, client: client, host: start.Host, delay: delay}
	d.robots = d.loadRobots(start)

	startPage, startDoc, err := d.get(start.String())
	if err != nil {
		return nil, err
	}

	series, doc, list, err := d.findChapterList(startPage, startDoc)
	if err != nil {
		return nil, err
	}
	chapterURL := list.sample()
	chapterPage, chapterDoc, err := d.get(chapterURL)
	if err != nil {
		return nil, fmt.Errorf("opening a chapter page: %w", err)
	}
	images, err := d.findImages(chapterPage, chapterDoc)
	if err != nil {
		return nil, err
	}

	origin := (&url.URL{Scheme: start.Scheme, Host: start.Host}).String()
	cfg := SourceConfig{
		BaseURL:                origin,
		Referer:                origin + "/",
		ChapterListSelector:    list.selector,
		ImageListSelector:      images.selector,
		ImageAttr:              images.attrs,
		MaxConcurrentDownloads: defaultConcurrency,
	}
	res := &Detection{
		Config:       cfg,
		SeriesURL:    series.String(),
		ChapterURL:   chapterURL,
		ChapterCount: len(list.chapters),
		ImageCount:   len(images.urls),
	}
	if d.mostTitlesHaveVolume(list.chapters) {
		res.Config.VolumeRegex = volumeTitle.String()
		res.Notes = append(res.Notes, "Chapter titles contain the volume (e.g. “Tập 2”), so volume_regex was filled in.")
	} else {
		res.Notes = append(res.Notes,
			"The chapter titles do not say which volume a chapter belongs to, so all chapters will land in one volume. "+
				"Add volume_ranges (e.g. 1:1-8, 2:9-17) or fill the gaps of a MangaDex series with this source to get real volumes.")
	}
	if len(images.urls) < 2 {
		res.Notes = append(res.Notes, "Only one image was found on the sample chapter; check that it is a full chapter.")
	}
	if title := strings.TrimSpace(doc.Find("h1").First().Text()); title != "" {
		res.Notes = append(res.Notes, fmt.Sprintf("Series page used for the check: %q.", title))
	}

	// The final check: the finished configuration must work through the
	// normal code path, not just inside the detector.
	if _, err := compile("chapter_list_selector", cfg.ChapterListSelector); err != nil {
		return nil, err
	}

	return res, nil
}

type detector struct {
	ctx      context.Context
	client   *http.Client
	host     string
	delay    time.Duration
	robots   *robotsRules
	requests int
}

// loadRobots fetches robots.txt; a missing or unreadable file allows everything.
func (d *detector) loadRobots(start *url.URL) *robotsRules {
	u := (&url.URL{Scheme: start.Scheme, Host: start.Host, Path: "/robots.txt"}).String()
	req, err := http.NewRequestWithContext(d.ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRobotsBytes))
	if err != nil {
		return nil
	}

	return parseRobots(string(data), detectUserAgent)
}

// get fetches a page on the same site, within the page budget and robots.txt.
func (d *detector) get(rawURL string) (*url.URL, *goquery.Document, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host != d.host {
		return nil, nil, fmt.Errorf("refusing to open %q: only pages of the same website are scanned", rawURL)
	}
	if !d.robots.allowed(u.RequestURI()) {
		return nil, nil, fmt.Errorf("the website's robots.txt does not allow automated access to %v, so it was not scanned", u.Path)
	}
	if d.requests >= maxDetectPages {
		return nil, nil, errors.New("scanning stopped after opening several pages without finding what it needs")
	}
	if d.requests > 0 {
		select {
		case <-d.ctx.Done():
			return nil, nil, d.ctx.Err()
		case <-time.After(d.delay):
		}
	}
	d.requests++

	return fetchDocument(d.ctx, d.client, rawURL, SourceConfig{})
}

type chapterList struct {
	selector string
	chapters []Chapter
}

// sample picks a chapter from the middle of the list, which avoids both
// the newest (possibly unfinished) and the first (often a notice) entry.
func (l *chapterList) sample() string {
	return l.chapters[len(l.chapters)/2].URL
}

// findChapterList looks for a page with a list of chapters: the start page if
// it looks like a series page, otherwise a few series linked from it.
func (d *detector) findChapterList(startPage *url.URL, startDoc *goquery.Document) (*url.URL, *goquery.Document, *chapterList, error) {
	if depth(startPage.Path) >= 2 {
		if list, ok := d.chapterListOn(startPage, startDoc); ok {
			return startPage, startDoc, list, nil
		}
	}

	candidates := seriesCandidates(startPage, startDoc)
	for i, link := range candidates {
		if i >= maxSeriesToTry {
			break
		}
		page, doc, err := d.get(link)
		if err != nil {
			if strings.Contains(err.Error(), "robots.txt") {
				return nil, nil, nil, err
			}
			continue
		}
		if list, ok := d.chapterListOn(page, doc); ok {
			return page, doc, list, nil
		}
	}

	return nil, nil, nil, fmt.Errorf("no chapter list was found. %v", describeStart(startDoc))
}

func describeStart(doc *goquery.Document) string {
	title := strings.TrimSpace(doc.Find("title").First().Text())
	if looksLikeChallenge(doc, title) {
		return "The page looks like a browser check (anti-bot) page, which this tool cannot pass."
	}

	return fmt.Sprintf("The start page (%q) has %d links but no series with a recognisable list of chapters; "+
		"try the address of one series page instead, or the page may add its content with JavaScript, which this tool does not run.",
		title, doc.Find("a").Length())
}

func depth(path string) int {
	n := 0
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			n++
		}
	}

	return n
}

func chapterLike(text, href string) bool {
	return chapterWord.MatchString(text) || chapterHref.MatchString(href)
}

// pageLinks lists the same-site links of a page that are not just anchors.
func pageLinks(page *url.URL, doc *goquery.Document) []link {
	var out []link
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		abs, ok := resolve(page, href)
		if !ok {
			return
		}
		u, err := url.Parse(abs)
		if err != nil || u.Host != page.Host || (u.Path == page.Path && u.RawQuery == page.RawQuery) {
			return
		}
		out = append(out, link{node: s.Nodes[0], url: abs, path: u.Path, text: strings.TrimSpace(s.Text()), title: s.AttrOr("title", "")})
	})

	return out
}

type link struct {
	node  *html.Node
	url   string
	path  string
	text  string
	title string
}

// seriesCandidates returns links that look like series pages, most common
// kind of link first: same first path segment and depth, not a chapter.
func seriesCandidates(page *url.URL, doc *goquery.Document) []string {
	groups := map[string][]string{}
	seen := map[string]bool{}
	for _, l := range pageLinks(page, doc) {
		if depth(l.path) < 2 || chapterLike(l.text+" "+l.title, l.path) || seen[l.url] {
			continue
		}
		seen[l.url] = true
		segs := strings.Split(strings.Trim(l.path, "/"), "/")
		key := segs[0] + "|" + fmt.Sprint(len(segs))
		groups[key] = append(groups[key], l.url)
	}

	keys := make([]string, 0, len(groups))
	for k, v := range groups {
		if len(v) >= minChapterLinks {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(groups[keys[i]]) != len(groups[keys[j]]) {
			return len(groups[keys[i]]) > len(groups[keys[j]])
		}
		return keys[i] < keys[j]
	})

	var out []string
	for _, k := range keys {
		out = append(out, groups[k]...)
		if len(out) >= 2*maxSeriesToTry {
			break
		}
	}

	return out
}

// chapterListOn finds the biggest group of chapter-like links on a page and
// builds a selector for it.
func (d *detector) chapterListOn(page *url.URL, doc *goquery.Document) (*chapterList, bool) {
	groups := map[string][]link{}
	for _, l := range pageLinks(page, doc) {
		if !chapterLike(l.text, l.path) {
			continue
		}
		key := hrefShape(l.path) + "|" + parentSignature(l.node)
		groups[key] = append(groups[key], l)
	}

	var best []link
	for _, g := range groups {
		if len(g) > len(best) {
			best = g
		}
	}
	if len(best) < minChapterLinks {
		return nil, false
	}

	nodes := make([]*html.Node, len(best))
	for i, l := range best {
		nodes[i] = l.node
	}
	selector, ok := synthesize(doc, nodes)
	if !ok {
		return nil, false
	}
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, false
	}
	chapters := chaptersFromDoc(page, doc, sel)
	if len(chapters) < minChapterLinks {
		return nil, false
	}

	return &chapterList{selector: selector, chapters: chapters}, true
}

var digits = regexp.MustCompile(`\d+`)

// hrefShape replaces numbers in a path so that chapter links of one series
// ("…-chap-12", "…-chap-13") share a shape.
func hrefShape(path string) string {
	return digits.ReplaceAllString(path, "#")
}

type imageGroup struct {
	selector string
	attrs    string
	urls     []string
}

// findImages finds the images of a chapter page: the biggest group of
// similar images, with the attribute that holds their real address.
func (d *detector) findImages(page *url.URL, doc *goquery.Document) (*imageGroup, error) {
	groups := map[string][]*html.Node{}
	doc.Find("img").Each(func(_ int, s *goquery.Selection) {
		n := s.Nodes[0]
		if notContent.MatchString(attrString(n, "class", "id", "alt")) {
			return
		}
		if _, _, ok := imageAddress(page, n); !ok {
			return
		}
		groups[parentSignature(n)] = append(groups[parentSignature(n)], n)
	})

	var best []*html.Node
	for _, g := range groups {
		if len(g) > len(best) {
			best = g
		}
	}
	if len(best) == 0 {
		title := strings.TrimSpace(doc.Find("title").First().Text())
		if looksLikeChallenge(doc, title) {
			return nil, errors.New("the chapter page looks like a browser check (anti-bot) page, which this tool cannot pass")
		}

		return nil, fmt.Errorf("no page images were found on the chapter page (%d <img> elements; the images may be added by JavaScript, which this tool does not run)", doc.Find("img").Length())
	}

	selector, ok := synthesize(doc, best)
	if !ok {
		return nil, errors.New("page images were found but no reliable selector could be built for them")
	}
	attrs := usableAttrs(page, best)
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, err
	}
	urls := imagesFromDoc(page, doc, sel, splitAttrs(attrs))
	if len(urls) == 0 {
		return nil, errors.New("page images were found but their addresses could not be read")
	}

	return &imageGroup{selector: selector, attrs: attrs, urls: urls}, nil
}

// imageAddress returns the first attribute (in lazy-loading priority order)
// holding an http(s) address that looks like an image.
func imageAddress(page *url.URL, n *html.Node) (string, string, bool) {
	for _, attr := range lazyAttrs {
		v := attrString(n, attr)
		if v == "" {
			continue
		}
		if abs, ok := resolve(page, v); ok && (imageExt.MatchString(abs) || attr != "src") {
			return attr, abs, true
		}
	}

	return "", "", false
}

// usableAttrs lists, in priority order, the attributes that hold distinct
// real addresses on at least half of the images. A value shared by all images
// is a placeholder and does not count.
func usableAttrs(page *url.URL, nodes []*html.Node) string {
	var attrs []string
	for _, attr := range lazyAttrs {
		values := map[string]int{}
		count := 0
		for _, n := range nodes {
			if abs, ok := resolve(page, attrString(n, attr)); ok {
				values[abs]++
				count++
			}
		}
		if count*2 < len(nodes) || (len(nodes) > 1 && len(values) == 1) {
			continue
		}
		attrs = append(attrs, attr)
	}

	return strings.Join(attrs, ",")
}

func attrString(n *html.Node, keys ...string) string {
	var parts []string
	for _, a := range n.Attr {
		for _, k := range keys {
			if a.Key == k {
				parts = append(parts, a.Val)
			}
		}
	}

	return strings.Join(parts, " ")
}

// mostTitlesHaveVolume reports whether enough chapter titles name a volume.
func (d *detector) mostTitlesHaveVolume(chapters []Chapter) bool {
	if len(chapters) < minChapterLinks {
		return false
	}
	n := 0
	for _, c := range chapters {
		if volumeTitle.MatchString(c.Title) {
			n++
		}
	}

	return float64(n) >= volumeShareToSet*float64(len(chapters))
}
