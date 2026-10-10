// Package web serves a local web interface for browsing series and
// downloading them with the job pipeline.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/formats/kindle"
	"github.com/leotaku/kojirou/cmd/formats/selector"
	"github.com/leotaku/kojirou/cmd/job"
	md "github.com/leotaku/kojirou/mangadex"
)

//go:embed static
var staticFiles embed.FS

const (
	clientHeader   = "X-Kojirou-Client"
	maxBodyBytes   = 1 << 20
	maxChapterIDs  = 5000
	mangadexSource = "mangadex"
)

var (
	languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	orderings       = map[string]bool{"": true, "relevance": true, "followedCount": true, "latestUploadedChapter": true, "createdAt": true}
)

type Config struct {
	// Addr is the listen address, e.g. 127.0.0.1:8080.
	Addr       string
	LibraryDir string
	ConfigDir  string
	Version    string
	// Dex is the MangaDex client for search; defaults to the public API.
	Dex *md.Client
	// Runner runs downloads; defaults to job.Run.
	Runner Runner
}

type Server struct {
	cfg     Config
	dex     *md.Client
	jobs    *Manager
	sources *SourceStore
	hosts   map[string]bool
	static  http.Handler
}

func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.LibraryDir == "" {
		return nil, errors.New("no library directory")
	}
	abs, err := filepath.Abs(cfg.LibraryDir)
	if err != nil {
		return nil, err
	}
	cfg.LibraryDir = abs
	if cfg.Dex == nil {
		cfg.Dex = md.NewClient()
	}
	if cfg.Runner == nil {
		cfg.Runner = job.Run
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:     cfg,
		dex:     cfg.Dex,
		jobs:    NewManager(ctx, cfg.Runner),
		sources: NewSourceStore(filepath.Join(cfg.ConfigDir, "sources.json")),
		hosts:   allowedHosts(cfg.Addr),
		static:  noCache(http.FileServer(http.FS(sub))),
	}

	return s, nil
}

// allowedHosts lists the Host header values accepted. Rejecting everything
// else stops DNS rebinding attacks against the local server.
func allowedHosts(addr string) map[string]bool {
	hosts := map[string]bool{addr: true}
	if _, port, err := net.SplitHostPort(addr); err == nil {
		for _, h := range []string{"localhost", "127.0.0.1", "[::1]"} {
			hosts[h+":"+port] = true
		}
	}

	return hosts
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/series", s.handleSeries)
	mux.HandleFunc("GET /api/downloads", s.handleListDownloads)
	mux.HandleFunc("POST /api/downloads", s.handleStartDownload)
	mux.HandleFunc("DELETE /api/downloads/{id}", s.handleCancelDownload)
	mux.HandleFunc("GET /api/library", s.handleLibrary)
	mux.HandleFunc("GET /api/library/{series}/{file}", s.handleLibraryFile)
	mux.HandleFunc("GET /api/sources", s.handleListSources)
	mux.HandleFunc("POST /api/sources", s.handleSaveSource)
	mux.HandleFunc("DELETE /api/sources/{name}", s.handleDeleteSource)
	mux.HandleFunc("POST /api/sources/test", s.handleTestSource)
	mux.Handle("/", s.static)

	return s.guard(mux)
}

// Serve listens on cfg.Addr until ctx is done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// guard rejects requests that could come from other web pages or from a
// rebound DNS name: unknown Host headers, cross-origin requests and
// state-changing requests without the custom client header. Browsers do not
// let other origins set that header without a preflight, which we never grant.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get(clientHeader) == "" {
				http.Error(w, "missing client header", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}

	return nil
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	names := []string{mangadexSource}
	if saved, err := s.sources.List(); err == nil {
		for _, src := range saved {
			names = append(names, src.Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"library": s.cfg.LibraryDir,
		"version": s.cfg.Version,
		"sources": names,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lang := q.Get("lang")
	if lang != "" && !languagePattern.MatchString(lang) {
		writeError(w, http.StatusBadRequest, errors.New("invalid language"))
		return
	}
	if !orderings[q.Get("order")] {
		writeError(w, http.StatusBadRequest, errors.New("invalid order"))
		return
	}
	offset := atoiClamp(q.Get("offset"), 0, 10000, 0)
	items, total, err := s.dex.Search(r.Context(), md.SearchParams{
		Title:        strings.TrimSpace(q.Get("q")),
		Language:     lang,
		Order:        q.Get("order"),
		IncludeAdult: q.Get("adult") == "1",
		Limit:        24,
		Offset:       offset,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func atoiClamp(s string, lo, hi, def int) int {
	n := 0
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}

	return n
}

type chapterDTO struct {
	ID        string    `json:"id"`
	Number    string    `json:"number"`
	Title     string    `json:"title"`
	Groups    []string  `json:"groups"`
	Published time.Time `json:"published"`
}

type volumeDTO struct {
	ID       string       `json:"id"`
	Chapters []chapterDTO `json:"chapters"`
}

type seriesDTO struct {
	ID          string      `json:"id"`
	Source      string      `json:"source"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	CoverURL    string      `json:"coverUrl,omitempty"`
	Authors     []string    `json:"authors"`
	Status      string      `json:"status,omitempty"`
	Year        int         `json:"year,omitempty"`
	Languages   []string    `json:"languages"`
	Volumes     []volumeDTO `json:"volumes"`
}

// resolveSource validates the source and identifier of a request.
func (s *Server) resolveSource(source, id string) (*selector.SourceConfig, error) {
	if source == "" || source == mangadexSource {
		if !uuidPattern.MatchString(id) {
			return nil, errors.New("invalid MangaDex series id")
		}
		return nil, nil
	}
	cfg, ok, err := s.sources.Get(source)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("unknown source %q", source)
	}
	if u, err := url.Parse(id); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the series must be an http(s) URL")
	}

	return &cfg, nil
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source, id, lang := q.Get("source"), q.Get("id"), q.Get("lang")
	if lang == "" {
		lang = "en"
	}
	if !languagePattern.MatchString(lang) {
		writeError(w, http.StatusBadRequest, errors.New("invalid language"))
		return
	}
	cfg, err := s.resolveSource(source, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	dto := seriesDTO{ID: id, Source: source, Authors: []string{}, Languages: []string{}, Volumes: []volumeDTO{}}
	if cfg == nil {
		dto.Source = mangadexSource
		summary, err := s.dex.Describe(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		dto.Title, dto.Description, dto.CoverURL = summary.Title, summary.Description, summary.CoverURL
		dto.Authors, dto.Status, dto.Year, dto.Languages = summary.Authors, summary.Status, summary.Year, summary.Languages
	}

	manga, err := job.Inspect(r.Context(), job.Options{
		Identifier: id,
		Source:     cfg,
		Language:   lang,
		Rank:       "most",
		Format:     job.FormatCBZ,
	}, job.NopReporter{})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if dto.Title == "" {
		dto.Title = manga.Info.Title
	}
	for _, vol := range manga.Sorted() {
		v := volumeDTO{ID: vol.Info.Identifier.String(), Chapters: []chapterDTO{}}
		for _, ch := range vol.Sorted() {
			groups := []string(ch.Info.GroupNames)
			if groups == nil {
				groups = []string{}
			}
			v.Chapters = append(v.Chapters, chapterDTO{
				ID:        ch.Info.ID,
				Number:    ch.Info.Identifier.String(),
				Title:     ch.Info.Title,
				Groups:    groups,
				Published: ch.Info.Published,
			})
		}
		dto.Volumes = append(dto.Volumes, v)
	}
	writeJSON(w, http.StatusOK, dto)
}

type downloadRequest struct {
	Source      string   `json:"source"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Lang        string   `json:"lang"`
	ChapterIDs  []string `json:"chapterIds"`
	Format      string   `json:"format"`
	RightToLeft *bool    `json:"rightToLeft"`
	Autocrop    bool     `json:"autocrop"`
	Widepage    string   `json:"widepage"`
	DataSaver   string   `json:"dataSaver"`
	JPEGQuality int      `json:"jpegQuality"`
	Lossless    bool     `json:"lossless"`
	Force       bool     `json:"force"`
}

var (
	widepagePolicies = map[string]kindle.WidepagePolicy{
		"":                   kindle.WidepagePolicyPreserve,
		"preserve":           kindle.WidepagePolicyPreserve,
		"split":              kindle.WidepagePolicySplit,
		"preserve-and-split": kindle.WidepagePolicyPreserveAndSplit,
		"split-and-preserve": kindle.WidepagePolicySplitAndPreserve,
	}
	dataSaverPolicies = map[string]download.DataSaverPolicy{
		"":         download.DataSaverPolicyNo,
		"no":       download.DataSaverPolicyNo,
		"prefer":   download.DataSaverPolicyPrefer,
		"fallback": download.DataSaverPolicyFallback,
	}
)

func (s *Server) handleStartDownload(w http.ResponseWriter, r *http.Request) {
	var req downloadRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	opts, err := s.optionsFromRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	title := req.Title
	if title == "" {
		title = req.ID
	}
	if len(title) > 200 {
		title = title[:200]
	}
	source := req.Source
	if source == "" {
		source = mangadexSource
	}
	info, err := s.jobs.Enqueue(opts, title, source)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusAccepted, info)
}

func (s *Server) optionsFromRequest(req downloadRequest) (job.Options, error) {
	var zero job.Options
	cfg, err := s.resolveSource(req.Source, req.ID)
	if err != nil {
		return zero, err
	}
	lang := req.Lang
	if lang == "" {
		lang = "en"
	}
	if !languagePattern.MatchString(lang) {
		return zero, errors.New("invalid language")
	}
	if len(req.ChapterIDs) == 0 {
		return zero, errors.New("select at least one chapter")
	}
	if len(req.ChapterIDs) > maxChapterIDs {
		return zero, fmt.Errorf("too many chapters (max %d)", maxChapterIDs)
	}
	format := job.Format(req.Format)
	if format == "" {
		format = job.FormatCBZ
	}
	if format != job.FormatCBZ && format != job.FormatMOBI {
		return zero, errors.New("format must be cbz or mobi")
	}
	widepage, ok := widepagePolicies[req.Widepage]
	if !ok {
		return zero, errors.New("invalid widepage option")
	}
	saver, ok := dataSaverPolicies[req.DataSaver]
	if !ok {
		return zero, errors.New("invalid data saver option")
	}
	quality := req.JPEGQuality
	if quality == 0 {
		quality = 90
	}
	if quality < 1 || quality > 100 {
		return zero, errors.New("JPEG quality must be between 1 and 100")
	}
	rtl := true
	if req.RightToLeft != nil {
		rtl = *req.RightToLeft
	}

	return job.Options{
		Identifier:  req.ID,
		Source:      cfg,
		Language:    lang,
		Rank:        "most",
		ChapterIDs:  req.ChapterIDs,
		Force:       req.Force,
		Format:      format,
		Autocrop:    req.Autocrop,
		Widepage:    widepage,
		LeftToRight: !rtl,
		DataSaver:   saver,
		JPEGQuality: quality,
		Lossless:    req.Lossless,
		LibraryDir:  s.cfg.LibraryDir,
	}, nil
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.jobs.List())
}

func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	if !s.jobs.Cancel(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, errors.New("no such download"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	list, err := scanLibrary(s.cfg.LibraryDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleLibraryFile(w http.ResponseWriter, r *http.Request) {
	series, file := r.PathValue("series"), r.PathValue("file")
	path, ok := libraryFilePath(s.cfg.LibraryDir, series, file)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close() //nolint:errcheck
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := series + " - " + file
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(w, r, file, info.ModTime(), f)
}

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.sources.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type sourceRequest struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

func (s *Server) handleSaveSource(w http.ResponseWriter, r *http.Request) {
	var req sourceRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := selector.ParseConfig(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.sources.Save(req.Name, cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, SavedSource{Name: req.Name, Config: cfg})
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	ok, err := s.sources.Delete(r.PathValue("name"))
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	case !ok:
		writeError(w, http.StatusNotFound, errors.New("no such source"))
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

type testRequest struct {
	Config json.RawMessage `json:"config"`
	URL    string          `json:"url"`
}

// handleTestSource tries a configuration against a series page and its first
// chapter, so selectors can be checked before they are saved.
func (s *Server) handleTestSource(w http.ResponseWriter, r *http.Request) {
	var req testRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := selector.ParseConfig(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if u, err := url.Parse(req.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, errors.New("the series must be an http(s) URL"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	src := selector.New(cfg)
	chapters, err := src.FetchChapterList(ctx, req.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("chapter list: %w", err))
		return
	}
	result := map[string]any{"chapterCount": len(chapters), "chapters": firstN(chapters, 5)}
	images, err := src.FetchImages(ctx, chapters[0].URL)
	if err != nil {
		result["imageError"] = err.Error()
	} else {
		result["imageCount"] = len(images)
		result["images"] = firstN(images, 3)
	}
	writeJSON(w, http.StatusOK, result)
}

func firstN[T any](list []T, n int) []T {
	if len(list) > n {
		return list[:n]
	}

	return list
}
