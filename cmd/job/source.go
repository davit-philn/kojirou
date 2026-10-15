package job

import (
	"context"

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
		site := selectorSource{selector.NewProvider(*opts.Source, lang)}
		if opts.VolumesFrom != "" {
			dex := mangadexSource{dataSaver: opts.DataSaver, interactive: opts.Interactive}
			return newVolumeMapSource(site, dex, dex, opts.Identifier, opts.VolumesFrom, lang)
		}

		return site
	}

	main := mangadexSource{dataSaver: opts.DataSaver, interactive: opts.Interactive}
	if opts.Fill != nil {
		extra := selectorSource{selector.NewProvider(opts.Fill.Config, lang)}
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
}

func (selectorSource) Name() string { return "selector" }
