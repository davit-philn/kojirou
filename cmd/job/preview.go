package job

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sort"
)

// PreviewPage is a page as downloaded and as it will be written.
type PreviewPage struct {
	Before image.Image
	After  image.Image
}

// PreviewChapter downloads one chapter and shows how its first pages come out
// with the options' page settings (device, fit mode, enhancing, wide pages),
// without writing anything. n limits the pages returned.
func PreviewChapter(ctx context.Context, opts Options, chapterID string, n int) ([]PreviewPage, error) {
	if chapterID == "" {
		return nil, errors.New("no chapter chosen")
	}
	opts.ChapterIDs = []string{chapterID}
	if err := opts.validate(); err != nil {
		return nil, err
	}

	src := newContentSource(opts)
	manga, err := inspect(ctx, src, opts, NopReporter{})
	if err != nil {
		return nil, err
	}
	chapters := manga.Chapters()
	if len(chapters) == 0 {
		return nil, errors.New("the chapter was not found")
	}
	images, err := src.Pages(ctx, chapters[:1], nopProgress{})
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	sort.SliceStable(images, func(i, j int) bool { return images[i].ImageIdentifier < images[j].ImageIdentifier })

	pageOpts := opts.pageOptions()
	var pages []PreviewPage
	for _, img := range images {
		if len(pages) >= n {
			break
		}
		// A page that is split gives several; show the first.
		out := processFirst(img.Image, pageOpts)
		pages = append(pages, PreviewPage{Before: img.Image, After: out})
	}
	if len(pages) == 0 {
		return nil, errors.New("the chapter has no pages")
	}

	return pages, nil
}
