package cbz

import (
	"archive/zip"
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

// EditPages changes some of the pictures of an archive and leaves everything
// else (the other pictures, ComicInfo.xml) byte for byte as it was. Pictures
// are counted from 0 in the order of their names. For each picture want says
// whether to edit it; those are decoded, passed to edit and written again as
// JPEG (quality 92) or, if they were PNG, PNG. It returns how many were edited.
// The file is replaced only after the new archive was written completely.
func EditPages(path string, want func(index int) bool, edit func(index int, img image.Image) image.Image) (int, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, fmt.Errorf("open: %w", err)
	}
	defer zr.Close() //nolint:errcheck

	var pages []*zip.File
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && imageExts[strings.ToLower(filepath.Ext(f.Name))] {
			pages = append(pages, f)
		}
	}
	if len(pages) == 0 {
		return 0, errors.New("the archive has no pictures")
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })
	indexOf := make(map[string]int, len(pages))
	for i, f := range pages {
		indexOf[f.Name] = i
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".edit-*.cbz")
	if err != nil {
		return 0, fmt.Errorf("temporary file: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck

	edited, err := writeEdited(tmp, zr.File, indexOf, want, edit)
	if err != nil {
		tmp.Close() //nolint:errcheck
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if edited == 0 {
		return 0, nil
	}
	if err := zr.Close(); err != nil {
		return 0, err
	}

	return edited, os.Rename(tmp.Name(), path)
}

func writeEdited(
	w io.Writer,
	files []*zip.File,
	indexOf map[string]int,
	want func(int) bool,
	edit func(int, image.Image) image.Image,
) (int, error) {
	zw := zip.NewWriter(w)
	edited := 0
	for _, f := range files {
		index, isPage := indexOf[f.Name]
		if !isPage || !want(index) {
			if err := zw.Copy(f); err != nil {
				return 0, fmt.Errorf("%v: %w", f.Name, err)
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return 0, fmt.Errorf("%v: %w", f.Name, err)
		}
		img, _, err := image.Decode(io.LimitReader(rc, 64<<20))
		rc.Close() //nolint:errcheck
		if err != nil {
			return 0, fmt.Errorf("%v: %w", f.Name, err)
		}

		name := f.Name
		encode := func(w io.Writer, img image.Image) error { return jpeg.Encode(w, img, &jpeg.Options{Quality: 92}) }
		if strings.EqualFold(filepath.Ext(name), ".png") || strings.EqualFold(filepath.Ext(name), ".gif") {
			name = strings.TrimSuffix(name, filepath.Ext(name)) + ".png"
			encode = png.Encode
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return 0, err
		}
		if err := encode(fw, edit(index, img)); err != nil {
			return 0, fmt.Errorf("%v: %w", f.Name, err)
		}
		edited++
	}

	return edited, zw.Close()
}
