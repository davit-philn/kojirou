package formats

import "context"

// Chapter is a single entry of a chapter list returned by a Source.
type Chapter struct {
	Title string
	URL   string
}

// Source extracts chapters and page images from a website.
type Source interface {
	FetchChapterList(ctx context.Context, url string) ([]Chapter, error)
	FetchImages(ctx context.Context, chapterURL string) ([]string, error)
}
