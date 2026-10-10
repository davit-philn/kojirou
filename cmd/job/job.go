package job

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"strings"

	"github.com/leotaku/kojirou/cmd/filter"
	"github.com/leotaku/kojirou/cmd/formats/cbz"
	"github.com/leotaku/kojirou/cmd/formats/disk"
	"github.com/leotaku/kojirou/cmd/formats/kindle"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

// Inspect fetches the series and the chapters that the options select,
// without downloading any pages.
func Inspect(ctx context.Context, opts Options, rep Reporter) (*md.Manga, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}

	return inspect(ctx, newContentSource(opts), opts, rep)
}

func inspect(ctx context.Context, src contentSource, opts Options, rep Reporter) (*md.Manga, error) {
	manga, err := src.Skeleton(ctx, opts.Identifier)
	if err != nil {
		return nil, fmt.Errorf("skeleton: %w", err)
	}

	chapters, err := getChapters(ctx, src, opts, rep, *manga)
	if err != nil {
		return nil, fmt.Errorf("chapters: %w", err)
	}
	*manga = manga.WithChapters(chapters)

	return manga, nil
}

// Run fetches the selected chapters and writes one e-book per volume.
func Run(ctx context.Context, opts Options, rep Reporter) (*Result, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if rep == nil {
		rep = NopReporter{}
	}

	src := newContentSource(opts)
	manga, err := inspect(ctx, src, opts, rep)
	if err != nil {
		return nil, err
	}

	rep.Summary(manga)
	result := &Result{Title: manga.Info.Title}
	if opts.DryRun {
		return result, nil
	}

	covers, err := getCovers(ctx, src, opts, rep, manga)
	if err != nil {
		return nil, fmt.Errorf("covers: %w", err)
	}
	*manga = manga.WithCovers(covers)

	out := outputDirectory(opts, manga.Info.Title)
	var dir volumeWriter
	switch opts.Format {
	case FormatCBZ:
		dir = newCBZWriter(opts, out, manga.Info.Title)
	default:
		dir = newMOBIWriter(opts, out, manga.Info.Title)
	}
	result.Dir = out

	for _, volume := range manga.Sorted() {
		if err := handleVolume(ctx, src, opts, rep, *manga, volume, dir); err != nil {
			return result, fmt.Errorf("volume %v: %w", volume.Info.Identifier, err)
		}
	}

	return result, nil
}

// outputDirectory returns the directory volumes are written to, or "" if
// the writer should derive it from the title itself.
func outputDirectory(opts Options, title string) string {
	if opts.Out != "" || opts.LibraryDir == "" {
		return opts.Out
	}

	return filepath.Join(opts.LibraryDir, SafeDirName(title))
}

// SafeDirName turns a series title into a single, harmless directory name.
func SafeDirName(title string) string {
	name := strings.TrimSpace(kindle.PathnameFromTitle(title))
	name = strings.Trim(name, ". ")
	name = strings.NewReplacer("\x00", "", "\\", "／").Replace(name)
	if name == "" {
		return "untitled"
	}

	return name
}

func handleVolume(
	ctx context.Context,
	src contentSource,
	opts Options,
	rep Reporter,
	skeleton md.Manga,
	volume md.Volume,
	dir volumeWriter,
) error {
	p := rep.Task(fmt.Sprintf("Volume: %v", volume.Info.Identifier), false)
	if dir.Has(volume.Info.Identifier) && !opts.Force {
		p.Cancel("Skipped")
		return nil
	}

	pages, err := getPages(ctx, src, opts, rep, volume, p)
	if err != nil {
		return fmt.Errorf("pages: %w", err)
	}

	mangaForVolume := skeleton.WithChapters(volume.Sorted()).WithPages(pages)

	p = rep.Task("Writing...", true)
	if err := dir.Write(volume.Info.Identifier, mangaForVolume, volume, p); err != nil {
		p.Cancel("Error")
		return fmt.Errorf("write: %w", err)
	}
	p.Done()

	return nil
}

func getChapters(
	ctx context.Context,
	src contentSource,
	opts Options,
	rep Reporter,
	manga md.Manga,
) (md.ChapterList, error) {
	chapters, err := src.Chapters(ctx, manga.Info.ID)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", src.Name(), err)
	}

	if opts.Disk != "" {
		p := rep.Task("Disk...", true)
		diskChapters, err := disk.LoadChapters(opts.Disk, language.Make(opts.Language), p)
		if err != nil {
			p.Cancel("Error")
			return nil, fmt.Errorf("disk: %w", err)
		}
		p.Done()
		chapters = append(chapters, diskChapters...)
	}

	chapters, err = filterAndSort(chapters, opts)
	if err != nil {
		return nil, fmt.Errorf("filter: %w", err)
	}

	// Ensure chapters from disk are preferred
	if opts.Disk != "" {
		chapters = chapters.SortBy(func(a md.ChapterInfo, b md.ChapterInfo) bool {
			return a.GroupNames.String() == "Filesystem" && b.GroupNames.String() != "Filesystem"
		})
	}

	return filter.RemoveDuplicates(chapters), nil
}

func getCovers(
	ctx context.Context,
	src contentSource,
	opts Options,
	rep Reporter,
	manga *md.Manga,
) (md.ImageList, error) {
	p := rep.Task("Covers", true)
	covers, err := src.Covers(ctx, manga, p)
	if err != nil {
		p.Cancel("Error")
		return nil, fmt.Errorf("%v: %w", src.Name(), err)
	}
	p.Done()

	// Covers from disk should automatically be preferred, because
	// they appear later in the list and thus should override the
	// earlier downloaded covers.
	if opts.Disk != "" {
		p := rep.Task("Disk...", true)
		diskCovers, err := disk.LoadCovers(opts.Disk, p)
		if err != nil {
			p.Cancel("Error")
			return nil, fmt.Errorf("disk: %w", err)
		}
		p.Done()
		covers = append(covers, diskCovers...)
	}

	return covers, nil
}

func getPages(
	ctx context.Context,
	src contentSource,
	opts Options,
	_ Reporter,
	volume md.Volume,
	p Progress,
) (md.ImageList, error) {
	remotePages, err := src.Pages(ctx, volume.Sorted().FilterBy(func(ci md.ChapterInfo) bool {
		return ci.GroupNames.String() != "Filesystem"
	}), p)
	if err != nil {
		p.Cancel("Error")
		return nil, fmt.Errorf("%v: %w", src.Name(), err)
	}
	diskPages, err := disk.LoadPages(volume.Sorted().FilterBy(func(ci md.ChapterInfo) bool {
		return ci.GroupNames.String() == "Filesystem"
	}), p)
	if err != nil {
		p.Cancel("Error")
		return nil, fmt.Errorf("disk: %w", err)
	}
	p.Done()

	return append(remotePages, diskPages...), nil
}

func filterAndSort(cl md.ChapterList, opts Options) (md.ChapterList, error) {
	if opts.Language != "" {
		cl = filter.FilterByLanguage(cl, language.Make(opts.Language))
	}
	if opts.Groups != "" {
		cl = filter.FilterByRegex(cl, "GroupNames", opts.Groups)
	}
	if opts.Volumes != "" {
		cl = filter.FilterByIdentifier(cl, "VolumeIdentifier", filter.ParseRanges(opts.Volumes))
	}
	if opts.Chapters != "" {
		cl = filter.FilterByIdentifier(cl, "Identifier", filter.ParseRanges(opts.Chapters))
	}
	if len(opts.ChapterIDs) > 0 {
		wanted := make(map[string]bool, len(opts.ChapterIDs))
		for _, id := range opts.ChapterIDs {
			wanted[id] = true
		}
		cl = cl.FilterBy(func(ci md.ChapterInfo) bool { return wanted[ci.ID] })
	}

	switch opts.Rank {
	case "newest":
		cl = filter.SortByNewest(cl)
	case "newest-total":
		cl = filter.SortByNewestGroup(cl)
	case "most", "":
		cl = filter.SortByMost(cl)
	default:
		return nil, fmt.Errorf(`not a valid ranking algorithm: "%v"`, opts.Rank)
	}

	return cl, nil
}

// volumeWriter abstracts over the supported output formats.
type volumeWriter interface {
	Has(md.Identifier) bool
	Write(id md.Identifier, manga md.Manga, volume md.Volume, p Progress) error
}

type mobiWriter struct {
	opts Options
	dir  kindle.NormalizedDirectory
}

func newMOBIWriter(opts Options, out, title string) *mobiWriter {
	return &mobiWriter{opts, kindle.NewNormalizedDirectory(out, title, opts.KindleFolderMode)}
}

func (w *mobiWriter) Has(id md.Identifier) bool {
	return w.dir.Has(id)
}

func (w *mobiWriter) Write(id md.Identifier, manga md.Manga, volume md.Volume, p Progress) error {
	book := kindle.GenerateMOBI(manga, w.opts.Widepage, w.opts.Autocrop, w.opts.LeftToRight)
	book.RightToLeft = !w.opts.LeftToRight
	book.Title = fmt.Sprintf("%v: %v",
		manga.Info.Title,
		volume.Info.Identifier.StringFilled(w.opts.FillVolumeNumber, 0, false),
	)

	return w.dir.Write(id, book, p)
}

type cbzWriter struct {
	opts Options
	dir  cbz.Directory
}

func newCBZWriter(opts Options, out, title string) *cbzWriter {
	return &cbzWriter{opts, cbz.NewDirectory(out, title)}
}

func (w *cbzWriter) Has(id md.Identifier) bool {
	return w.dir.Has(id)
}

func (w *cbzWriter) Write(id md.Identifier, manga md.Manga, _ md.Volume, p Progress) error {
	return w.dir.Write(id, manga, cbz.Options{
		Process: func(img image.Image) []image.Image {
			return kindle.CropAndSplit(img, w.opts.Widepage, w.opts.Autocrop, w.opts.LeftToRight)
		},
		RightToLeft: !w.opts.LeftToRight,
		JPEGQuality: w.opts.JPEGQuality,
		Lossless:    w.opts.Lossless,
	}, p)
}
