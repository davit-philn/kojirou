package job

import (
	"context"
	"fmt"
	"sort"

	"github.com/leotaku/kojirou/cmd/formats"
	"github.com/leotaku/kojirou/cmd/formats/selector"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

// FillSource describes a website whose chapters are used to fill the gaps in
// the main source: chapters the main source does not have are taken from here.
type FillSource struct {
	Config selector.SourceConfig
	// URL is the series page on the website.
	URL string
}

// fillSource combines a main source with a secondary one. Chapters present in
// the main source always win; only chapter numbers missing there are added,
// and they are placed into the volume that their neighbours belong to so that
// the main source's volumes and covers can be used.
type fillSource struct {
	main      contentSource
	extra     contentSource
	extraID   string
	lang      language.Tag
	extraIDs  map[string]bool // chapter IDs that must be downloaded from extra
	notes     []string
	extraName string
}

func newFillSource(main, extra contentSource, extraID string, lang language.Tag) *fillSource {
	return &fillSource{
		main:      main,
		extra:     extra,
		extraID:   extraID,
		lang:      lang,
		extraIDs:  make(map[string]bool),
		extraName: extra.Name(),
	}
}

func (s *fillSource) Name() string { return s.main.Name() + "+" + s.extra.Name() }

func (s *fillSource) Skeleton(ctx context.Context, id string) (*md.Manga, error) {
	return s.main.Skeleton(ctx, id)
}

func (s *fillSource) Chapters(ctx context.Context, id string) (md.ChapterList, error) {
	mainChapters, err := s.main.Chapters(ctx, id)
	if err != nil {
		return nil, err
	}
	extraChapters, err := s.extra.Chapters(ctx, s.extraID)
	if err != nil {
		return nil, fmt.Errorf("fill source: %w", err)
	}

	merged, notes := fillGaps(mainChapters, extraChapters, s.lang)
	s.notes = notes
	for _, ch := range merged[len(mainChapters):] {
		s.extraIDs[ch.Info.ID] = true
	}

	return merged, nil
}

// Notes lists placements that could not be decided with certainty.
func (s *fillSource) Notes() []string { return s.notes }

func (s *fillSource) Covers(ctx context.Context, manga *md.Manga, p formats.Progress) (md.ImageList, error) {
	return s.main.Covers(ctx, manga, p)
}

func (s *fillSource) Pages(ctx context.Context, chapters md.ChapterList, p formats.Progress) (md.ImageList, error) {
	fromMain := make(md.ChapterList, 0, len(chapters))
	fromExtra := make(md.ChapterList, 0)
	for _, ch := range chapters {
		if s.extraIDs[ch.Info.ID] {
			fromExtra = append(fromExtra, ch)
		} else {
			fromMain = append(fromMain, ch)
		}
	}

	var images md.ImageList
	if len(fromMain) > 0 {
		pages, err := s.main.Pages(ctx, fromMain, p)
		if err != nil {
			return nil, fmt.Errorf("%v: %w", s.main.Name(), err)
		}
		images = append(images, pages...)
	}
	if len(fromExtra) > 0 {
		pages, err := s.extra.Pages(ctx, fromExtra, p)
		if err != nil {
			return nil, fmt.Errorf("%v: %w", s.extraName, err)
		}
		images = append(images, pages...)
	}

	return images, nil
}

// fillGaps returns all of main followed by those chapters of extra whose
// chapter number main does not have in the given language. The result keeps
// main's order and slice prefix so callers can tell the added chapters apart.
func fillGaps(main, extra md.ChapterList, lang language.Tag) (md.ChapterList, []string) {
	have := make(map[md.Identifier]bool)
	known := make([]md.ChapterInfo, 0)
	for _, ch := range main {
		if ch.Info.Language != lang || ch.Info.Identifier.IsSpecial() {
			continue
		}
		have[ch.Info.Identifier] = true
		known = append(known, ch.Info)
	}
	sort.SliceStable(known, func(i, j int) bool { return known[i].Identifier.Less(known[j].Identifier) })

	merged := append(md.ChapterList(nil), main...)
	notes := make([]string, 0)
	added := make(map[md.Identifier]bool)
	for _, ch := range extra {
		id := ch.Info.Identifier
		if have[id] || added[id] || id.IsSpecial() {
			continue
		}
		added[id] = true

		volume, certain := inferVolume(known, id)
		ch.Info.VolumeIdentifier = volume
		ch.Info.Language = lang
		merged = append(merged, ch)
		if !certain {
			notes = append(notes, fmt.Sprintf(
				"chapter %v is not on the main source; its volume (%v) is a guess between two volumes",
				id, volume))
		}
	}

	return merged, notes
}

// inferVolume picks a volume for chapter id from the nearest chapters of the
// main source on either side (known is sorted by chapter number). It reports
// false when the neighbours disagree and the choice is a guess.
func inferVolume(known []md.ChapterInfo, id md.Identifier) (md.Identifier, bool) {
	var lo, hi *md.ChapterInfo
	for i := range known {
		switch {
		case known[i].Identifier.Less(id):
			lo = &known[i]
		case id.Less(known[i].Identifier) && hi == nil:
			hi = &known[i]
		}
	}

	switch {
	case lo != nil && hi != nil && lo.VolumeIdentifier.Equal(hi.VolumeIdentifier):
		return lo.VolumeIdentifier, true
	case lo != nil && hi != nil:
		return lo.VolumeIdentifier, false
	case lo != nil:
		// Newer than everything on the main source: not in a volume yet.
		return unvolumed(), true
	case hi != nil:
		return hi.VolumeIdentifier, false
	default:
		return unvolumed(), true
	}
}

func unvolumed() md.Identifier {
	return md.NewWithFallback("", "Special")
}
