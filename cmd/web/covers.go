package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/leotaku/kojirou/cmd/formats/cbz"
	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/formats/selector"
	md "github.com/leotaku/kojirou/mangadex"
)

const (
	coverUpdateTimeout = 10 * time.Minute
	// recentlyWritten skips files that a download may still be writing.
	recentlyWritten = 20 * time.Second
)

type libraryCoversRequest struct {
	Series string `json:"series"`
	// MangaDexID or a MangaDex link; empty means the remembered one or a search by name.
	MangaDex  string `json:"mangadex"`
	CoverPage string `json:"coverPage"`
}

type libraryCoversResult struct {
	Series string `json:"series"`
	// Status is "updated", "partial", "nothing", "unmatched" or "failed".
	Status  string   `json:"status"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Matched string   `json:"matched,omitempty"`
	Notes   []string `json:"notes"`
	// Candidates lists MangaDex series that may be the right one, when the
	// name alone did not decide.
	Candidates []candidate `json:"candidates,omitempty"`
}

type candidate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
}

var nonWordTitle = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func titleKey(s string) string {
	return strings.TrimSpace(nonWordTitle.ReplaceAllString(strings.ToLower(s), " "))
}

// handleLibraryCovers puts proper covers into the CBZ files of one series of
// the library, without downloading the chapters again.
func (s *Server) handleLibraryCovers(w http.ResponseWriter, r *http.Request) {
	var req libraryCoversRequest
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
			files, found = series.Files, true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("no such series in the library"))
		return
	}
	if req.CoverPage != "" {
		if err := checkHTTPURL(req.CoverPage); err != nil || len(req.CoverPage) > 2048 {
			writeError(w, http.StatusBadRequest, errors.New("the cover page must be an http(s) address"))
			return
		}
	}
	dexID := ""
	if req.MangaDex != "" {
		id, ok := md.ParseSeriesID(req.MangaDex)
		if !ok {
			writeError(w, http.StatusBadRequest, errors.New("not a MangaDex series ID or link"))
			return
		}
		dexID = id
	}

	// One update at a time: the files are rewritten.
	s.coversMu.Lock()
	defer s.coversMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), coverUpdateTimeout)
	defer cancel()

	result := s.updateSeriesCovers(ctx, req, dexID, files)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) updateSeriesCovers(ctx context.Context, req libraryCoversRequest, dexID string, files []LibraryFile) libraryCoversResult {
	res := libraryCoversResult{Series: req.Series, Status: "failed", Notes: []string{}}
	meta, _ := s.meta.Get(req.Series)
	if dexID == "" {
		dexID = meta.MangaDexID
	}
	coverPage := req.CoverPage
	if coverPage == "" {
		coverPage = meta.CoverPage
	}

	// Which volumes does the series have, and what is its name?
	volumes := map[string]string{} // volume number -> file name
	for _, f := range files {
		if f.Format != "cbz" {
			continue
		}
		id := md.NewIdentifier(strings.TrimSuffix(f.Name, filepath.Ext(f.Name)))
		if id.IsSpecial() {
			res.Notes = append(res.Notes, fmt.Sprintf("%v: the file name is not a volume number, skipped", f.Name))
			res.Skipped++
			continue
		}
		volumes[id.String()] = f.Name
	}
	if len(volumes) == 0 {
		res.Status, res.Notes = "nothing", append(res.Notes, "No CBZ volume with a number in its name")
		return res
	}

	sources := map[string]volumePicture{}
	if coverPage != "" {
		covers, err := selector.FindVolumeCovers(ctx, coverPage, nil)
		if err != nil {
			res.Notes = append(res.Notes, "Cover page: "+err.Error())
		}
		for _, c := range covers {
			if _, want := volumes[md.NewIdentifier(c.Volume).String()]; want {
				sources[md.NewIdentifier(c.Volume).String()] = volumePicture{url: c.URL}
			}
		}
	}

	title := req.Series
	if len(sources) < len(volumes) {
		if dexID == "" {
			id, found, candidates, err := s.matchSeries(ctx, req.Series)
			switch {
			case err != nil:
				res.Notes = append(res.Notes, "MangaDex search: "+err.Error())
			case found:
				dexID = id
			default:
				res.Candidates = candidates
			}
		}
		if dexID != "" {
			paths, err := download.InteractiveClient().FetchCovers(ctx, dexID)
			if err != nil {
				res.Notes = append(res.Notes, "MangaDex covers: "+err.Error())
			}
			for _, p := range paths {
				key := p.VolumeIdentifier.String()
				if _, want := volumes[key]; want && !p.VolumeIdentifier.IsSpecial() {
					if _, have := sources[key]; !have {
						sources[key] = volumePicture{url: p.DataURL}
					}
				}
			}
			if summary, err := s.dex.Describe(ctx, dexID); err == nil {
				res.Matched, title = summary.Title, summary.Title
			}
		}
	}
	if dexID == "" && coverPage == "" {
		res.Status = "unmatched"
		res.Notes = append(res.Notes, "Could not tell which MangaDex series this is; choose it or paste a link")
		return res
	}

	failures := 0
	for _, volume := range sortedVolumes(volumes) {
		name := volumes[volume]
		if failures >= 2 {
			res.Skipped++
			continue
		}
		pic, ok := sources[volume]
		if !ok {
			res.Skipped++
			res.Notes = append(res.Notes, fmt.Sprintf("Volume %v: no cover was found for it", volume))
			continue
		}
		path := filepath.Join(s.cfg.LibraryDir, req.Series, name)
		if info, err := os.Stat(path); err != nil || (time.Since(info.ModTime()) < recentlyWritten && !s.writtenByUs(path, info.ModTime())) {
			res.Skipped++
			res.Notes = append(res.Notes, fmt.Sprintf("%v: skipped (it is being written)", name))
			continue
		}
		data, ext, err := fetchCover(ctx, pic.url)
		if err != nil {
			failures++
			res.Skipped++
			res.Notes = append(res.Notes, fmt.Sprintf("%v: cover download failed (%v)", name, err))
			if failures >= 2 {
				res.Notes = append(res.Notes, "Two covers in a row could not be downloaded, so the rest were skipped: the picture server may be unreachable from this computer")
			}
			continue
		}
		failures = 0
		if err := cbz.SetCover(path, cbz.CoverUpdate{Picture: data, Ext: ext, Series: title, Volume: volume}); err != nil {
			res.Skipped++
			res.Notes = append(res.Notes, fmt.Sprintf("%v: %v", name, err))
			continue
		}
		if info, err := os.Stat(path); err == nil {
			s.rememberWrite(path, info.ModTime())
		}
		res.Updated++
	}

	switch {
	case res.Updated > 0 && res.Skipped == 0:
		res.Status = "updated"
	case res.Updated > 0:
		res.Status = "partial"
	default:
		res.Status = "nothing"
	}
	if res.Updated > 0 && (dexID != "" || coverPage != "") {
		_ = s.meta.Save(req.Series, SeriesMeta{MangaDexID: dexID, CoverPage: coverPage})
	}

	return res
}

type volumePicture struct{ url string }

// coverDownloadTimeout bounds the download of one cover.
const coverDownloadTimeout = 40 * time.Second

func fetchCover(ctx context.Context, address string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, coverDownloadTimeout)
	defer cancel()

	return download.FetchPictureQuick(ctx, address)
}

// sortedVolumes lists the volume numbers in order, so that results come in a
// predictable order.
func sortedVolumes(volumes map[string]string) []string {
	keys := make([]string, 0, len(volumes))
	for k := range volumes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return md.NewIdentifier(keys[i]).Less(md.NewIdentifier(keys[j]))
	})

	return keys
}

// writtenByUs tells a file this server rewrote itself from one that a download
// may still be writing.
func (s *Server) writtenByUs(path string, modified time.Time) bool {
	s.writtenMu.Lock()
	defer s.writtenMu.Unlock()

	return s.written[path].Equal(modified)
}

func (s *Server) rememberWrite(path string, modified time.Time) {
	s.writtenMu.Lock()
	defer s.writtenMu.Unlock()
	if s.written == nil {
		s.written = map[string]time.Time{}
	}
	s.written[path] = modified
}

// matchSeries looks the library series up on MangaDex by its name. It only
// accepts a result whose title equals the name; otherwise the likely ones
// are returned for the person to choose from.
func (s *Server) matchSeries(ctx context.Context, name string) (string, bool, []candidate, error) {
	items, _, err := s.dex.Search(ctx, md.SearchParams{Title: name, IncludeAdult: true, Limit: 8})
	if err != nil {
		return "", false, nil, err
	}
	want := titleKey(name)
	var exact []md.SeriesSummary
	candidates := make([]candidate, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, candidate{ID: item.ID, Title: item.Title, Year: item.Year})
		if titleKey(item.Title) == want {
			exact = append(exact, item)
		}
	}
	if len(exact) == 1 {
		return exact[0].ID, true, nil, nil
	}

	return "", false, candidates, nil
}

func checkHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("invalid")
	}

	return nil
}
