package web

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/leotaku/kojirou/cmd/adstrip"
	"github.com/leotaku/kojirou/cmd/formats/cbz"
)

const adsTimeout = 40 * time.Minute

// AdStore remembers the advertising strips found per source ("source:<name>")
// and per library series ("series:<name>") in a JSON file.
type AdStore struct {
	path string
	mu   sync.Mutex
}

func NewAdStore(path string) *AdStore { return &AdStore{path: path} }

func (s *AdStore) load() (map[string][]adstrip.Strip, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]adstrip.Strip{}, nil
	} else if err != nil {
		return nil, err
	}
	m := map[string][]adstrip.Strip{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	return m, nil
}

func (s *AdStore) Get(key string) []adstrip.Strip {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, _ := s.load()

	return m[key]
}

// Add remembers strips, leaving out those that look like one already known.
func (s *AdStore) Add(key string, strips []adstrip.Strip) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[key] = adstrip.Merge(m[key], strips)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".ads-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), s.path)
}

type adsRequest struct {
	Series string `json:"series"`
	// Apply cuts the strips; without it the library is only searched.
	Apply bool `json:"apply"`
}

type adsFile struct {
	Name  string `json:"name"`
	Pages int    `json:"pages"`
}

type adsResult struct {
	Series string    `json:"series"`
	Apply  bool      `json:"apply"`
	Files  []adsFile `json:"files"`
	Pages  int       `json:"pages"`
	// Strips is how many kinds of strip were found.
	Strips int `json:"strips"`
	// Before and After show the first page with a strip, before and after cutting.
	Before string   `json:"before,omitempty"`
	After  string   `json:"after,omitempty"`
	Notes  []string `json:"notes"`
}

type pageRef struct {
	file int
	page int
	info *adstrip.Page
}

// handleLibraryAds finds the advertising strips on the pages of a library
// series and, when asked, cuts them off.
func (s *Server) handleLibraryAds(w http.ResponseWriter, r *http.Request) {
	var req adsRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	list, err := scanLibrary(s.cfg.LibraryDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var files []LibraryFile
	found := false
	for _, series := range list {
		if series.Name == req.Series {
			for _, f := range series.Files {
				if f.Format == "cbz" {
					files = append(files, f)
				}
			}
			found = true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("no such series in the library"))
		return
	}

	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), adsTimeout)
	defer cancel()
	res, err := s.cleanSeriesAds(ctx, req, files)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cleanSeriesAds(ctx context.Context, req adsRequest, files []LibraryFile) (*adsResult, error) {
	res := &adsResult{Series: req.Series, Apply: req.Apply, Files: []adsFile{}, Notes: []string{}}
	key := "series:" + req.Series

	// 1. Reduce every page of every file to what is needed to compare them.
	var refs []pageRef
	analysed := make([]*adstrip.Page, 0)
	for fi, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(s.cfg.LibraryDir, req.Series, f.Name)
		i := 0
		if err := eachPage(path, func(img image.Image) {
			info := adstrip.Analyze(img)
			refs = append(refs, pageRef{file: fi, page: i, info: info})
			analysed = append(analysed, info)
			i++
		}); err != nil {
			res.Notes = append(res.Notes, f.Name+": "+err.Error())
		}
	}

	// 2. Strips seen on several pages, plus the ones remembered for this series.
	strips := adstrip.Merge(s.ads.Get(key), adstrip.Detect(analysed))
	res.Strips = len(strips)
	if len(strips) == 0 {
		return res, nil
	}

	// 3. Which pages carry one.
	hits := map[int]map[int]adstrip.Cut{} // file -> page -> cut
	for _, ref := range refs {
		if cut := adstrip.Find(ref.info, strips); !cut.Empty() {
			if hits[ref.file] == nil {
				hits[ref.file] = map[int]adstrip.Cut{}
			}
			hits[ref.file][ref.page] = cut
			res.Pages++
		}
	}
	var fileIndexes []int
	for fi := range hits {
		fileIndexes = append(fileIndexes, fi)
	}
	sort.Ints(fileIndexes)
	for _, fi := range fileIndexes {
		res.Files = append(res.Files, adsFile{Name: files[fi].Name, Pages: len(hits[fi])})
	}

	// 4. A sample of the first page that carries one.
	if len(fileIndexes) > 0 {
		fi := fileIndexes[0]
		var pages []int
		for p := range hits[fi] {
			pages = append(pages, p)
		}
		sort.Ints(pages)
		path := filepath.Join(s.cfg.LibraryDir, req.Series, files[fi].Name)
		if img, err := readPage(path, pages[0]); err == nil {
			res.Before, _ = dataURL(scaleToWidth(img, previewBeforeW))
			res.After, _ = dataURL(scaleToWidth(adstrip.Crop(img, hits[fi][pages[0]]), previewBeforeW))
		}
	}

	if !req.Apply {
		return res, nil
	}

	// 5. Cut them off, rewriting only the pages that change.
	for _, fi := range fileIndexes {
		name := files[fi].Name
		path := filepath.Join(s.cfg.LibraryDir, req.Series, name)
		if info, err := os.Stat(path); err != nil || (time.Since(info.ModTime()) < recentlyWritten && !s.writtenByUs(path, info.ModTime())) {
			res.Notes = append(res.Notes, name+": skipped (it is being written)")
			continue
		}
		cuts := hits[fi]
		_, err := cbz.EditPages(path,
			func(i int) bool { _, ok := cuts[i]; return ok },
			func(i int, img image.Image) image.Image { return adstrip.Crop(img, cuts[i]) })
		if err != nil {
			res.Notes = append(res.Notes, name+": "+err.Error())
			continue
		}
		if info, err := os.Stat(path); err == nil {
			s.rememberWrite(path, info.ModTime())
		}
	}
	_ = s.ads.Add(key, strips)

	return res, nil
}

// eachPage decodes the pictures of a CBZ in the order of their names.
func eachPage(path string, f func(image.Image)) error {
	n := 0
	for {
		img, err := readPage(path, n)
		if err != nil {
			if strings.Contains(err.Error(), "no such page") {
				return nil
			}
			return err
		}
		f(img)
		n++
	}
}
