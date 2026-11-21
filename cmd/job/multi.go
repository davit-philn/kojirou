package job

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/leotaku/kojirou/cmd/formats"
	"github.com/leotaku/kojirou/cmd/formats/selector"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

// MultiMember is one source a series can be read from.
type MultiMember struct {
	// Name labels the source in messages ("mangadex" or the saved source's name).
	Name string
	// Config is the website's configuration; nil means MangaDex.
	Config *selector.SourceConfig
	// ID is the MangaDex series ID, or the series page's address.
	ID string
}

// MultiSource makes one series out of several sources: for each chapter number
// the first source (in the order of Members) that has it is used, unless Choice
// names another one.
type MultiSource struct {
	Title   string
	Members []MultiMember
	// Choice maps a chapter number ("12", "12.5") to the index of the member
	// to take that chapter from.
	Choice map[string]int
	// ChaptersPerVolume, if above 0, puts chapters N at a time into a volume
	// whatever the sources say (volumes from MangaDex take precedence when
	// Options.VolumesFrom is set).
	ChaptersPerVolume int
	// VolumeRanges puts chapters into volumes by hand, e.g. "1:1-8, 2:9-17,
	// 3:18-26" (volume:first-last chapter). It wins over everything else.
	VolumeRanges string
}

type multiSource struct {
	cfg     MultiSource
	lang    language.Tag
	opts    Options
	members []contentSource
	// origin remembers which member each chapter ID returned by Chapters came
	// from, and the ID it has there.
	origin map[string]multiOrigin
	notes  []string
}

type multiOrigin struct {
	member int
	id     string
}

func newMultiSource(cfg MultiSource, lang language.Tag, opts Options) *multiSource {
	s := &multiSource{cfg: cfg, lang: lang, opts: opts, origin: map[string]multiOrigin{}}
	for _, m := range cfg.Members {
		if m.Config == nil {
			s.members = append(s.members, mangadexSource{dataSaver: opts.DataSaver, interactive: opts.Interactive})
		} else {
			s.members = append(s.members, selectorSource{Provider: selector.NewProvider(*m.Config, lang), problem: new(string)})
		}
	}

	return s
}

func (s *multiSource) Name() string { return "multi" }

// Notes says where the chapters came from and which sources failed.
func (s *multiSource) Notes() []string { return s.notes }

func (s *multiSource) Skeleton(_ context.Context, id string) (*md.Manga, error) {
	return &md.Manga{
		Info:    md.MangaInfo{Title: s.cfg.Title, ID: id},
		Volumes: make(map[md.Identifier]md.Volume),
	}, nil
}

func (s *multiSource) Chapters(ctx context.Context, _ string) (md.ChapterList, error) {
	type found struct {
		list md.ChapterList
		err  error
	}
	lists := make([]found, len(s.members))
	for i, m := range s.members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		all, err := m.Chapters(ctx, s.cfg.Members[i].ID)
		if err != nil {
			lists[i].err = err
			continue
		}
		base, _ := s.lang.Base()
		for _, ch := range all {
			if b, _ := ch.Info.Language.Base(); b != base {
				continue
			}
			lists[i].list = append(lists[i].list, ch)
		}
	}

	// Per chapter number, which member has it (and its chapter there).
	type candidate struct {
		member  int
		chapter md.Chapter
	}
	byNumber := map[string][]candidate{}
	var order []string
	failed := 0
	for i, f := range lists {
		if f.err != nil {
			failed++
			s.notes = append(s.notes, fmt.Sprintf("%s: %v", s.cfg.Members[i].Name, f.err))
			continue
		}
		seen := map[string]bool{}
		for _, ch := range f.list {
			key := ch.Info.Identifier.String()
			if seen[key] {
				continue // two groups on one source: the first one is enough
			}
			seen[key] = true
			if _, ok := byNumber[key]; !ok {
				order = append(order, key)
			}
			byNumber[key] = append(byNumber[key], candidate{i, ch})
		}
	}
	if failed == len(s.members) {
		return nil, fmt.Errorf("none of the %d sources could be read: %s", failed, strings.Join(s.notes, "; "))
	}

	fromCount := map[string]int{}
	out := make(md.ChapterList, 0, len(order))
	for _, key := range order {
		cands := byNumber[key]
		pick := cands[0] // members are in priority order
		if want, ok := s.cfg.Choice[key]; ok {
			for _, c := range cands {
				if c.member == want {
					pick = c
				}
			}
		}
		ch := pick.chapter
		id := fmt.Sprintf("%d|%s", pick.member, ch.Info.ID)
		s.origin[id] = multiOrigin{member: pick.member, id: ch.Info.ID}
		ch.Info.ID = id
		if v, ok := s.volumeByCount(ch.Info.Identifier); ok {
			ch.Info.VolumeIdentifier = v
		}
		fromCount[s.cfg.Members[pick.member].Name]++
		out = append(out, ch)
	}
	for _, m := range s.cfg.Members {
		if n := fromCount[m.Name]; n > 0 {
			s.notes = append(s.notes, fmt.Sprintf("%d chapters from %s", n, m.Name))
		}
	}

	return out, nil
}

// volumeByCount puts chapter number n into volume ceil(n / ChaptersPerVolume).
func (s *multiSource) volumeByCount(id md.Identifier) (md.Identifier, bool) {
	if s.cfg.ChaptersPerVolume <= 0 || id.IsSpecial() {
		return md.Identifier{}, false
	}
	n, err := strconv.ParseFloat(id.String(), 64)
	if err != nil || n <= 0 {
		return md.Identifier{}, false
	}

	return md.NewIdentifier(strconv.Itoa(int(math.Ceil(n / float64(s.cfg.ChaptersPerVolume))))), true
}

// Covers: the volumes' covers come from MangaDex or a cover page, not from
// the websites.
func (s *multiSource) Covers(context.Context, *md.Manga, formats.Progress) (md.ImageList, error) {
	return md.ImageList{}, nil
}

func (s *multiSource) Pages(ctx context.Context, chapters md.ChapterList, p formats.Progress) (md.ImageList, error) {
	groups := make([]md.ChapterList, len(s.members))
	for _, ch := range chapters {
		o, ok := s.origin[ch.Info.ID]
		if !ok {
			return nil, fmt.Errorf("chapter %v does not come from any of the sources", ch.Info.Identifier)
		}
		ch.Info.ID = o.id
		groups[o.member] = append(groups[o.member], ch)
	}
	var images md.ImageList
	for i, list := range groups {
		if len(list) == 0 {
			continue
		}
		pages, err := s.members[i].Pages(ctx, list, p)
		if err != nil {
			return nil, fmt.Errorf("%v: %w", s.cfg.Members[i].Name, err)
		}
		images = append(images, pages...)
	}

	return images, nil
}

type volumeRange struct {
	volume   md.Identifier
	from, to float64
}

// parseVolumeRanges reads "1:1-8, 2:9-17, 3:18" (separated by commas, semicolons
// or new lines): volume, a colon, then one chapter or first-last.
func parseVolumeRanges(text string) ([]volumeRange, error) {
	var out []volumeRange
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		vol, chapters, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("%q: write volume:first-last, for example 2:9-17", part)
		}
		volume, err := ParseVolume(strings.TrimSpace(vol))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", part, err)
		}
		first, last, _ := strings.Cut(strings.TrimSpace(chapters), "-")
		from, err1 := strconv.ParseFloat(strings.TrimSpace(first), 64)
		to := from
		var err2 error
		if last != "" {
			to, err2 = strconv.ParseFloat(strings.TrimSpace(last), 64)
		}
		if err1 != nil || err2 != nil || to < from {
			return nil, fmt.Errorf("%q: the chapters must be a number or first-last", part)
		}
		out = append(out, volumeRange{volume: volume, from: from, to: to})
	}

	return out, nil
}

// applyVolumeRanges moves the chapters into the volumes the ranges name. The
// list is changed in place.
func applyVolumeRanges(chapters md.ChapterList, text string) {
	ranges, err := parseVolumeRanges(text)
	if err != nil || len(ranges) == 0 {
		return
	}
	for i := range chapters {
		id := chapters[i].Info.Identifier
		if id.IsSpecial() {
			continue
		}
		n, err := strconv.ParseFloat(id.String(), 64)
		if err != nil {
			continue
		}
		for _, r := range ranges {
			if n >= r.from && n <= r.to {
				chapters[i].Info.VolumeIdentifier = r.volume
				break
			}
		}
	}
}
