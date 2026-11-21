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

// EditPages changes some of the pictures of an archive and leaves everything
// else (the other pictures, ComicInfo.xml) byte for byte as it was. Pictures
// are counted from 0 in the order of their names. For each picture want says
// whether to edit it; those are decoded, passed to edit and written again as
// JPEG (quality 92) or, if they were PNG, PNG. It returns how many were edited.
// The file is replaced only after the new archive was written completely.
func EditPages(path string, want func(index int) bool, edit func(index int, img image.Image) image.Image) (int, error) {
	return Rewrite(path, nil, want, edit)
}

// Rewrite is EditPages that can also drop pictures. Dropped pictures are left
// out and the pages after them are renumbered; ComicInfo.xml's page list is
// updated to match. It returns how many pictures were edited or dropped.
func Rewrite(path string, drop func(index int) bool, want func(index int) bool, edit func(index int, img image.Image) image.Image) (int, error) {
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

	edited, err := writeEdited(tmp, zr.File, indexOf, drop, want, edit)
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
	drop func(int) bool,
	want func(int) bool,
	edit func(int, image.Image) image.Image,
) (int, error) {
	zw := zip.NewWriter(w)
	edited := 0

	// Pages after a dropped one move up: work out the new numbers first.
	newIndex := map[int]int{}
	dropped := 0
	for i := 0; i < len(indexOf); i++ {
		if drop != nil && drop(i) {
			dropped++
			continue
		}
		newIndex[i] = i - dropped
	}
	renumber := dropped > 0
	if renumber {
		edited += dropped
	}
	pageName := func(f *zip.File, index int) string {
		if !renumber {
			return f.Name
		}

		return fmt.Sprintf("%05d%s", newIndex[index], strings.ToLower(filepath.Ext(f.Name)))
	}

	for _, f := range files {
		index, isPage := indexOf[f.Name]
		if isPage && drop != nil && drop(index) {
			continue
		}
		if renumber && strings.EqualFold(f.Name, "ComicInfo.xml") {
			if err := writeRenumberedInfo(zw, f, newIndex); err != nil {
				return 0, err
			}
			continue
		}
		if !isPage || !want(index) {
			if !renumber || !isPage {
				if err := zw.Copy(f); err != nil {
					return 0, fmt.Errorf("%v: %w", f.Name, err)
				}
				continue
			}
			header := f.FileHeader
			header.Name = pageName(f, index)
			raw, err := f.OpenRaw()
			if err != nil {
				return 0, fmt.Errorf("%v: %w", f.Name, err)
			}
			fw, err := zw.CreateRaw(&header)
			if err != nil {
				return 0, err
			}
			if _, err := io.Copy(fw, raw); err != nil {
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

		name := pageName(f, index)
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

// writeRenumberedInfo copies ComicInfo.xml with its page list following the
// pages' new numbers; pictures that were dropped leave the list.
func writeRenumberedInfo(zw *zip.Writer, f *zip.File, newIndex map[int]int) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	var info comicBookInfo
	err = xml.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&info)
	rc.Close() //nolint:errcheck
	if err != nil {
		return zw.Copy(f)
	}
	var pages []comicPage
	for _, p := range info.Pages {
		if n, ok := newIndex[p.Image]; ok {
			pages = append(pages, comicPage{Image: n, Type: p.Type})
		}
	}
	info.Pages = pages
	info.PageCount = len(newIndex)
	data, err := xml.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	fw, err := zw.Create("ComicInfo.xml")
	if err != nil {
		return err
	}
	_, err = fw.Write(append([]byte(xml.Header), data...))

	return err
}
