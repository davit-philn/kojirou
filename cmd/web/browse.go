package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leotaku/kojirou/cmd/formats/selector"
)

const (
	maxCoverBytes   = 5 << 20
	maxKnownCovers  = 5000
	maxSearchLength = 200
)

// coverSet remembers the cover addresses that browse results contained, so
// that the cover proxy only fetches addresses a source itself handed out and
// cannot be pointed at arbitrary URLs.
type coverSet struct {
	mu    sync.Mutex
	known map[string]bool
	order []string
}

func newCoverSet() *coverSet { return &coverSet{known: map[string]bool{}} }

func (c *coverSet) add(urls ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, u := range urls {
		if u == "" || c.known[u] {
			continue
		}
		c.known[u] = true
		c.order = append(c.order, u)
		if len(c.order) > maxKnownCovers {
			delete(c.known, c.order[0])
			c.order = c.order[1:]
		}
	}
}

func (c *coverSet) has(u string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.known[u]
}

type browseResponse struct {
	Items []selector.SeriesItem `json:"items"`
	Next  string                `json:"next,omitempty"`
	// Page is the address that was listed.
	Page string `json:"page"`
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cfg, ok, err := s.sources.Get(q.Get("source"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("unknown source"))
		return
	}
	if !cfg.Browsable() {
		writeError(w, http.StatusBadRequest, errors.New("this source cannot list series; open a series by its address instead"))
		return
	}

	page := strings.TrimSpace(q.Get("page"))
	if text := strings.TrimSpace(q.Get("q")); text != "" {
		if len([]rune(text)) > maxSearchLength {
			writeError(w, http.StatusBadRequest, errors.New("search text too long"))
			return
		}
		if page, err = cfg.SearchAddress(text); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	} else if page == "" {
		page = cfg.DefaultListURL()
	}
	if !cfg.SameSite(page) {
		writeError(w, http.StatusBadRequest, errors.New("the page must be on the source's own website"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result, err := selector.ListSeries(ctx, page, cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for _, item := range result.Items {
		s.covers.add(item.CoverURL)
	}
	if result.Next != "" && !cfg.SameSite(result.Next) {
		result.Next = ""
	}
	writeJSON(w, http.StatusOK, browseResponse{Items: result.Items, Next: result.Next, Page: page})
}

// handleCover serves a cover image of a browse result through the server, so
// that it is fetched with the source's Referer, which sites often require.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cfg, ok, err := s.sources.Get(q.Get("source"))
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	target := q.Get("url")
	if !s.covers.has(target) {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	resp, err := selector.NewClient(cfg).Do(req)
	if err != nil {
		http.Error(w, "cover unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	kind := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(kind, "image/") || strings.Contains(kind, "svg") {
		http.Error(w, "cover unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxCoverBytes))
}
