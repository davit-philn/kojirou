package web

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
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
	// MarkFile and MarkPage (counted from 1) point at a page that is an
	// advertisement as a whole; it is remembered so that every page that looks
	// like it is found.
	MarkFile   string `json:"markFile,omitempty"`
	MarkPage   int    `json:"markPage,omitempty"`
	ProgressID string `json:"progressId"`
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
	// Dropped is how many of those pages are advertisements as a whole and are
	// removed rather than cropped.
	Dropped int `json:"dropped"`
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

	if req.MarkFile != "" && req.MarkPage > 0 {
		if filepath.Base(req.MarkFile) != req.MarkFile {
			return nil, errors.New("bad file name")
		}
		img, err := readPage(filepath.Join(s.cfg.LibraryDir, req.Series, req.MarkFile), req.MarkPage-1)
		if err != nil {
			return nil, fmt.Errorf("page %d of %s: %w", req.MarkPage, req.MarkFile, err)
		}
		if err := s.ads.Add(key, []adstrip.Strip{adstrip.WholePage(adstrip.Analyze(img))}); err != nil {
			return nil, err
		}
	}

	report := s.progressFor(req.ProgressID)
	steps := len(files)
	if req.Apply {
		steps *= 2
	}

	// 1. Reduce every page of every file to what is needed to compare them.
	var refs []pageRef
	pageCounts := make([]int, len(files))
	analysed := make([]*adstrip.Page, 0)
	for fi, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report(fi, steps, "Đang quét "+f.Name)
		path := filepath.Join(s.cfg.LibraryDir, req.Series, f.Name)
		cover := cbz.CoverIndex(path)
		if err := eachPage(path, func(i int, img image.Image) {
			pageCounts[fi] = i + 1
			if img == nil || i == cover { // the cover is never taken for an advert
				return
			}
			info := adstrip.Analyze(img)
			info.Group = fi
			refs = append(refs, pageRef{file: fi, page: i, info: info})
			analysed = append(analysed, info)
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
			if cut.Drop {
				res.Dropped++
			}
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
			if !hits[fi][pages[0]].Drop {
				res.After, _ = dataURL(scaleToWidth(adstrip.Crop(img, hits[fi][pages[0]]), previewBeforeW))
			}
		}
	}

	if !req.Apply {
		report(steps, steps, "")
		return res, nil
	}

	// 5. Cut them off, rewriting only the pages that change.
	for n, fi := range fileIndexes {
		name := files[fi].Name
		report(len(files)+n*len(files)/max(len(fileIndexes), 1), steps, "Đang cắt "+name)
		path := filepath.Join(s.cfg.LibraryDir, req.Series, name)
		if info, err := os.Stat(path); err != nil || (time.Since(info.ModTime()) < recentlyWritten && !s.writtenByUs(path, info.ModTime())) {
			res.Notes = append(res.Notes, name+": skipped (it is being written)")
			continue
		}
		cuts := hits[fi]
		dropped := 0
		for _, c := range cuts {
			if c.Drop {
				dropped++
			}
		}
		// A real chapter has a few advertising pages at most; many more means a
		// look-alike was taken for one, and pages must not be lost over that.
		if dropped > 6 || dropped*3 > pageCounts[fi] {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: skipped, %d of %d pages would be removed, which is too many to be right", name, dropped, pageCounts[fi]))
			continue
		}
		_, err := cbz.Rewrite(path,
			func(i int) bool { return cuts[i].Drop },
			func(i int) bool { c, ok := cuts[i]; return ok && !c.Drop },
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
	report(steps, steps, "")

	return res, nil
}

// eachPage decodes the pictures of a CBZ in the order of their names, opening
// the archive once. A picture that cannot be decoded is passed as nil so that
// the numbering of the others stays right.
func eachPage(path string, f func(index int, img image.Image)) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close() //nolint:errcheck
	var pages []*zip.File
	for _, file := range zr.File {
		switch strings.ToLower(filepath.Ext(file.Name)) {
		case ".jpg", ".jpeg", ".png", ".gif":
			pages = append(pages, file)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })
	for n, file := range pages {
		f(n, decodeEntry(file))
	}

	return nil
}

func decodeEntry(file *zip.File) image.Image {
	if file.UncompressedSize64 > maxCoverEntry {
		return nil
	}
	rc, err := file.Open()
	if err != nil {
		return nil
	}
	defer rc.Close() //nolint:errcheck
	img, _, err := image.Decode(io.LimitReader(rc, maxCoverEntry))
	if err != nil {
		return nil
	}

	return img
}
