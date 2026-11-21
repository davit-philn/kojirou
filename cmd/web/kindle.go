package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leotaku/kojirou/cmd/device"
	"github.com/leotaku/kojirou/cmd/formats/cbz"
	"github.com/leotaku/kojirou/cmd/formats/kindle"
	"github.com/leotaku/kojirou/cmd/job"
)

const (
	previewPages   = 4
	previewTimeout = 3 * time.Minute
	convertTimeout = 30 * time.Minute
	previewBeforeW = 360
	previewAfterW  = 536
)

// pageSettings are the settings that decide how a page comes out.
type pageSettings struct {
	Device      string `json:"device"`
	FitMode     string `json:"fitMode"`
	Enhance     bool   `json:"enhance"`
	Widepage    string `json:"widepage"`
	Autocrop    bool   `json:"autocrop"`
	RightToLeft *bool  `json:"rightToLeft"`
}

// options turns the settings into what the page code needs.
func (p pageSettings) options() (kindle.PageOptions, error) {
	dev, err := (job.Options{Device: p.Device, FitMode: p.FitMode, Enhance: p.Enhance}).DeviceOptions()
	if err != nil {
		return kindle.PageOptions{}, err
	}
	widepage, ok := widepagePolicies[p.Widepage]
	if !ok {
		return kindle.PageOptions{}, errors.New("invalid widepage option")
	}
	rtl := true
	if p.RightToLeft != nil {
		rtl = *p.RightToLeft
	}

	return kindle.PageOptions{Widepage: widepage, Autocrop: p.Autocrop, LTR: !rtl, Device: dev}, nil
}

type previewPageJSON struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// handlePreview downloads the first pages of one chapter and shows them as
// they will be written with the page settings of the download.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req downloadRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.ChapterIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("choose a chapter first"))
		return
	}
	req.ChapterIDs = req.ChapterIDs[:1]
	opts, err := s.optionsFromRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	opts.Interactive = true

	ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
	defer cancel()
	pages, err := job.PreviewChapter(ctx, opts, req.ChapterIDs[0], previewPages)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := make([]previewPageJSON, 0, len(pages))
	for _, p := range pages {
		before, err1 := dataURL(scaleToWidth(p.Before, previewBeforeW))
		after, err2 := dataURL(scaleToWidth(p.After, previewAfterW))
		if err1 != nil || err2 != nil {
			writeError(w, http.StatusInternalServerError, errors.New("could not encode a preview"))
			return
		}
		out = append(out, previewPageJSON{Before: before, After: after})
	}
	screen := device.Profile{}
	if dev, err := opts.DeviceOptions(); err == nil {
		screen = dev.Profile
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": out, "screen": screen})
}

func dataURL(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 78}); err != nil {
		return "", err
	}

	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// handleLibraryPreview shows one page of a library CBZ before or after the
// page settings in the query are applied.
func (s *Server) handleLibraryPreview(w http.ResponseWriter, r *http.Request) {
	series, file := r.PathValue("series"), r.PathValue("file")
	path, ok := libraryFilePath(s.cfg.LibraryDir, series, file)
	if !ok || !strings.EqualFold(filepath.Ext(file), ".cbz") {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	settings := pageSettings{Device: q.Get("device"), FitMode: q.Get("fitMode"), Enhance: q.Get("enhance") == "1",
		Widepage: q.Get("widepage"), Autocrop: q.Get("autocrop") == "1"}
	if q.Get("rtl") == "0" {
		no := false
		settings.RightToLeft = &no
	}
	opts, err := settings.options()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))

	img, err := readPage(path, page)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if q.Get("raw") != "1" {
		if out := kindle.ProcessPage(img, opts); len(out) > 0 {
			img = out[0]
		}
	}
	width := previewAfterW
	if q.Get("raw") == "1" {
		width = previewBeforeW
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaleToWidth(img, width), &jpeg.Options{Quality: 80}); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// readPage decodes the n-th picture (counting from 0) of a CBZ.
func readPage(path string, n int) (image.Image, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck
	var pages []*zip.File
	for _, f := range zr.File {
		switch strings.ToLower(filepath.Ext(f.Name)) {
		case ".jpg", ".jpeg", ".png", ".gif":
			pages = append(pages, f)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })
	if n < 0 || n >= len(pages) {
		return nil, errors.New("no such page")
	}
	if pages[n].UncompressedSize64 > maxCoverEntry {
		return nil, errors.New("page too large")
	}
	rc, err := pages[n].Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close() //nolint:errcheck
	img, _, err := image.Decode(io.LimitReader(rc, maxCoverEntry))

	return img, err
}

type convertRequest struct {
	Series     string `json:"series"`
	ProgressID string `json:"progressId"`
	pageSettings
}

type convertResult struct {
	Series    string   `json:"series"`
	Output    string   `json:"output"`
	Converted int      `json:"converted"`
	Failed    int      `json:"failed"`
	Notes     []string `json:"notes"`
}

// handleLibraryConvert writes a copy of every CBZ of a series with the pages
// prepared for a device, into a folder of its own next to the series.
func (s *Server) handleLibraryConvert(w http.ResponseWriter, r *http.Request) {
	var req convertRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Device == "" && !req.Enhance && req.Widepage == "" {
		writeError(w, http.StatusBadRequest, errors.New("choose a device or another setting to apply"))
		return
	}
	opts, err := req.options()
	if err != nil {
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
			files, found = series.Files, true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("no such series in the library"))
		return
	}

	if isConvertedName(req.Series) {
		writeError(w, http.StatusBadRequest, errors.New("this series is already a converted copy; convert the original instead"))
		return
	}
	suffix := "converted"
	if req.Device != "" {
		suffix = req.Device
	}
	outName := job.SafeDirName(req.Series + " [" + suffix + "]")
	res := convertResult{Series: req.Series, Output: outName, Notes: []string{}}

	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), convertTimeout)
	defer cancel()
	report := s.progressFor(req.ProgressID)
	total := 0
	for _, f := range files {
		if f.Format == "cbz" {
			total++
		}
	}
	report(0, total, "")
	doneFiles := 0
	for _, f := range files {
		if f.Format != "cbz" {
			continue
		}
		report(doneFiles, total, f.Name)
		doneFiles++
		if ctx.Err() != nil {
			res.Notes = append(res.Notes, "stopped: "+ctx.Err().Error())
			break
		}
		src := filepath.Join(s.cfg.LibraryDir, req.Series, f.Name)
		dst := filepath.Join(s.cfg.LibraryDir, outName, f.Name)
		err := cbz.Convert(src, dst, cbz.ConvertOptions{
			Process: func(img image.Image) []image.Image { return kindle.ProcessPage(img, opts) },
			Cover:   func(img image.Image) image.Image { return deviceApply(img, opts) },
		})
		if err != nil {
			res.Failed++
			res.Notes = append(res.Notes, fmt.Sprintf("%v: %v", f.Name, err))
			continue
		}
		res.Converted++
	}
	report(total, total, "")
	if res.Converted == 0 && res.Failed == 0 {
		_ = os.Remove(filepath.Join(s.cfg.LibraryDir, outName))
	}
	writeJSON(w, http.StatusOK, res)
}

func deviceApply(img image.Image, o kindle.PageOptions) image.Image {
	return device.Apply(img, o.Device)
}

// isConvertedName reports whether a library folder is a copy made by the
// conversion, i.e. its name ends with " [<device id>]" or " [converted]".
func isConvertedName(name string) bool {
	i := strings.LastIndex(name, " [")
	if i < 0 || !strings.HasSuffix(name, "]") {
		return false
	}
	tag := name[i+2 : len(name)-1]
	if tag == "converted" {
		return true
	}
	_, ok := device.Lookup(tag)
	return ok
}

type mobiRequest struct {
	Series string `json:"series"`
	// LeftToRight turns the reading direction round (manga reads right to left).
	LeftToRight bool   `json:"leftToRight"`
	ProgressID  string `json:"progressId"`
}

// handleLibraryMOBI writes an AZW3 next to every CBZ of a series, for Kindles
// that cannot open CBZ. The pages go in as they are in the CBZ, so make the
// CBZ ready for the device first.
func (s *Server) handleLibraryMOBI(w http.ResponseWriter, r *http.Request) {
	var req mobiRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	files, ok := s.findSeries(req.Series)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such series in the library"))
		return
	}
	var cbzs []LibraryFile
	for _, f := range files {
		if f.Format == "cbz" {
			cbzs = append(cbzs, f)
		}
	}
	s.convertMu.Lock()
	defer s.convertMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), convertTimeout)
	defer cancel()
	report := s.progressFor(req.ProgressID)
	res := convertResult{Series: req.Series, Output: req.Series, Notes: []string{}}
	for n, f := range cbzs {
		report(n, len(cbzs), f.Name)
		if ctx.Err() != nil {
			res.Notes = append(res.Notes, "stopped: "+ctx.Err().Error())
			break
		}
		src := filepath.Join(s.cfg.LibraryDir, req.Series, f.Name)
		dst := filepath.Join(s.cfg.LibraryDir, req.Series, strings.TrimSuffix(f.Name, filepath.Ext(f.Name))+".azw3")
		if err := writeAZW3(src, dst, req.Series, req.LeftToRight); err != nil {
			res.Failed++
			res.Notes = append(res.Notes, fmt.Sprintf("%v: %v", f.Name, err))
			continue
		}
		res.Converted++
	}
	report(len(cbzs), len(cbzs), "")
	writeJSON(w, http.StatusOK, res)
}

// writeAZW3 turns the pictures of a CBZ into a fixed layout AZW3.
func writeAZW3(src, dst, series string, ltr bool) error {
	var pages []image.Image
	cover := cbz.CoverIndex(src)
	var coverImg image.Image
	if err := eachPage(src, func(i int, img image.Image) {
		if img == nil {
			return
		}
		pages = append(pages, img)
		if i == cover {
			coverImg = img
		}
	}); err != nil {
		return err
	}
	if len(pages) == 0 {
		return errors.New("no readable pictures")
	}
	title := series + ": " + strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	book := kindle.BookFromImages(title, nil, coverImg, pages, ltr)
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".azw3-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if err := book.Realize().Write(tmp); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), dst)
}
