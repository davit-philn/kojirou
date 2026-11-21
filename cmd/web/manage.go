package web

import (
	"bytes"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leotaku/kojirou/cmd/adstrip"
	"github.com/leotaku/kojirou/cmd/formats/cbz"
	"github.com/leotaku/kojirou/cmd/job"
)

// backupDir holds, inside the library, each file as it was before the first
// edit that removed or changed pages.
const backupDir = ".backup"

// findSeries returns the files of a series of the library, if there is one.
func (s *Server) findSeries(name string) ([]LibraryFile, bool) {
	list, err := scanLibrary(s.cfg.LibraryDir)
	if err != nil {
		return nil, false
	}
	for _, series := range list {
		if series.Name == name {
			return series.Files, true
		}
	}

	return nil, false
}

// busy reports whether a download may still be writing the file.
func (s *Server) busy(path string) bool {
	info, err := os.Stat(path)

	return err != nil || (time.Since(info.ModTime()) < recentlyWritten && !s.writtenByUs(path, info.ModTime()))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".copy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), dst)
}

// backUp keeps a file as it is, unless an earlier backup of it exists: the
// backup is always the file as it was before any edit.
func (s *Server) backUp(series, file string) error {
	dst := filepath.Join(s.cfg.LibraryDir, backupDir, series, file)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	return copyFile(filepath.Join(s.cfg.LibraryDir, series, file), dst)
}

type manageRequest struct {
	Series string `json:"series"`
	// File is empty to mean every file of the series.
	File string `json:"file"`
	// Action is "restore" or "discard" for /api/library/backup.
	Action string `json:"action"`
}

type manageResult struct {
	Done  int      `json:"done"`
	Bytes int64    `json:"bytes"`
	Notes []string `json:"notes"`
}

func (s *Server) selectFiles(req manageRequest) ([]LibraryFile, error) {
	files, ok := s.findSeries(req.Series)
	if !ok {
		return nil, errors.New("no such series in the library")
	}
	if req.File == "" {
		return files, nil
	}
	for _, f := range files {
		if f.Name == req.File {
			return []LibraryFile{f}, nil
		}
	}

	return nil, errors.New("no such file in the series")
}

// handleLibraryDelete removes a file, or a whole series, from the library.
func (s *Server) handleLibraryDelete(w http.ResponseWriter, r *http.Request) {
	var req manageRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	files, err := s.selectFiles(req)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	res := manageResult{Notes: []string{}}
	for _, f := range files {
		path := filepath.Join(s.cfg.LibraryDir, req.Series, f.Name)
		if err := os.Remove(path); err != nil {
			res.Notes = append(res.Notes, f.Name+": "+err.Error())
			continue
		}
		_ = os.Remove(filepath.Join(s.cfg.LibraryDir, backupDir, req.Series, f.Name))
		res.Done++
		res.Bytes += f.Size
	}
	// Leave no empty folder behind (only if nothing else is in it).
	_ = os.Remove(filepath.Join(s.cfg.LibraryDir, req.Series))
	_ = os.Remove(filepath.Join(s.cfg.LibraryDir, backupDir, req.Series))
	writeJSON(w, http.StatusOK, res)
}

// handleLibraryBackup restores files from their backup, or throws the backup away.
func (s *Server) handleLibraryBackup(w http.ResponseWriter, r *http.Request) {
	var req manageRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Action != "restore" && req.Action != "discard" {
		writeError(w, http.StatusBadRequest, errors.New("action must be restore or discard"))
		return
	}
	files, err := s.selectFiles(req)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	res := manageResult{Notes: []string{}}
	for _, f := range files {
		if !f.HasBackup {
			continue
		}
		backup := filepath.Join(s.cfg.LibraryDir, backupDir, req.Series, f.Name)
		path := filepath.Join(s.cfg.LibraryDir, req.Series, f.Name)
		if req.Action == "restore" {
			if s.busy(path) {
				res.Notes = append(res.Notes, f.Name+": skipped (it is being written)")
				continue
			}
			if err := copyFile(backup, path); err != nil {
				res.Notes = append(res.Notes, f.Name+": "+err.Error())
				continue
			}
			if info, err := os.Stat(path); err == nil {
				s.rememberWrite(path, info.ModTime())
			}
		}
		if err := os.Remove(backup); err != nil {
			res.Notes = append(res.Notes, f.Name+": "+err.Error())
			continue
		}
		res.Done++
		res.Bytes += f.Size
	}
	_ = os.Remove(filepath.Join(s.cfg.LibraryDir, backupDir, req.Series))
	writeJSON(w, http.StatusOK, res)
}

type renameRequest struct {
	Series string `json:"series"`
	File   string `json:"file"`
	// Name is the new name of the series, or of the file when File is set.
	Name string `json:"name"`
}

// handleLibraryRename renames a series folder or one file of it.
func (s *Server) handleLibraryRename(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	files, ok := s.findSeries(req.Series)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such series in the library"))
		return
	}
	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	lib := s.cfg.LibraryDir
	if req.File == "" {
		name := job.SafeDirName(req.Name)
		if name == req.Series {
			writeJSON(w, http.StatusOK, map[string]string{"name": name})
			return
		}
		if strings.HasPrefix(name, ".") {
			writeError(w, http.StatusBadRequest, errors.New("the name cannot start with a dot"))
			return
		}
		if _, err := os.Stat(filepath.Join(lib, name)); err == nil {
			writeError(w, http.StatusConflict, errors.New("a series with this name exists already"))
			return
		}
		for _, f := range files {
			if s.busy(filepath.Join(lib, req.Series, f.Name)) {
				writeError(w, http.StatusConflict, errors.New(f.Name+" is being written; try again in a moment"))
				return
			}
		}
		if err := os.Rename(filepath.Join(lib, req.Series), filepath.Join(lib, name)); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = os.Rename(filepath.Join(lib, backupDir, req.Series), filepath.Join(lib, backupDir, name))
		// Its converted copies ("<name> [kindle-pw3]") follow, so that they stay together.
		if all, err := scanLibrary(lib); err == nil && !isConvertedName(req.Series) {
			for _, other := range all {
				if isConvertedName(other.Name) && strings.HasPrefix(other.Name, req.Series+" [") {
					renamed := name + strings.TrimPrefix(other.Name, req.Series)
					if _, err := os.Stat(filepath.Join(lib, renamed)); err != nil {
						_ = os.Rename(filepath.Join(lib, other.Name), filepath.Join(lib, renamed))
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": name})
		return
	}

	found := false
	for _, f := range files {
		found = found || f.Name == req.File
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("no such file in the series"))
		return
	}
	ext := filepath.Ext(req.File)
	name := strings.TrimSpace(req.Name)
	if !strings.EqualFold(filepath.Ext(name), ext) {
		name += ext
	}
	if name == ext || strings.ContainsAny(name, `/\:*?"<>|`) || strings.HasPrefix(name, ".") || len(name) > 150 {
		writeError(w, http.StatusBadRequest, errors.New("this is not a valid file name"))
		return
	}
	from, to := filepath.Join(lib, req.Series, req.File), filepath.Join(lib, req.Series, name)
	if name != req.File {
		if _, err := os.Stat(to); err == nil {
			writeError(w, http.StatusConflict, errors.New("a file with this name exists already"))
			return
		}
		if s.busy(from) {
			writeError(w, http.StatusConflict, errors.New("the file is being written; try again in a moment"))
			return
		}
		if err := os.Rename(from, to); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = os.Rename(filepath.Join(lib, backupDir, req.Series, req.File), filepath.Join(lib, backupDir, req.Series, name))
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// pageCount counts the pictures of a CBZ the way the page editing does.
func pageCount(path string) (int, error) {
	n := 0
	err := eachPageName(path, func(string) { n++ })

	return n, err
}

// handleLibraryPages answers how many pages a CBZ has and which is its cover.
func (s *Server) handleLibraryPages(w http.ResponseWriter, r *http.Request) {
	path, ok := libraryFilePath(s.cfg.LibraryDir, r.PathValue("series"), r.PathValue("file"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	n, err := pageCount(path)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n, "cover": cbz.CoverIndex(path)})
}

// handleLibraryPage serves one page of a CBZ as a small JPEG.
func (s *Server) handleLibraryPage(w http.ResponseWriter, r *http.Request) {
	path, ok := libraryFilePath(s.cfg.LibraryDir, r.PathValue("series"), r.PathValue("file"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 {
		http.NotFound(w, r)
		return
	}
	width := 240
	if v, err := strconv.Atoi(r.URL.Query().Get("w")); err == nil && v >= 80 && v <= 1600 {
		width = v
	}
	img, err := readPage(path, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaleToWidth(img, width), &jpeg.Options{Quality: 75}); err != nil {
		http.Error(w, "cannot encode", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-cache")
	_, _ = w.Write(buf.Bytes())
}

type removePagesRequest struct {
	Series string `json:"series"`
	File   string `json:"file"`
	// Pages are counted from 1.
	Pages []int `json:"pages"`
	// Remember makes every page of the series that looks like these go too.
	Remember bool `json:"remember"`
}

type removePagesResult struct {
	Removed int `json:"removed"`
	// Also counts the look-alike pages removed from other files.
	Also  int      `json:"also"`
	Notes []string `json:"notes"`
}

// handleLibraryRemovePages deletes chosen pages of a CBZ (after backing it up).
func (s *Server) handleLibraryRemovePages(w http.ResponseWriter, r *http.Request) {
	var req removePagesRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	files, ok := s.findSeries(req.Series)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such series in the library"))
		return
	}
	known := false
	for _, f := range files {
		known = known || (f.Name == req.File && f.Format == "cbz")
	}
	if !known || len(req.Pages) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("choose a CBZ file and at least one page"))
		return
	}
	path := filepath.Join(s.cfg.LibraryDir, req.Series, req.File)
	n, err := pageCount(path)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	drop := map[int]bool{}
	for _, p := range req.Pages {
		if p < 1 || p > n {
			writeError(w, http.StatusBadRequest, fmt.Errorf("page %d does not exist (the file has %d)", p, n))
			return
		}
		drop[p-1] = true
	}
	if len(drop) >= n {
		writeError(w, http.StatusBadRequest, errors.New("that would remove every page"))
		return
	}

	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	if s.busy(path) {
		writeError(w, http.StatusConflict, errors.New("the file is being written; try again in a moment"))
		return
	}
	res := removePagesResult{Notes: []string{}}
	if req.Remember {
		var strips []adstrip.Strip
		for i := range drop {
			if img, err := readPage(path, i); err == nil {
				strips = append(strips, adstrip.WholePage(adstrip.Analyze(img)))
			}
		}
		if err := s.ads.Add("series:"+req.Series, strips); err != nil {
			res.Notes = append(res.Notes, "could not remember the pages: "+err.Error())
		}
	}
	if err := s.backUp(req.Series, req.File); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("backup: %w", err))
		return
	}
	if _, err := cbz.Rewrite(path, func(i int) bool { return drop[i] }, func(int) bool { return false }, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if info, err := os.Stat(path); err == nil {
		s.rememberWrite(path, info.ModTime())
	}
	res.Removed = len(drop)
	writeJSON(w, http.StatusOK, res)
}

type exportRequest struct {
	Series string   `json:"series"`
	Files  []string `json:"files"`
	// Dest is an existing folder, for example the documents folder of a Kindle.
	Dest string `json:"dest"`
}

// handleLibraryExport copies files of a series into a folder (a Kindle's, say).
func (s *Server) handleLibraryExport(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	files, ok := s.findSeries(req.Series)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such series in the library"))
		return
	}
	dest := strings.TrimSpace(req.Dest)
	if !filepath.IsAbs(dest) {
		writeError(w, http.StatusBadRequest, errors.New("enter the full path of the folder, for example E:\\documents"))
		return
	}
	if info, err := os.Stat(dest); err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("this folder does not exist (is the reader connected?)"))
		return
	}
	want := map[string]bool{}
	for _, f := range req.Files {
		want[f] = true
	}
	// Each series gets a folder of its own, so that files named 0001.cbz of
	// different series do not overwrite each other.
	target := filepath.Join(dest, job.SafeDirName(req.Series))
	res := manageResult{Notes: []string{}}
	var names []string
	for _, f := range files {
		if len(want) == 0 || want[f.Name] {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := copyFile(filepath.Join(s.cfg.LibraryDir, req.Series, name), filepath.Join(target, name)); err != nil {
			res.Notes = append(res.Notes, name+": "+err.Error())
			continue
		}
		res.Done++
	}
	writeJSON(w, http.StatusOK, map[string]any{"done": res.Done, "folder": target, "notes": res.Notes})
}
