package cmd

import (
	"context"
	"fmt"
	"image"

	"github.com/leotaku/kojirou/cmd/filter"
	"github.com/leotaku/kojirou/cmd/formats"
	"github.com/leotaku/kojirou/cmd/formats/cbz"
	"github.com/leotaku/kojirou/cmd/formats/disk"
	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/formats/kindle"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func run(ctx context.Context) error {
	manga, err := download.MangadexSkeleton(ctx, identifierArg)
	if err != nil {
		return fmt.Errorf("skeleton: %w", err)
	}

	chapters, err := getChapters(ctx, *manga)
	if err != nil {
		return fmt.Errorf("chapters: %w", err)
	}
	*manga = manga.WithChapters(chapters)

	formats.PrintSummary(manga)
	if dryRunArg {
		return nil
	}

	covers, err := getCovers(ctx, manga)
	if err != nil {
		return fmt.Errorf("covers: %w", err)
	}
	*manga = manga.WithCovers(covers)

	if formatArg == FormatCBZ && kindleFolderModeArg {
		return fmt.Errorf("--kindle-folder-mode is not supported with --format=cbz")
	}

	if jpegQualityArg < 1 || jpegQualityArg > 100 {
		return fmt.Errorf("--jpeg-quality must be between 1 and 100")
	}

	var dir volumeWriter
	switch formatArg {
	case FormatCBZ:
		dir = newCBZWriter(outArg, manga.Info.Title)
	default:
		dir = newMOBIWriter(outArg, manga.Info.Title)
	}
	for _, volume := range manga.Sorted() {
		if err := handleVolume(ctx, *manga, volume, dir); err != nil {
			return fmt.Errorf("volume %v: %w", volume.Info.Identifier, err)
		}
	}

	return nil
}

func handleVolume(ctx context.Context, skeleton md.Manga, volume md.Volume, dir volumeWriter) error {
	p := formats.TitledProgress(fmt.Sprintf("Volume: %v", volume.Info.Identifier))
	if dir.Has(volume.Info.Identifier) && !forceArg {
		p.Cancel("Skipped")
		return nil
	}

	pages, err := getPages(ctx, volume, p)
	if err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	mangaForVolume := skeleton.WithChapters(volume.Sorted()).WithPages(pages)

	p = formats.VanishingProgress("Writing...")
	if err := dir.Write(volume.Info.Identifier, mangaForVolume, volume, p); err != nil {
		p.Cancel("Error")
		return fmt.Errorf("write: %w", err)
	}
	p.Done()

	return nil
}

func getChapters(ctx context.Context, manga md.Manga) (md.ChapterList, error) {
	chapters, err := download.MangadexChapters(ctx, manga.Info.ID)
	if err != nil {
		return nil, fmt.Errorf("mangadex: %w", err)
	}

	if diskArg != "" {
		p := formats.VanishingProgress("Disk...")
		diskChapters, err := disk.LoadChapters(diskArg, language.Make(languageArg), p)
		if err != nil {
			p.Cancel("Error")
			return nil, fmt.Errorf("disk: %w", err)
		}
		p.Done()
		chapters = append(chapters, diskChapters...)
	}

	chapters, err = filterAndSortFromFlags(chapters)
	if err != nil {
		return nil, fmt.Errorf("filter: %w", err)
	}

	// Ensure chapters from disk are preferred
	if diskArg != "" {
		chapters = chapters.SortBy(func(a md.ChapterInfo, b md.ChapterInfo) bool {
			return a.GroupNames.String() == "Filesystem" && b.GroupNames.String() != "Filesystem"
		})
	}

	return filter.RemoveDuplicates(chapters), nil
}

func getCovers(ctx context.Context, manga *md.Manga) (md.ImageList, error) {
	p := formats.VanishingProgress("Covers")
	covers, err := download.MangadexCovers(ctx, manga, p)
	if err != nil {
		p.Cancel("Error")
		return nil, fmt.Errorf("mangadex: %w", err)
	}
	p.Done()

	// Covers from disk should automatically be preferred, because
	// they appear later in the list and thus should override the
	// earlier downloaded covers.
	if diskArg != "" {
		p := formats.VanishingProgress("Disk...")
		diskCovers, err := disk.LoadCovers(diskArg, p)
		if err != nil {
			p.Cancel("Error")
			return nil, fmt.Errorf("disk: %w", err)
		}
		p.Done()
		covers = append(covers, diskCovers...)
	}

	return covers, nil
}

func getPages(ctx context.Context, volume md.Volume, p formats.CliProgress) (md.ImageList, error) {
	mangadexPages, err := download.MangadexPages(ctx, volume.Sorted().FilterBy(func(ci md.ChapterInfo) bool {
		return ci.GroupNames.String() != "Filesystem"
	}), download.DataSaverPolicy(dataSaverArg), p)
	if err != nil {
		p.Cancel("Error")
		return nil, fmt.Errorf("mangadex: %w", err)
	}
	diskPages, err := disk.LoadPages(volume.Sorted().FilterBy(func(ci md.ChapterInfo) bool {
		return ci.GroupNames.String() == "Filesystem"
	}), p)
	if err != nil {
		p.Cancel("Error")
		return nil, fmt.Errorf("disk: %w", err)
	}
	p.Done()

	return append(mangadexPages, diskPages...), nil
}

func filterAndSortFromFlags(cl md.ChapterList) (md.ChapterList, error) {
	if languageArg != "" {
		lang := language.Make(languageArg)
		cl = filter.FilterByLanguage(cl, lang)
	}
	if groupsFilter != "" {
		cl = filter.FilterByRegex(cl, "GroupNames", groupsFilter)
	}
	if volumesFilter != "" {
		ranges := filter.ParseRanges(volumesFilter)
		cl = filter.FilterByIdentifier(cl, "VolumeIdentifier", ranges)
	}
	if chaptersFilter != "" {
		ranges := filter.ParseRanges(chaptersFilter)
		cl = filter.FilterByIdentifier(cl, "Identifier", ranges)
	}

	switch rankArg {
	case "newest":
		cl = filter.SortByNewest(cl)
	case "newest-total":
		cl = filter.SortByNewestGroup(cl)
	case "most":
		cl = filter.SortByMost(cl)
	default:
		return nil, fmt.Errorf(`not a valid ranking algorithm: "%v"`, rankArg)
	}

	return cl, nil
}

// volumeWriter abstracts over the supported output formats.
type volumeWriter interface {
	Has(md.Identifier) bool
	Write(id md.Identifier, manga md.Manga, volume md.Volume, p formats.Progress) error
}

type mobiWriter struct {
	dir kindle.NormalizedDirectory
}

func newMOBIWriter(out, title string) *mobiWriter {
	return &mobiWriter{kindle.NewNormalizedDirectory(out, title, kindleFolderModeArg)}
}

func (w *mobiWriter) Has(id md.Identifier) bool {
	return w.dir.Has(id)
}

func (w *mobiWriter) Write(id md.Identifier, manga md.Manga, volume md.Volume, p formats.Progress) error {
	book := kindle.GenerateMOBI(
		manga,
		kindle.WidepagePolicy(widepageArg),
		autocropArg,
		leftToRightArg,
	)
	book.RightToLeft = !leftToRightArg
	book.Title = fmt.Sprintf("%v: %v",
		manga.Info.Title,
		volume.Info.Identifier.StringFilled(fillVolumeNumberArg, 0, false),
	)

	return w.dir.Write(id, book, p)
}

type cbzWriter struct {
	dir cbz.Directory
}

func newCBZWriter(out, title string) *cbzWriter {
	return &cbzWriter{cbz.NewDirectory(out, title)}
}

func (w *cbzWriter) Has(id md.Identifier) bool {
	return w.dir.Has(id)
}

func (w *cbzWriter) Write(id md.Identifier, manga md.Manga, _ md.Volume, p formats.Progress) error {
	return w.dir.Write(id, manga, cbz.Options{
		Process: func(img image.Image) []image.Image {
			return kindle.CropAndSplit(img, kindle.WidepagePolicy(widepageArg), autocropArg, leftToRightArg)
		},
		RightToLeft: !leftToRightArg,
		JPEGQuality: jpegQualityArg,
		Lossless:    losslessArg,
	}, p)
}
