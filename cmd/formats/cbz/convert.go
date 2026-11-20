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
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConvertOptions says how the pictures of an archive are prepared again.
type ConvertOptions struct {
	// Process prepares a page and may return several (a split page) or none.
	Process Processor
	// Cover prepares the front cover; nil leaves it as it is.
	Cover func(image.Image) image.Image
	// JPEGQuality is 1 to 100, or 0 for the default; Lossless writes PNG.
	JPEGQuality int
	Lossless    bool
}

// Convert writes a copy of a CBZ whose pictures went through the options,
// keeping its metadata (the page list is updated). The original is not touched.
func Convert(srcPath, dstPath string, o ConvertOptions) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer zr.Close() //nolint:errcheck

	var pages []*zip.File
	var infoFile *zip.File
	for _, f := range zr.File {
		switch {
		case f.FileInfo().IsDir():
		case strings.EqualFold(f.Name, "ComicInfo.xml"):
			infoFile = f
		case imageExts[strings.ToLower(filepath.Ext(f.Name))]:
			pages = append(pages, f)
		}
	}
	if len(pages) == 0 {
		return errors.New("the archive has no pictures")
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })

	var info comicBookInfo
	if infoFile != nil {
		if rc, err := infoFile.Open(); err == nil {
			_ = xml.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&info)
			rc.Close() //nolint:errcheck
		}
	}
	types := map[int]string{}
	for _, p := range info.Pages {
		types[p.Image] = p.Type
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), os.ModePerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), ".convert-*.cbz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if err := writeConverted(tmp, pages, types, &info, o); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), dstPath)
}

func writeConverted(w io.Writer, pages []*zip.File, types map[int]string, info *comicBookInfo, o ConvertOptions) error {
	if o.Process == nil {
		o.Process = func(img image.Image) []image.Image { return []image.Image{img} }
	}
	quality := o.JPEGQuality
	if quality == 0 {
		quality = DefaultJPEGQuality
	}
	if quality < 1 || quality > 100 {
		return fmt.Errorf("invalid JPEG quality: %d", quality)
	}
	ext := ".jpg"
	encode := func(w io.Writer, img image.Image) error { return jpeg.Encode(w, img, &jpeg.Options{Quality: quality}) }
	if o.Lossless {
		ext, encode = ".png", png.Encode
	}

	zw := zip.NewWriter(w)
	var out []comicPage
	for oldIndex, f := range pages {
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%v: %w", f.Name, err)
		}
		img, _, err := image.Decode(io.LimitReader(rc, 64<<20))
		rc.Close() //nolint:errcheck
		if err != nil {
			return fmt.Errorf("%v: %w", f.Name, err)
		}

		results := []image.Image{img}
		kind := types[oldIndex]
		if kind == "FrontCover" {
			if o.Cover != nil {
				results = []image.Image{o.Cover(img)}
			}
		} else {
			results = o.Process(img)
		}
		for i, res := range results {
			fw, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%05d%s", len(out), ext), Method: zip.Store})
			if err != nil {
				return err
			}
			if err := encode(fw, res); err != nil {
				return fmt.Errorf("%v: %w", f.Name, err)
			}
			pageType := ""
			if i == 0 {
				pageType = kind
			}
			out = append(out, comicPage{Image: len(out), Type: pageType})
		}
	}

	info.Pages = out
	info.PageCount = len(out)
	data, err := xml.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("comicinfo: %w", err)
	}
	fw, err := zw.Create("ComicInfo.xml")
	if err != nil {
		return err
	}
	if _, err := fw.Write(append([]byte(xml.Header), data...)); err != nil {
		return err
	}

	return zw.Close()
}
