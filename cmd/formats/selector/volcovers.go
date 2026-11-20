package selector

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/sync/errgroup"
)

const (
	maxVolumeCovers   = 200
	coverDownloaders  = 4
	maxCoverImageSize = 12 << 20
)

// VolumeCover is the cover of one volume on a series page.
type VolumeCover struct {
	// Volume is the volume number as written on the page, e.g. "1" or "2.5".
	Volume string `json:"volume"`
	// URL is the address of the cover image.
	URL string `json:"url"`
}

var (
	// volumeInPath finds the volume number in the last part of an address,
	// e.g. "undead-unluck-vol-12" or "series-volume-3".
	volumeInPath = regexp.MustCompile(`(?i)(?:^|[-_/])vol(?:ume)?[-_ ]?(\d+(?:\.\d+)?)$`)
	srcsetItem   = regexp.MustCompile(`(\S+)\s+(\d+)w`)
	// volumeSection matches addresses of the form /volume/<id>/<title>.
	volumeSection   = regexp.MustCompile(`(?i)^/(?:volumes?|vol)/[^/]+/[^/]+/?$`)
	cardVolumeLabel = regexp.MustCompile(`(?i)\bvol(?:ume)?\.?\s*(\d+(?:\.\d+)?)\b`)
)

// FindVolumeCovers reads a series page that shows its volumes as cards (a link
// to each volume with the volume's number in its address, and the cover next
// to it) and returns the cover of every volume. It obeys robots.txt and reads
// plain HTML only: the covers must be in the page itself.
func FindVolumeCovers(ctx context.Context, pageURL string, client *http.Client) ([]VolumeCover, error) {
	target, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, errors.New("enter the address of a series page, starting with http:// or https://")
	}
	if client == nil {
		client = NewClient(SourceConfig{UserAgent: detectUserAgent, TimeoutSeconds: 30})
	}

	d := &detector{ctx: ctx, client: client, host: target.Host, delay: detectDelay}
	d.robots = d.loadRobots(target)
	if !d.robots.allowed(target.RequestURI()) {
		return nil, fmt.Errorf("the website's robots.txt does not allow automated access to %v", target.Path)
	}
	page, doc, err := fetchDocument(ctx, client, target.String(), SourceConfig{})
	if err != nil {
		return nil, err
	}

	covers := volumeCoversFromDoc(page, doc)
	if len(covers) == 0 {
		// A page of one volume links to its series, which lists all volumes.
		if series := seriesLinkOf(page, doc); series != "" && d.robots.allowed(pathOf(series)) {
			if next, nextDoc, err := fetchDocument(ctx, client, series, SourceConfig{}); err == nil {
				covers = volumeCoversFromDoc(next, nextDoc)
			}
		}
	}
	if len(covers) == 0 {
		title := strings.TrimSpace(doc.Find("title").First().Text())
		if looksLikeChallenge(doc, title) {
			return nil, errors.New("the page looks like a browser check (anti-bot) page, which this tool cannot pass")
		}

		return nil, fmt.Errorf("no volume covers were found on this page (%d links, %d images). "+
			"The page must be a series page that lists the volumes with their covers in its HTML (for BookWalker: .../series/<id>/<title>); "+
			"the page of a single volume does not list them, and pages that add them with JavaScript cannot be read",
			doc.Find("a[href]").Length(), doc.Find("img").Length())
	}

	return covers, nil
}

// seriesLinkOf returns the address of the series page a volume page links to
// (".../series/<id>/<title>" on the same website), or "".
func seriesLinkOf(page *url.URL, doc *goquery.Document) string {
	var found string
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		abs, ok := resolve(page, a.AttrOr("href", ""))
		if !ok {
			return true
		}
		u, err := url.Parse(abs)
		if err != nil || !sameHost(u.Host, page.Host) || u.Path == page.Path {
			return true
		}
		if seriesSection.MatchString(u.Path) {
			found = (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
			return false
		}

		return true
	})

	return found
}

var seriesSection = regexp.MustCompile(`(?i)^/series/[^/]+(?:/[^/]+)?/?$`)

// volumeCoversFromDoc finds the volume cards of a parsed page.
func volumeCoversFromDoc(page *url.URL, doc *goquery.Document) []VolumeCover {
	type found struct {
		volume float64
		text   string
		cover  string
	}
	byVolume := map[string]*found{}

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		abs, ok := resolve(page, href)
		if !ok {
			return
		}
		u, err := url.Parse(abs)
		if err != nil {
			return
		}
		last := u.Path[strings.LastIndex(u.Path, "/")+1:]
		m := volumeInPath.FindStringSubmatch(last)
		number := ""
		switch {
		case m != nil:
			number = m[1]
		case volumeSection.MatchString(u.Path):
			// "/volume/ID/title" without a number in the title (a one-volume
			// series, say): read the card's own label, e.g. "VOL 1".
			number = labelledVolume(a)
		}
		if number == "" {
			return
		}
		if seen, ok := byVolume[number]; ok && seen.cover != "" {
			return
		}
		cover := coverNear(page, a, number)
		if cover == "" {
			return
		}
		value, _ := strconv.ParseFloat(number, 64)
		byVolume[number] = &found{volume: value, text: number, cover: cover}
	})

	list := make([]*found, 0, len(byVolume))
	for _, f := range byVolume {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].volume < list[j].volume })
	result := make([]VolumeCover, 0, len(list))
	for _, f := range list {
		if len(result) >= maxVolumeCovers {
			break
		}
		result = append(result, VolumeCover{Volume: f.text, URL: f.cover})
	}

	return result
}

// labelledVolume reads "VOL 1" from the card around a volume link. The words
// and the number are often in separate elements, so the text is joined with
// spaces.
func labelledVolume(a *goquery.Selection) string {
	scope := a
	for depth := 0; depth < 4; depth++ {
		if m := cardVolumeLabel.FindStringSubmatch(spacedText(scope)); m != nil {
			return m[1]
		}
		scope = scope.Parent()
		if scope.Length() == 0 {
			break
		}
	}

	return ""
}

// spacedText is the text of a selection with a space between text nodes.
func spacedText(s *goquery.Selection) string {
	var parts []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				parts = append(parts, t)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range s.Nodes {
		walk(n)
	}

	return strings.Join(parts, " ")
}

// coverNear returns the cover image of the card a volume link belongs to:
// the first image inside the link or inside the nearest ancestor that shows
// no other volume.
func coverNear(page *url.URL, a *goquery.Selection, volume string) string {
	scope := a
	for depth := 0; depth < 6; depth++ {
		if img := scope.Find("img").First(); img.Length() > 0 {
			if src := bestImage(page, img); src != "" {
				return src
			}
		}
		parent := scope.Parent()
		if parent.Length() == 0 || showsOtherVolume(page, parent, volume) {
			return ""
		}
		scope = parent
	}

	return ""
}

// showsOtherVolume reports whether a section links to a volume other than the
// given one, i.e. is more than one card.
func showsOtherVolume(page *url.URL, section *goquery.Selection, volume string) bool {
	other := false
	section.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		if abs, ok := resolve(page, href); ok {
			if u, err := url.Parse(abs); err == nil {
				if m := volumeInPath.FindStringSubmatch(u.Path[strings.LastIndex(u.Path, "/")+1:]); m != nil && m[1] != volume {
					other = true
					return false
				}
			}
		}

		return true
	})

	return other
}

// bestImage picks the largest picture an <img> offers: the widest entry of
// its srcset, or else its (lazy-loading) source attributes.
func bestImage(page *url.URL, img *goquery.Selection) string {
	best, bestWidth := "", -1
	for _, item := range srcsetItem.FindAllStringSubmatch(img.AttrOr("srcset", ""), -1) {
		width, _ := strconv.Atoi(item[2])
		if abs, ok := resolve(page, item[1]); ok && width > bestWidth {
			best, bestWidth = abs, width
		}
	}
	if best != "" {
		return best
	}
	for _, attr := range lazyAttrs {
		if v := img.AttrOr(attr, ""); v != "" && !strings.HasPrefix(v, "data:") {
			if abs, ok := resolve(page, v); ok && imageExt.MatchString(abs) {
				return abs
			}
		}
	}

	return ""
}

// DownloadVolumeCovers downloads the covers, a few at a time. A cover that
// cannot be fetched is left out of the result.
func DownloadVolumeCovers(ctx context.Context, covers []VolumeCover, client *http.Client) map[string]image.Image {
	if client == nil {
		client = NewClient(SourceConfig{UserAgent: detectUserAgent, TimeoutSeconds: 30})
	}
	var mu sync.Mutex
	result := make(map[string]image.Image, len(covers))
	var group errgroup.Group
	group.SetLimit(coverDownloaders)
	for _, c := range covers {
		group.Go(func() error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
			if err != nil {
				return nil
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil
			}
			defer resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != http.StatusOK {
				return nil
			}
			img, _, err := image.Decode(io.LimitReader(resp.Body, maxCoverImageSize))
			if err != nil {
				return nil
			}
			mu.Lock()
			result[c.Volume] = img
			mu.Unlock()

			return nil
		})
	}
	_ = group.Wait()

	return result
}
