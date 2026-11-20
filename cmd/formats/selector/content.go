package selector

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/leotaku/kojirou/cmd/formats"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/sync/errgroup"
	"golang.org/x/text/language"
)

const (
	pageAttempts  = 5
	pageSpacing   = 400 * time.Millisecond
	maxAttempts   = 3
	maxRetryDelay = 30 * time.Second
	baseRetryWait = 2 * time.Second
)

var (
	numberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)
	// chapterNumberPattern finds the number after a chapter keyword, so that
	// in "Tập 3 - Chương 7" the chapter is 7 and not the volume number 3.
	chapterNumberPattern = regexp.MustCompile(`(?i)\b(?:chương|chuong|chapter|chap|ch|episode|ep|#)\.?\s*(\d+(?:\.\d+)?)`)
)

// Provider adapts a Source to the manga model used by the rest of the
// application. The "identifier" of a series is the URL of its chapter list.
type Provider struct {
	// pages paces the requests for chapter pages, images the downloads of
	// pictures; both slow down for a while when the site answers 429.
	pages  *gate
	images *gate

	source   *Source
	cfg      SourceConfig
	lang     language.Tag
	rules    *volumeRules
	rulesErr error

	retryWait time.Duration
}

func NewProvider(cfg SourceConfig, lang language.Tag) *Provider {
	rules, err := compileVolumeRules(cfg)
	return &Provider{
		source: New(cfg), cfg: cfg, lang: lang, rules: rules, rulesErr: err, retryWait: baseRetryWait,
		pages: newGate(pageSpacing), images: newGate(0),
	}
}

func (p *Provider) Skeleton(_ context.Context, seriesURL string) (*md.Manga, error) {
	title := p.cfg.Title
	if title == "" {
		title = titleFromURL(seriesURL)
	}

	return &md.Manga{
		Info:    md.MangaInfo{Title: title, ID: seriesURL},
		Volumes: make(map[md.Identifier]md.Volume),
	}, nil
}

func (p *Provider) Chapters(ctx context.Context, seriesURL string) (md.ChapterList, error) {
	if p.rulesErr != nil {
		return nil, p.rulesErr
	}
	chapters, err := p.source.FetchChapterList(ctx, seriesURL)
	if err != nil {
		return nil, err
	}

	group := hostOf(seriesURL, p.cfg.BaseURL)
	result := make(md.ChapterList, 0, len(chapters))
	// Sites usually list the newest chapter first, so number from the end.
	for i, ch := range chapters {
		position := len(chapters) - i
		identifier := chapterIdentifier(ch.Title, position)
		result = append(result, md.Chapter{
			Info: md.ChapterInfo{
				Title:            ch.Title,
				Language:         p.lang,
				GroupNames:       []string{group},
				ID:               ch.URL,
				Identifier:       identifier,
				VolumeIdentifier: p.volumeFor(ch, identifier, position),
			},
			Pages: make(map[int]image.Image),
		})
	}

	return result, nil
}

// volumeFor applies the volume rules of the config; see SourceConfig.
func (p *Provider) volumeFor(ch formats.Chapter, id md.Identifier, position int) md.Identifier {
	number, err := strconv.ParseFloat(id.String(), 64)
	if label, ok := p.rules.volumeFor(ch.Title, ch.URL, number, err == nil); ok {
		return md.NewIdentifier(label)
	}

	switch {
	case p.cfg.ChaptersPerVolume > 0:
		return md.NewIdentifier(strconv.Itoa((position-1)/p.cfg.ChaptersPerVolume + 1))
	case p.rules.configured():
		// Rules exist but none matched this chapter: not in a volume yet.
		return md.NewWithFallback("", "Special")
	default:
		return md.NewIdentifier("1")
	}
}

// SeriesCover downloads one cover image of the series, e.g. the one a
// catalogue shows, with the source's Referer.
func (p *Provider) SeriesCover(ctx context.Context, coverURL string) (image.Image, error) {
	referer := p.cfg.Referer
	if referer == "" {
		referer = strings.TrimRight(p.cfg.BaseURL, "/") + "/"
	}

	return p.download(ctx, coverURL, referer)
}

// Covers returns no covers: a generic selector source has no notion of them.
func (p *Provider) Covers(context.Context, *md.Manga, formats.Progress) (md.ImageList, error) {
	return md.ImageList{}, nil
}

// Pages downloads all images of the given chapters. Downloads are limited to
// MaxConcurrentDownloads at a time and are retried with backoff on HTTP 429
// and 5xx responses.
func (p *Provider) Pages(parent context.Context, chapters md.ChapterList, prog formats.Progress) (md.ImageList, error) {
	limit := p.cfg.MaxConcurrentDownloads
	if limit <= 0 {
		limit = defaultConcurrency
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	eg, egCtx := errgroup.WithContext(ctx)
	// SetLimit makes eg.Go block while `limit` downloads are running, which
	// also paces how quickly further chapter pages are requested.
	eg.SetLimit(limit)

	results := make(md.ImageList, 0)
	resultCh := make(chan md.Image)
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for img := range resultCh {
			results = append(results, img)
		}
	}()

	var listErr error
	for _, chapter := range chapters {
		if egCtx.Err() != nil {
			break
		}
		prog.Increase(1)
		urls, err := p.fetchChapterImages(egCtx, chapter.Info.ID)
		if err != nil {
			listErr = fmt.Errorf("chapter %v: %w", chapter.Info.Identifier, err)
			cancel()
			break
		}
		prog.Add(1)
		prog.Increase(len(urls))

		for i, imageURL := range urls {
			eg.Go(func() error {
				img, err := p.download(egCtx, imageURL, chapter.Info.ID)
				if err != nil {
					return fmt.Errorf("chapter %v: image %v: %w", chapter.Info.Identifier, i+1, err)
				}
				select {
				case <-egCtx.Done():
					return egCtx.Err()
				case resultCh <- md.Image{
					Image:             img,
					ImageIdentifier:   i + 1,
					ChapterIdentifier: chapter.Info.Identifier,
					VolumeIdentifier:  chapter.Info.VolumeIdentifier,
				}:
					prog.Add(1)
					return nil
				}
			})
		}
	}

	waitErr := eg.Wait()
	close(resultCh)
	<-collected

	// Prefer the root cause over the context errors it triggered.
	switch {
	case listErr != nil:
		return nil, listErr
	case waitErr != nil:
		return nil, waitErr
	case parent.Err() != nil:
		return nil, parent.Err()
	}

	return results, nil
}

// fetchChapterImages reads a chapter page, a little apart from the previous
// request and again, with growing pauses, when the site limits the rate.
func (p *Provider) fetchChapterImages(ctx context.Context, chapterURL string) ([]string, error) {
	var lastErr error
	for attempt := 0; attempt < pageAttempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, p.backoff(attempt, lastErr)); err != nil {
				return nil, err
			}
		}
		if err := p.pages.wait(ctx); err != nil {
			return nil, err
		}

		urls, err := p.source.FetchImages(ctx, chapterURL)
		if err == nil {
			p.pages.succeeded()
			p.images.succeeded()
			return urls, nil
		}
		var status *httpStatusError
		if !errors.As(err, &status) || !status.retryable() {
			return nil, err
		}
		if status.code == http.StatusTooManyRequests {
			p.pages.slowDown()
			p.images.slowDown()
		}
		lastErr = &retryAfterError{status: status.status, after: status.after}
	}

	return nil, fmt.Errorf("the website keeps answering %v; wait a while (minutes) and download again, or set max_concurrent_downloads to 1", lastErr)
}

func (p *Provider) download(ctx context.Context, imageURL, referer string) (image.Image, error) {
	client := p.source.Client()
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, p.backoff(attempt, lastErr)); err != nil {
				return nil, err
			}
		}
		if err := p.images.wait(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
		if err != nil {
			return nil, fmt.Errorf("prepare: %w", err)
		}
		// Hotlink protection usually expects the chapter page as referer.
		req.Header.Set("Referer", referer)

		resp, err := client.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			lastErr = err
			continue
		}

		img, retryable, err := decodeResponse(resp)
		if err == nil {
			p.images.succeeded()
			return img, nil
		}
		if !retryable {
			return nil, err
		}
		if re, ok := err.(*retryAfterError); ok && strings.HasPrefix(re.status, "429") {
			p.pages.slowDown()
			p.images.slowDown()
		}
		lastErr = err
	}

	return nil, fmt.Errorf("giving up after %d attempts: %w", maxAttempts, lastErr)
}

type retryAfterError struct {
	status string
	after  time.Duration
}

func (e *retryAfterError) Error() string { return "status: " + e.status }

func decodeResponse(resp *http.Response) (image.Image, bool, error) {
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, true, &retryAfterError{resp.Status, parseRetryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("status: %v", resp.Status)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("decode: %w", err)
	}

	return img, false, nil
}

func (p *Provider) backoff(attempt int, err error) time.Duration {
	delay := p.retryWait << (attempt - 1)
	if re, ok := err.(*retryAfterError); ok && re.after > 0 {
		delay = re.after
	}
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}

	return delay
}

func parseRetryAfter(value string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}

	return 0
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// chapterIdentifier uses the number after a chapter keyword (Chương, Chapter,
// Ch, Ep...), else the first number in the title, else the chapter's position
// in the series.
func chapterIdentifier(title string, position int) md.Identifier {
	if m := chapterNumberPattern.FindStringSubmatch(title); m != nil {
		return md.NewIdentifier(m[1])
	}
	if m := numberPattern.FindString(title); m != "" {
		return md.NewIdentifier(m)
	}

	return md.NewIdentifier(strconv.Itoa(position))
}

func titleFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if base := path.Base(strings.TrimRight(u.Path, "/")); base != "" && base != "." && base != "/" {
		return base
	}

	return u.Host
}

func hostOf(rawURL, baseURL string) string {
	for _, candidate := range []string{rawURL, baseURL} {
		if u, err := url.Parse(candidate); err == nil && u.Host != "" {
			return u.Host
		}
	}

	return "web"
}

// gate spaces requests apart. Its pause grows when the site answers 429 and
// shrinks again as requests succeed.
type gate struct {
	mu      sync.Mutex
	base    time.Duration
	current time.Duration
	next    time.Time
	// floor and ceiling bound the pause after slowing down.
	floor, ceiling time.Duration
}

func newGate(base time.Duration) *gate {
	return &gate{base: base, current: base, floor: 500 * time.Millisecond, ceiling: 8 * time.Second}
}

// wait blocks until it is this request's turn.
func (g *gate) wait(ctx context.Context) error {
	g.mu.Lock()
	now := time.Now()
	at := g.next
	if at.Before(now) {
		at = now
	}
	g.next = at.Add(g.current)
	g.mu.Unlock()

	if d := time.Until(at); d > 0 {
		return sleep(ctx, d)
	}

	return nil
}

// slowDown doubles the pause (at least 500ms, at most 8s).
func (g *gate) slowDown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.current *= 2
	if g.current < g.floor {
		g.current = g.floor
	}
	if g.current > g.ceiling {
		g.current = g.ceiling
	}
}

// succeeded lets the pause shrink towards its normal size.
func (g *gate) succeeded() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.current > g.base {
		g.current -= g.current / 10
		if g.current < g.base {
			g.current = g.base
		}
	}
}
