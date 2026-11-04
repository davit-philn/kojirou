package selector

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

const (
	maxSeriesPerPage  = 200
	maxTitleRunes     = 200
	searchPlaceholder = "{q}"
)

// SeriesItem is one series of a catalogue page.
type SeriesItem struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	CoverURL string `json:"coverUrl,omitempty"`
}

// SeriesPage is one page of a source's catalogue.
type SeriesPage struct {
	Items []SeriesItem `json:"items"`
	// Next is the address of the next page, or empty.
	Next string `json:"next,omitempty"`
}

// Browsable reports whether the source can list series.
func (c SourceConfig) Browsable() bool { return strings.TrimSpace(c.SeriesSelector) != "" }

// Searchable reports whether the source has a search address.
func (c SourceConfig) Searchable() bool { return c.SearchURL != "" }

func validateBrowse(cfg SourceConfig) error {
	if cfg.SeriesSelector != "" {
		if _, err := compile("series_selector", cfg.SeriesSelector); err != nil {
			return err
		}
	}
	if cfg.NextPageSelector != "" {
		if _, err := compile("next_page_selector", cfg.NextPageSelector); err != nil {
			return err
		}
	}
	if cfg.ListURL != "" {
		if _, err := resolveAgainstBase(cfg.BaseURL, cfg.ListURL); err != nil {
			return fmt.Errorf("list_url: %w", err)
		}
	}
	if cfg.SearchURL != "" {
		if !strings.Contains(cfg.SearchURL, searchPlaceholder) {
			return errors.New("search_url must contain {q} where the search text goes")
		}
		if _, err := resolveAgainstBase(cfg.BaseURL, strings.ReplaceAll(cfg.SearchURL, searchPlaceholder, "x")); err != nil {
			return fmt.Errorf("search_url: %w", err)
		}
	}
	if (cfg.SearchURL != "" || cfg.NextPageSelector != "" || cfg.ListURL != "") && cfg.SeriesSelector == "" {
		return errors.New("series_selector is needed for list_url, next_page_selector and search_url")
	}

	return nil
}

// SearchAddress returns the search address for a text.
func (c SourceConfig) SearchAddress(text string) (string, error) {
	if !c.Searchable() {
		return "", errors.New("this source has no search address")
	}

	return strings.ReplaceAll(c.SearchURL, searchPlaceholder, url.QueryEscape(strings.TrimSpace(text))), nil
}

// DefaultListURL is the address of the first catalogue page.
func (c SourceConfig) DefaultListURL() string {
	if c.ListURL != "" {
		return c.ListURL
	}

	return c.BaseURL
}

// SameSite reports whether rawURL is on the same website as the source's
// base address (ignoring a leading "www.").
func (c SourceConfig) SameSite(rawURL string) bool {
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Host == "" {
		return false
	}
	target, err := resolveAgainstBase(c.BaseURL, rawURL)
	if err != nil {
		return false
	}

	return strings.TrimPrefix(strings.ToLower(target.Host), "www.") == strings.TrimPrefix(strings.ToLower(base.Host), "www.")
}

// ListSeries reads one catalogue page: the series on it and the next page.
// The page must be on the source's own website.
func ListSeries(ctx context.Context, pageURL string, cfg SourceConfig) (*SeriesPage, error) {
	if !cfg.Browsable() {
		return nil, errors.New("this source cannot list series")
	}
	if !cfg.SameSite(pageURL) {
		return nil, errors.New("the list page must be on the source's own website")
	}
	sel, err := compile("series_selector", cfg.SeriesSelector)
	if err != nil {
		return nil, err
	}
	page, doc, err := fetchDocument(ctx, NewClient(cfg), pageURL, cfg)
	if err != nil {
		return nil, err
	}

	result := &SeriesPage{Items: seriesFromDoc(page, doc, sel)}
	if len(result.Items) == 0 {
		return nil, fmt.Errorf("no series found on %v: %v", page, describeNoMatch(doc, sel, cfg.SeriesSelector, nil, "series"))
	}
	if cfg.NextPageSelector != "" {
		if next, err := compile("next_page_selector", cfg.NextPageSelector); err == nil {
			result.Next = nextPageFromDoc(page, doc, next)
		}
	}

	return result, nil
}

// seriesFromDoc lists the series the selector matches, once per address.
func seriesFromDoc(page *url.URL, doc *goquery.Document, sel cascadia.Selector) []SeriesItem {
	items := make([]SeriesItem, 0)
	seen := map[string]bool{}
	doc.FindMatcher(sel).Each(func(_ int, s *goquery.Selection) {
		if len(items) >= maxSeriesPerPage {
			return
		}
		anchor := s
		if _, ok := s.Attr("href"); !ok {
			anchor = s.Find("a[href]").First()
		}
		href, ok := anchor.Attr("href")
		if !ok {
			return
		}
		abs, ok := resolve(page, href)
		if !ok || seen[abs] {
			return
		}
		seen[abs] = true

		title := cardTitle(s, anchor)
		if title == "" {
			title = titleFromURL(abs)
		}
		items = append(items, SeriesItem{URL: abs, Title: title, CoverURL: cardCover(page, s)})
	})

	return items
}

// cardTitle finds the series name of a card: the link's title attribute, a
// heading, the cover's alt text, or else the text of the link.
func cardTitle(card, anchor *goquery.Selection) string {
	candidates := []string{
		anchor.AttrOr("title", ""),
		card.Find("h1,h2,h3,h4,h5,h6").First().Text(),
		card.Find("img").First().AttrOr("alt", ""),
		anchor.Text(),
		card.Text(),
	}
	for _, c := range candidates[:4] {
		if title := truncate(strings.Join(strings.Fields(c), " ")); title != "" {
			return title
		}
	}
	// The name may sit next to the link, in the card around it.
	for _, ancestor := range ancestors(card, 2) {
		if headings := ancestor.Find("h1,h2,h3,h4,h5,h6"); headings.Length() == 1 {
			if title := truncate(strings.Join(strings.Fields(headings.Text()), " ")); title != "" {
				return title
			}
		}
	}

	return truncate(strings.Join(strings.Fields(candidates[4]), " "))
}

// ancestors returns up to n parents of a selection, nearest first.
func ancestors(s *goquery.Selection, n int) []*goquery.Selection {
	var out []*goquery.Selection
	for p := s.Parent(); p.Length() > 0 && len(out) < n; p = p.Parent() {
		out = append(out, p)
	}

	return out
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) > maxTitleRunes {
		return string(r[:maxTitleRunes])
	}

	return s
}

// cardCover returns the first image of a card, looking at the attributes
// lazy-loading pages use as well as src.
func cardCover(page *url.URL, card *goquery.Selection) string {
	img := card.Find("img").First()
	if card.Is("img") {
		img = card
	}
	if img.Length() == 0 {
		// The cover may sit next to the link, in the card around it.
		for _, ancestor := range ancestors(card, 2) {
			if imgs := ancestor.Find("img"); imgs.Length() == 1 {
				img = imgs
				break
			}
		}
	}
	for _, attr := range lazyAttrs {
		if v, ok := img.Attr(attr); ok {
			if abs, ok := resolve(page, v); ok {
				return abs
			}
		}
	}

	return ""
}

func nextPageFromDoc(page *url.URL, doc *goquery.Document, sel cascadia.Selector) string {
	next := doc.FindMatcher(sel).First()
	href, ok := next.Attr("href")
	if !ok {
		href, ok = next.Find("a[href]").First().Attr("href")
	}
	if !ok {
		return ""
	}
	abs, ok := resolve(page, href)
	if !ok || abs == page.String() {
		return ""
	}

	return abs
}
