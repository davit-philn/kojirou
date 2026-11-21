package web

import (
	"net/http"
	"regexp"
	"sync"
	"time"
)

// progressState is how far a long operation of the library has come. The page
// asks for it while it waits for the operation's own answer.
type progressState struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Label string `json:"label"`
	seen  time.Time
}

type progressStore struct {
	mu sync.Mutex
	m  map[string]*progressState
}

var progressID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// progressFor returns a function that records progress under id; it does
// nothing for an empty or malformed id.
func (s *Server) progressFor(id string) func(done, total int, label string) {
	if !progressID.MatchString(id) {
		return func(int, int, string) {}
	}
	return func(done, total int, label string) {
		p := &s.progress
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.m == nil {
			p.m = map[string]*progressState{}
		}
		now := time.Now()
		for k, v := range p.m {
			if now.Sub(v.seen) > time.Hour {
				delete(p.m, k)
			}
		}
		p.m[id] = &progressState{Done: done, Total: total, Label: label, seen: now}
	}
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	s.progress.mu.Lock()
	st, ok := s.progress.m[r.PathValue("id")]
	var out progressState
	if ok {
		out = *st
	}
	s.progress.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusOK, progressState{})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
