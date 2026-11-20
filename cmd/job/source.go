package job

import (
	"context"
	"fmt"

	"github.com/leotaku/kojirou/cmd/formats"
	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/formats/selector"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

// contentSource is where series metadata, chapters, covers and pages come
// from. Chapters from Options.Disk are merged in separately.
type contentSource interface {
	Name() string
	Skeleton(ctx context.Context, id string) (*md.Manga, error)
	Chapters(ctx context.Context, id string) (md.ChapterList, error)
	Covers(ctx context.Context, manga *md.Manga, p formats.Progress) (md.ImageList, error)
	Pages(ctx context.Context, chapters md.ChapterList, p formats.Progress) (md.ImageList, error)
}

func newContentSource(opts Options) contentSource {
	lang := language.Make(opts.Language)
	if opts.Source != nil {
		site := selectorSource{Provider: selector.NewProvider(*opts.Source, lang), coverURL: opts.CoverURL, problem: new(string)}
		if opts.VolumesFrom != "" {
			dex := mangadexSource{dataSaver: opts.DataSaver, interactive: opts.Interactive}
			src := newVolumeMapSource(site, dex, dex, opts.Identifier, opts.VolumesFrom, lang)
			if opts.Wikipedia != nil {
				src.wiki = opts.Wikipedia
			}

			return src
		}

		return site
	}

	main := mangadexSource{dataSaver: opts.DataSaver, interactive: opts.Interactive}
	if opts.Fill != nil {
		extra := selectorSource{Provider: selector.NewProvider(opts.Fill.Config, lang)}
		return newFillSource(main, extra, opts.Fill.URL, lang)
	}

	return main
}

type mangadexSource struct {
	dataSaver   download.DataSaverPolicy
	interactive bool
}

func (s mangadexSource) client() *md.Client {
	if s.interactive {
		return download.InteractiveClient()
	}

	return download.PatientClient()
}

func (mangadexSource) Name() string { return "mangadex" }

func (s mangadexSource) Skeleton(ctx context.Context, id string) (*md.Manga, error) {
	return s.client().FetchManga(ctx, id)
}

func (s mangadexSource) Chapters(ctx context.Context, id string) (md.ChapterList, error) {
	return s.client().FetchChapters(ctx, id)
}

func (mangadexSource) Covers(ctx context.Context, manga *md.Manga, p formats.Progress) (md.ImageList, error) {
	return download.MangadexCovers(ctx, manga, p)
}

// VolumeMap says which volume each chapter number belongs to on MangaDex.
func (s mangadexSource) VolumeMap(ctx context.Context, id string) (map[md.Identifier]md.Identifier, error) {
	return s.client().FetchVolumeMap(ctx, id)
}

func (s mangadexSource) Pages(ctx context.Context, chapters md.ChapterList, p formats.Progress) (md.ImageList, error) {
	return download.MangadexPages(ctx, chapters, s.dataSaver, p)
}

type selectorSource struct {
	*selector.Provider
	// coverURL is a cover image of the whole series, used for every volume
	// when the source itself has no covers.
	coverURL string
	// problem remembers why the series cover could not be fetched.
	problem *string
}

// CoverProblem says why the series cover could not be used, or "".
func (s selectorSource) CoverProblem() string {
	if s.problem == nil {
		return ""
	}

	return *s.problem
}

// Covers gives every volume the series cover. A cover that cannot be fetched
// is skipped: the chapters matter more than the picture.
func (s selectorSource) Covers(ctx context.Context, manga *md.Manga, _ formats.Progress) (md.ImageList, error) {
	if s.coverURL == "" {
		return md.ImageList{}, nil
	}
	img, err := s.Provider.SeriesCover(ctx, s.coverURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if s.problem != nil {
			*s.problem = fmt.Sprintf("the series cover could not be downloaded (%v)", err)
		}
		return md.ImageList{}, nil
	}
	covers := make(md.ImageList, 0, len(manga.Volumes))
	for id := range manga.Volumes {
		covers = append(covers, md.Image{Image: img, VolumeIdentifier: id})
	}

	return covers, nil
}

func (selectorSource) Name() string { return "selector" }
