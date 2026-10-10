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
	if opts.Source != nil {
		return selectorSource{selector.NewProvider(*opts.Source, language.Make(opts.Language))}
	}

	return mangadexSource{dataSaver: opts.DataSaver}
}

type mangadexSource struct {
	dataSaver download.DataSaverPolicy
}

func (mangadexSource) Name() string { return "mangadex" }

func (mangadexSource) Skeleton(ctx context.Context, id string) (*md.Manga, error) {
	return download.MangadexSkeleton(ctx, id)
}

func (mangadexSource) Chapters(ctx context.Context, id string) (md.ChapterList, error) {
	return download.MangadexChapters(ctx, id)
}

func (mangadexSource) Covers(ctx context.Context, manga *md.Manga, p formats.Progress) (md.ImageList, error) {
	return download.MangadexCovers(ctx, manga, p)
}

func (s mangadexSource) Pages(ctx context.Context, chapters md.ChapterList, p formats.Progress) (md.ImageList, error) {
	return download.MangadexPages(ctx, chapters, s.dataSaver, p)
}

type selectorSource struct {
	*selector.Provider
}

func (selectorSource) Name() string { return "selector" }
