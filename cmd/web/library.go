package web

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LibraryFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Format   string    `json:"format"`
	Modified time.Time `json:"modified"`
}

type LibrarySeries struct {
	Name  string        `json:"name"`
	Files []LibraryFile `json:"files"`
}

var libraryFormats = map[string]string{".cbz": "cbz", ".azw3": "mobi"}

// scanLibrary lists series directories and their e-book files. Symbolic links
// are ignored so that nothing outside the library can be exposed.
func scanLibrary(dir string) ([]LibrarySeries, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []LibrarySeries{}, nil
		}
		return nil, err
	}

	result := make([]LibrarySeries, 0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		series := LibrarySeries{Name: e.Name(), Files: []LibraryFile{}}
		for _, f := range files {
			format, ok := libraryFormats[strings.ToLower(filepath.Ext(f.Name()))]
			if !ok || !f.Type().IsRegular() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			series.Files = append(series.Files, LibraryFile{
				Name:     f.Name(),
				Size:     info.Size(),
				Format:   format,
				Modified: info.ModTime(),
			})
		}
		if len(series.Files) > 0 {
			sort.Slice(series.Files, func(i, j int) bool { return series.Files[i].Name < series.Files[j].Name })
			result = append(result, series)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

// libraryFilePath resolves a requested file only if the scan lists it, so
// request values are never used to build a path.
func libraryFilePath(dir, series, file string) (string, bool) {
	list, err := scanLibrary(dir)
	if err != nil {
		return "", false
	}
	for _, s := range list {
		if s.Name != series {
			continue
		}
		for _, f := range s.Files {
			if f.Name == file {
				return filepath.Join(dir, s.Name, f.Name), true
			}
		}
	}

	return "", false
}
