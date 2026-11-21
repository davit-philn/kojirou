package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/leotaku/kojirou/cmd/formats/selector"
	"github.com/leotaku/kojirou/cmd/job"
	"github.com/leotaku/kojirou/cmd/multi"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

// multiSourceName is the source name of downloads that read several sources.
const multiSourceName = "multi"

const (
	multiParallel    = 4
	multiPerSource   = 8
	multiSearchTime  = 30 * time.Second
	multiChapterTime = 75 * time.Second
	maxMultiMembers  = 30
)

type multiSourceStatus struct {
	Source string `json:"source"`
	// Status is ok, error or skipped.
	Status string `json:"status"`
	Count  int    `json:"count"`
	Error  string `json:"error,omitempty"`
}

type multiSearchResult struct {
	Groups  []multi.Group       `json:"groups"`
	Sources []multiSourceStatus `json:"sources"`
}

// parallel runs the jobs, at most multiParallel at a time.
func parallel(n int, job func(i int)) {
	sem := make(chan struct{}, multiParallel)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			job(i)
		}(i)
	}
	wg.Wait()
}

// handleMultiSearch searches every saved source, and MangaDex, for a title and
// groups what is found by series.
func (s *Server) handleMultiSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := strings.TrimSpace(q.Get("q"))
	lang := q.Get("lang")
	if text == "" || len([]rune(text)) > maxSearchLength {
		writeError(w, http.StatusBadRequest, errors.New("enter a title to search for"))
		return
	}
	if lang != "" && !languagePattern.MatchString(lang) {
		writeError(w, http.StatusBadRequest, errors.New("invalid language"))
		return
	}
	saved, err := s.sources.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	type task struct {
		name string
		run  func(ctx context.Context) ([]multi.Candidate, error)
	}
	var tasks []task
	if s.dex != nil {
		tasks = append(tasks, task{mangadexSource, func(ctx context.Context) ([]multi.Candidate, error) {
			items, _, err := s.dex.Search(ctx, md.SearchParams{Title: text, Language: lang, Limit: multiPerSource, IncludeAdult: q.Get("adult") == "1"})
			out := make([]multi.Candidate, 0, len(items))
			for _, it := range items {
				out = append(out, multi.Candidate{Source: mangadexSource, Title: it.Title, ID: it.ID, Cover: it.CoverURL})
			}
			return out, err
		}})
	}
	statuses := make([]multiSourceStatus, 0, len(saved)+1)
	skipped := []multiSourceStatus{}
	for _, src := range saved {
		cfg := src.Config
		if !cfg.Searchable() || !cfg.Browsable() {
			skipped = append(skipped, multiSourceStatus{Source: src.Name, Status: "skipped", Error: "no search address; open a series of it by its address"})
			continue
		}
		name := src.Name
		tasks = append(tasks, task{name, func(ctx context.Context) ([]multi.Candidate, error) {
			page, err := cfg.SearchAddress(text)
			if err != nil {
				return nil, err
			}
			result, err := selector.ListSeries(ctx, page, cfg)
			if err != nil {
				return nil, err
			}
			out := []multi.Candidate{}
			for _, it := range result.Items {
				if len(out) >= multiPerSource {
					break
				}
				s.covers.add(it.CoverURL)
				out = append(out, multi.Candidate{Source: name, Title: it.Title, ID: it.URL, Cover: it.CoverURL})
			}
			return out, nil
		}})
	}

	found := make([][]multi.Candidate, len(tasks))
	results := make([]multiSourceStatus, len(tasks))
	ctx, cancel := context.WithTimeout(r.Context(), multiSearchTime+5*time.Second)
	defer cancel()
	parallel(len(tasks), func(i int) {
		tctx, tcancel := context.WithTimeout(ctx, multiSearchTime)
		defer tcancel()
		list, err := tasks[i].run(tctx)
		results[i] = multiSourceStatus{Source: tasks[i].name, Status: "ok", Count: len(list)}
		if err != nil {
			results[i].Status, results[i].Error = "error", err.Error()
			return
		}
		found[i] = list
	})
	var all []multi.Candidate
	for i := range tasks {
		all = append(all, found[i]...)
	}
	statuses = append(statuses, results...)
	statuses = append(statuses, skipped...)
	groups := multi.GroupCandidates(all)
	multi.Rank(groups, text)
	writeJSON(w, http.StatusOK, multiSearchResult{Groups: groups, Sources: statuses})
}

type multiMember struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Title  string `json:"title"`
}

type multiChaptersRequest struct {
	Members []multiMember `json:"members"`
	Lang    string        `json:"lang"`
}

type multiMemberOut struct {
	// Key names the member in the table: its source, numbered if a source appears twice.
	Key    string `json:"key"`
	Source string `json:"source"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Error  string `json:"error,omitempty"`
}

type multiChaptersResult struct {
	Table   multi.Table      `json:"table"`
	Members []multiMemberOut `json:"members"`
}

// handleMultiChapters lists the chapters of the chosen series on each of their
// sources and merges them into one table.
func (s *Server) handleMultiChapters(w http.ResponseWriter, r *http.Request) {
	var req multiChaptersRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Members) == 0 || len(req.Members) > maxMultiMembers {
		writeError(w, http.StatusBadRequest, errors.New("choose between 1 and 30 sources"))
		return
	}
	lang := req.Lang
	if lang == "" {
		lang = "vi"
	}
	if !languagePattern.MatchString(lang) {
		writeError(w, http.StatusBadRequest, errors.New("invalid language"))
		return
	}
	tag := language.Make(lang)

	members := make([]multiMemberOut, len(req.Members))
	count := map[string]int{}
	for i, m := range req.Members {
		count[m.Source]++
		key := m.Source
		if count[m.Source] > 1 {
			key = fmt.Sprintf("%s #%d", m.Source, count[m.Source])
		}
		members[i] = multiMemberOut{Key: key, Source: m.Source, ID: m.ID, Title: m.Title}
	}
	lists := make([][]multi.Chapter, len(members))
	ctx, cancel := context.WithTimeout(r.Context(), multiChapterTime+5*time.Second)
	defer cancel()
	parallel(len(members), func(i int) {
		tctx, tcancel := context.WithTimeout(ctx, multiChapterTime)
		defer tcancel()
		list, err := s.memberChapters(tctx, members[i], tag)
		if err != nil {
			members[i].Error = err.Error()
			return
		}
		lists[i] = list
	})
	merged := map[string][]multi.Chapter{}
	for i, m := range members {
		if m.Error == "" {
			merged[m.Key] = lists[i]
		}
	}
	writeJSON(w, http.StatusOK, multiChaptersResult{Table: multi.Merge(merged), Members: members})
}

func (s *Server) memberChapters(ctx context.Context, m multiMemberOut, tag language.Tag) ([]multi.Chapter, error) {
	if m.Source == mangadexSource {
		if s.dex == nil {
			return nil, errors.New("MangaDex is not available")
		}
		if !uuidPattern.MatchString(m.ID) {
			return nil, errors.New("invalid MangaDex series id")
		}
		chapters, err := s.dex.FetchChapters(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		base, _ := tag.Base()
		out := []multi.Chapter{}
		for _, c := range chapters {
			if b, _ := c.Info.Language.Base(); b != base {
				continue
			}
			out = append(out, multi.Chapter{Number: c.Info.Identifier.String(), Title: c.Info.Title, ID: c.Info.ID, Groups: []string(c.Info.GroupNames)})
		}
		return out, nil
	}
	cfg, ok, err := s.sources.Get(m.Source)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("unknown source %q", m.Source)
	}
	if u, err := url.Parse(m.ID); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !cfg.SameSite(m.ID) {
		return nil, errors.New("the series address must be on the source's own website")
	}
	chapters, err := selector.NewProvider(cfg, tag).Chapters(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	out := make([]multi.Chapter, 0, len(chapters))
	for _, c := range chapters {
		out = append(out, multi.Chapter{Number: c.Info.Identifier.String(), Title: c.Info.Title, ID: c.Info.ID})
	}

	return out, nil
}

// resolveMulti checks a request for a download from several sources and turns
// it into the job's settings. The volumes come from the MangaDex member when
// there is one.
func (s *Server) resolveMulti(req downloadRequest) (*job.MultiSource, string, error) {
	m := req.Multi
	if len(m.Members) == 0 || len(m.Members) > maxMultiMembers {
		return nil, "", errors.New("choose between 1 and 30 sources")
	}
	title := strings.TrimSpace(m.Title)
	if title == "" {
		title = m.Members[0].Title
	}
	if title == "" || len(title) > 200 {
		return nil, "", errors.New("the series needs a title of up to 200 characters")
	}
	if m.ChaptersPerVolume < 0 || m.ChaptersPerVolume > 1000 {
		return nil, "", errors.New("chapters per volume must be between 0 and 1000")
	}
	out := &job.MultiSource{Title: title, Choice: map[string]int{}, ChaptersPerVolume: m.ChaptersPerVolume}
	volumesFrom := ""
	for _, mem := range m.Members {
		member := job.MultiMember{Name: mem.Source, ID: mem.ID}
		if mem.Source == mangadexSource {
			if !uuidPattern.MatchString(mem.ID) {
				return nil, "", errors.New("invalid MangaDex series id")
			}
			if volumesFrom == "" {
				volumesFrom = mem.ID
			}
		} else {
			cfg, ok, err := s.sources.Get(mem.Source)
			if err != nil {
				return nil, "", err
			}
			if !ok {
				return nil, "", fmt.Errorf("unknown source %q", mem.Source)
			}
			if u, err := url.Parse(mem.ID); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !cfg.SameSite(mem.ID) {
				return nil, "", fmt.Errorf("%s: the series address must be on the source's own website", mem.Source)
			}
			member.Config = &cfg
		}
		out.Members = append(out.Members, member)
	}
	for number, index := range m.Choice {
		if index < 0 || index >= len(out.Members) || len(number) > 16 {
			return nil, "", errors.New("a chapter is assigned to a source that is not chosen")
		}
		out.Choice[number] = index
	}
	if v := strings.TrimSpace(req.VolumesFrom); v != "" {
		id, ok := md.ParseSeriesID(v)
		if !ok {
			return nil, "", errors.New("volumes from MangaDex: not a MangaDex series ID or link")
		}
		volumesFrom = id
	}

	return out, volumesFrom, nil
}
