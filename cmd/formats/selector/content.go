package selector

import (
	"context"
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
	"time"

	"github.com/leotaku/kojirou/cmd/formats"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/sync/errgroup"
	"golang.org/x/text/language"
)

const (
	maxAttempts   = 3
	maxRetryDelay = 30 * time.Second
	baseRetryWait = 2 * time.Second
)

var numberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

// Provider adapts a Source to the manga model used by the rest of the
// application. The "identifier" of a series is the URL of its chapter list.
type Provider struct {
	source *Source
	cfg    SourceConfig
	lang   language.Tag

	retryWait time.Duration
}

func NewProvider(cfg SourceConfig, lang language.Tag) *Provider {
	return &Provider{source: New(cfg), cfg: cfg, lang: lang, retryWait: baseRetryWait}
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
	chapters, err := p.source.FetchChapterList(ctx, seriesURL)
	if err != nil {
		return nil, err
	}

	group := hostOf(seriesURL, p.cfg.BaseURL)
	result := make(md.ChapterList, 0, len(chapters))
	// Sites usually list the newest chapter first, so number from the end.
	for i, ch := range chapters {
		position := len(chapters) - i
		volume := 1
		if n := p.cfg.ChaptersPerVolume; n > 0 {
			volume = (position-1)/n + 1
		}
		result = append(result, md.Chapter{
			Info: md.ChapterInfo{
				Title:            ch.Title,
				Language:         p.lang,
				GroupNames:       []string{group},
				ID:               ch.URL,
				Identifier:       chapterIdentifier(ch.Title, position),
				VolumeIdentifier: md.NewIdentifier(strconv.Itoa(volume)),
			},
			Pages: make(map[int]image.Image),
		})
	}

	return result, nil
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
		urls, err := p.source.FetchImages(egCtx, chapter.Info.ID)
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

func (p *Provider) download(ctx context.Context, imageURL, referer string) (image.Image, error) {
	client := p.source.Client()
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, p.backoff(attempt, lastErr)); err != nil {
				return nil, err
			}
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
			return img, nil
		}
		if !retryable {
			return nil, err
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

// chapterIdentifier uses the first number in the title, falling back to the
// chapter's position in the series.
func chapterIdentifier(title string, position int) md.Identifier {
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
