// Package cbz writes manga volumes as comic book archives (CBZ).
package cbz

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/leotaku/kojirou/cmd/formats"
	md "github.com/leotaku/kojirou/mangadex"
)

// DefaultJPEGQuality is used when Options.JPEGQuality is not set.
const DefaultJPEGQuality = 90

// Processor transforms a single page into zero or more output pages,
// e.g. by cropping or splitting it.
type Processor func(image.Image) []image.Image

type Options struct {
	Process     Processor
	RightToLeft bool

	// JPEGQuality is the JPEG quality from 1 to 100, or 0 for the default.
	JPEGQuality int
	// Lossless stores pages as PNG instead of JPEG, ignoring JPEGQuality.
	Lossless bool
}

type Directory struct {
	bookDirectory string
}

func NewDirectory(target, title string) Directory {
	if target == "" {
		target = pathnameFromTitle(title)
	}

	return Directory{bookDirectory: target}
}

func (d *Directory) Has(identifier md.Identifier) bool {
	_, err := os.Stat(filepath.Join(d.bookDirectory, filename(identifier)))
	return !errors.Is(err, fs.ErrNotExist) && err == nil
}

func (d *Directory) Write(
	identifier md.Identifier,
	manga md.Manga,
	opts Options,
	p formats.Progress,
) error {
	pathname := filepath.Join(d.bookDirectory, filename(identifier))
	if err := os.MkdirAll(filepath.Dir(pathname), os.ModePerm); err != nil {
		return fmt.Errorf("directory: %w", err)
	}
	f, err := os.Create(pathname)
	if err != nil {
		return fmt.Errorf("file: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if err := Generate(p.NewProxyWriter(f), manga, opts); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return f.Close()
}

// Generate writes all volumes of manga, in order, into a single archive.
// Pages are numbered consecutively so that any reader sorts them correctly.
func Generate(w io.Writer, manga md.Manga, opts Options) error {
	if opts.Process == nil {
		opts.Process = func(img image.Image) []image.Image { return []image.Image{img} }
	}

	quality := opts.JPEGQuality
	if quality == 0 {
		quality = DefaultJPEGQuality
	}
	if quality < 1 || quality > 100 {
		return fmt.Errorf("invalid JPEG quality: %d", quality)
	}
	ext, encode := ".jpg", func(w io.Writer, img image.Image) error {
		return jpeg.Encode(w, img, &jpeg.Options{Quality: quality})
	}
	if opts.Lossless {
		ext, encode = ".png", png.Encode
	}

	zw := zip.NewWriter(w)
	pages := make([]comicPage, 0)
	index := 0
	add := func(img image.Image, typ string) error {
		// Encoded image data is already compressed, so deflating it again is wasted work.
		fw, err := zw.CreateHeader(&zip.FileHeader{
			Name:   fmt.Sprintf("%05d%s", index, ext),
			Method: zip.Store,
		})
		if err != nil {
			return err
		}
		if err := encode(fw, img); err != nil {
			return err
		}
		pages = append(pages, comicPage{Image: index, Type: typ})
		index++
		return nil
	}

	for _, vol := range manga.Sorted() {
		if vol.Cover != nil {
			if err := add(vol.Cover, "FrontCover"); err != nil {
				return fmt.Errorf("cover: %w", err)
			}
		}
		for _, chap := range vol.Sorted() {
			for _, img := range chap.Sorted() {
				for _, out := range opts.Process(img) {
					if err := add(out, ""); err != nil {
						return fmt.Errorf("chapter %v: %w", chap.Info.Identifier, err)
					}
				}
			}
		}
	}

	info, err := xml.MarshalIndent(comicInfo(manga, pages, opts.RightToLeft), "", "  ")
	if err != nil {
		return fmt.Errorf("comicinfo: %w", err)
	}
	fw, err := zw.Create("ComicInfo.xml")
	if err != nil {
		return fmt.Errorf("comicinfo: %w", err)
	}
	if _, err := fw.Write(append([]byte(xml.Header), info...)); err != nil {
		return fmt.Errorf("comicinfo: %w", err)
	}

	return zw.Close()
}

type comicPage struct {
	Image int    `xml:",attr"`
	Type  string `xml:",attr,omitempty"`
}

type comicBookInfo struct {
	XMLName     xml.Name `xml:"ComicInfo"`
	Series      string
	Number      string `xml:",omitempty"`
	Writer      string `xml:",omitempty"`
	Penciller   string `xml:",omitempty"`
	LanguageISO string `xml:",omitempty"`
	PageCount   int
	Manga       string
	Pages       []comicPage `xml:"Pages>Page"`
}

func comicInfo(manga md.Manga, pages []comicPage, rtl bool) comicBookInfo {
	info := comicBookInfo{
		Series:    manga.Info.Title,
		Writer:    strings.Join(manga.Info.Authors, ", "),
		Penciller: strings.Join(manga.Info.Artists, ", "),
		PageCount: len(pages),
		Manga:     "Yes",
		Pages:     pages,
	}
	if rtl {
		info.Manga = "YesAndRightToLeft"
	}

	keys := make([]string, 0)
	for _, key := range manga.Keys() {
		keys = append(keys, key.String())
	}
	info.Number = strings.Join(keys, ", ")

	if chapters := manga.Chapters(); len(chapters) > 0 {
		if tag, _ := chapters[0].Info.Language.Base(); tag.String() != "und" {
			info.LanguageISO = tag.String()
		}
	}

	return info
}

func filename(identifier md.Identifier) string {
	return identifier.StringFilled(4, 2, false) + ".cbz"
}

func pathnameFromTitle(title string) string {
	return strings.ReplaceAll(title, "/", "／")
}
