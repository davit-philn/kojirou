package cbz

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CoverUpdate describes a new front cover for an existing archive.
type CoverUpdate struct {
	// Picture is the encoded cover (JPEG or PNG) and Ext its extension,
	// including the dot.
	Picture []byte
	Ext     string
	// Series and Volume fill the matching ComicInfo.xml fields when the file
	// has none yet; Title is derived from them in the same way.
	Series string
	Volume string
}

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true}

// SetCover makes the given picture the first page of the archive, marked as
// the front cover in ComicInfo.xml. A front cover the archive already has is
// replaced; all other pages are kept as they are, in order. The file is
// replaced only after the new archive was written completely.
func SetCover(path string, upd CoverUpdate) error {
	if len(upd.Picture) == 0 || !imageExts[strings.ToLower(upd.Ext)] {
		return errors.New("the cover must be a JPEG, PNG or GIF picture")
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer zr.Close() //nolint:errcheck

	var pages, others []*zip.File
	var infoFile *zip.File
	for _, f := range zr.File {
		switch {
		case f.FileInfo().IsDir():
		case strings.EqualFold(f.Name, "ComicInfo.xml"):
			infoFile = f
		case imageExts[strings.ToLower(filepath.Ext(f.Name))]:
			pages = append(pages, f)
		default:
			others = append(others, f)
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
	types := map[int]string{} // old page index -> type
	oldCover := -1
	for _, p := range info.Pages {
		if p.Type == "FrontCover" && oldCover < 0 {
			oldCover = p.Image
			continue
		}
		if p.Type != "" {
			types[p.Image] = p.Type
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".cover-*.cbz")
	if err != nil {
		return fmt.Errorf("temporary file: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if err := writeWithCover(tmp, pages, others, &info, types, oldCover, upd); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := zr.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), path)
}

func writeWithCover(
	w io.Writer,
	pages, others []*zip.File,
	info *comicBookInfo,
	types map[int]string,
	oldCover int,
	upd CoverUpdate,
) error {
	zw := zip.NewWriter(w)
	index := 0
	add := func(name string, data io.Reader) error {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, data)

		return err
	}

	newPages := make([]comicPage, 0, len(pages)+1)
	if err := add(fmt.Sprintf("%05d%s", index, strings.ToLower(upd.Ext)), strings.NewReader(string(upd.Picture))); err != nil {
		return fmt.Errorf("cover: %w", err)
	}
	newPages = append(newPages, comicPage{Image: 0, Type: "FrontCover"})
	index++

	for oldIndex, f := range pages {
		if oldIndex == oldCover {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%v: %w", f.Name, err)
		}
		err = add(fmt.Sprintf("%05d%s", index, strings.ToLower(filepath.Ext(f.Name))), rc)
		rc.Close() //nolint:errcheck
		if err != nil {
			return fmt.Errorf("%v: %w", f.Name, err)
		}
		newPages = append(newPages, comicPage{Image: index, Type: types[oldIndex]})
		index++
	}

	for _, f := range others {
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%v: %w", f.Name, err)
		}
		err = add(f.Name, rc)
		rc.Close() //nolint:errcheck
		if err != nil {
			return fmt.Errorf("%v: %w", f.Name, err)
		}
	}

	info.Pages = newPages
	info.PageCount = len(newPages)
	if info.Series == "" {
		info.Series = upd.Series
	}
	if info.Volume == "" {
		info.Volume = upd.Volume
	}
	if info.Title == "" && info.Series != "" && info.Volume != "" {
		info.Title = info.Series + " - Vol. " + info.Volume
	}
	if info.Number == "" {
		info.Number = info.Volume
	}
	if info.Manga == "" {
		info.Manga = "Yes"
	}
	data, err := xml.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("comicinfo: %w", err)
	}
	if err := add("ComicInfo.xml", strings.NewReader(xml.Header+string(data))); err != nil {
		return fmt.Errorf("comicinfo: %w", err)
	}

	return zw.Close()
}
