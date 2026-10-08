// Package selector implements a generic, configuration-driven source that
// extracts chapter lists and image URLs from HTML pages using CSS selectors.
//
// It performs plain HTTP requests only. It does not execute JavaScript and
// does not attempt to bypass any access controls; point it only at sites
// whose terms allow this kind of automated access.
package selector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

const (
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "kojirou"
	defaultImageAttr = "src"
	maxBodyBytes     = 16 << 20
)

// SourceConfig describes how to extract data from one site.
type SourceConfig struct {
	BaseURL   string `json:"base_url"`
	UserAgent string `json:"user_agent"`
	Referer   string `json:"referer"`

	// ChapterListSelector selects one element per chapter. The element
	// itself, or its first descendant <a>, must carry an href attribute.
	ChapterListSelector string `json:"chapter_list_selector"`
	// ImageListSelector selects one element per page image.
	ImageListSelector string `json:"image_list_selector"`
	// ImageAttr names the attribute holding the image URL, e.g. "src" or
	// "data-original". Defaults to "src".
	ImageAttr string `json:"image_attr"`

	// TimeoutSeconds bounds each HTTP request. Defaults to 30.
	TimeoutSeconds int `json:"timeout_seconds"`
}

// Chapter is a single entry of a chapter list.
type Chapter struct {
	Title string
	URL   string
}

// NewClient returns an HTTP client with the configured timeout that sends
// the configured User-Agent and Referer on every request. It can also be
// used to download the images returned by FetchImages.
func NewClient(cfg SourceConfig) *http.Client {
	timeout := defaultTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &headerTransport{
			base:      http.DefaultTransport,
			userAgent: userAgent,
			referer:   cfg.Referer,
		},
	}
}

type headerTransport struct {
	base      http.RoundTripper
	userAgent string
	referer   string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTrippers must not modify the caller's request.
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.userAgent)
	if t.referer != "" {
		req.Header.Set("Referer", t.referer)
	}

	return t.base.RoundTrip(req)
}

// FetchChapterList downloads the page at pageURL (resolved against
// cfg.BaseURL) and returns the chapters matched by cfg.ChapterListSelector,
// in document order.
func FetchChapterList(ctx context.Context, pageURL string, cfg SourceConfig) ([]Chapter, error) {
	return fetchChapterList(ctx, NewClient(cfg), pageURL, cfg)
}

// FetchImages downloads the chapter page at chapterURL (resolved against
// cfg.BaseURL) and returns the absolute image URLs matched by
// cfg.ImageListSelector, in document order.
func FetchImages(ctx context.Context, chapterURL string, cfg SourceConfig) ([]string, error) {
	return fetchImages(ctx, NewClient(cfg), chapterURL, cfg)
}

// Source bundles a configuration with a reusable HTTP client.
type Source struct {
	cfg    SourceConfig
	client *http.Client
}

func New(cfg SourceConfig) *Source {
	return &Source{cfg: cfg, client: NewClient(cfg)}
}

func (s *Source) FetchChapterList(ctx context.Context, pageURL string) ([]Chapter, error) {
	return fetchChapterList(ctx, s.client, pageURL, s.cfg)
}

func (s *Source) FetchImages(ctx context.Context, chapterURL string) ([]string, error) {
	return fetchImages(ctx, s.client, chapterURL, s.cfg)
}

func (s *Source) Client() *http.Client {
	return s.client
}

func fetchChapterList(ctx context.Context, client *http.Client, pageURL string, cfg SourceConfig) ([]Chapter, error) {
	sel, err := compile("chapter_list_selector", cfg.ChapterListSelector)
	if err != nil {
		return nil, err
	}
	page, doc, err := fetchDocument(ctx, client, pageURL, cfg)
	if err != nil {
		return nil, err
	}

	chapters := make([]Chapter, 0)
	doc.FindMatcher(sel).Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		title := s
		if !ok {
			title = s.Find("a[href]").First()
			href, ok = title.Attr("href")
		}
		if !ok {
			return
		}
		if abs, ok := resolve(page, href); ok {
			chapters = append(chapters, Chapter{
				Title: strings.TrimSpace(s.Text()),
				URL:   abs,
			})
		}
	})

	if len(chapters) == 0 {
		return nil, fmt.Errorf("no chapters matched %q on %v", cfg.ChapterListSelector, page)
	}

	return chapters, nil
}

func fetchImages(ctx context.Context, client *http.Client, chapterURL string, cfg SourceConfig) ([]string, error) {
	sel, err := compile("image_list_selector", cfg.ImageListSelector)
	if err != nil {
		return nil, err
	}
	attr := cfg.ImageAttr
	if attr == "" {
		attr = defaultImageAttr
	}
	page, doc, err := fetchDocument(ctx, client, chapterURL, cfg)
	if err != nil {
		return nil, err
	}

	images := make([]string, 0)
	doc.FindMatcher(sel).Each(func(_ int, s *goquery.Selection) {
		value, ok := s.Attr(attr)
		if !ok {
			return
		}
		if abs, ok := resolve(page, value); ok {
			images = append(images, abs)
		}
	})

	if len(images) == 0 {
		return nil, fmt.Errorf("no images matched %q with attribute %q on %v", cfg.ImageListSelector, attr, page)
	}

	return images, nil
}

func compile(name, selector string) (cascadia.Selector, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("%v must not be empty", name)
	}
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, fmt.Errorf("invalid %v %q: %w", name, selector, err)
	}

	return sel, nil
}

func fetchDocument(ctx context.Context, client *http.Client, rawURL string, cfg SourceConfig) (*url.URL, *goquery.Document, error) {
	target, err := resolveAgainstBase(cfg.BaseURL, rawURL)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, fmt.Errorf("get %v: %w", target, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("get %v: status %v", target, resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, fmt.Errorf("parse %v: %w", target, err)
	}

	// Relative links must resolve against the final URL after redirects.
	if resp.Request != nil && resp.Request.URL != nil {
		target = resp.Request.URL
	}

	return target, doc, nil
}

func resolveAgainstBase(baseURL, rawURL string) (*url.URL, error) {
	ref, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", rawURL, err)
	}
	if baseURL != "" {
		base, err := url.Parse(baseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid base_url %q: %w", baseURL, err)
		}
		ref = base.ResolveReference(ref)
	}
	if !isHTTP(ref) {
		return nil, errors.New("url must be absolute http(s), or relative with a base_url: " + rawURL)
	}

	return ref, nil
}

// resolve turns a possibly relative link into an absolute http(s) URL.
func resolve(page *url.URL, link string) (string, bool) {
	ref, err := url.Parse(strings.TrimSpace(link))
	if err != nil || link == "" {
		return "", false
	}
	abs := page.ResolveReference(ref)
	if !isHTTP(abs) {
		return "", false
	}

	return abs.String(), true
}

func isHTTP(u *url.URL) bool {
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
