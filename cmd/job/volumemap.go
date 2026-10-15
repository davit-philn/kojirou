package job

import (
	"context"
	"fmt"
	"sort"

	"github.com/leotaku/kojirou/cmd/formats"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

// volumeMapper is a source that can say which volume each chapter number
// belongs to.
type volumeMapper interface {
	VolumeMap(ctx context.Context, id string) (map[md.Identifier]md.Identifier, error)
}

// maxListedGuesses is how many guessed chapters get a note of their own;
// beyond that they are summarised in one.
const maxListedGuesses = 5

// volumeMapSource takes chapters and pages from a website (the main source)
// and asks MangaDex only how the chapters are split into volumes, plus the
// series' metadata and covers. Chapters MangaDex does not know are placed
// next to their neighbours, like fillSource does for the opposite case.
type volumeMapSource struct {
	site    contentSource
	dex     contentSource
	mapper  volumeMapper
	siteURL string
	dexID   string
	lang    language.Tag
	notes   []string
}

func newVolumeMapSource(site, dex contentSource, mapper volumeMapper, siteURL, dexID string, lang language.Tag) *volumeMapSource {
	return &volumeMapSource{site: site, dex: dex, mapper: mapper, siteURL: siteURL, dexID: dexID, lang: lang}
}

func (s *volumeMapSource) Name() string { return s.site.Name() + "+" + s.dex.Name() }

// Skeleton returns MangaDex's description of the series. Its ID is the
// MangaDex ID, which covers need; the site is addressed by siteURL, so the
// identifier the job passes in (that URL) is not used.
func (s *volumeMapSource) Skeleton(ctx context.Context, _ string) (*md.Manga, error) {
	return s.dex.Skeleton(ctx, s.dexID)
}

func (s *volumeMapSource) Chapters(ctx context.Context, dexID string) (md.ChapterList, error) {
	siteChapters, err := s.site.Chapters(ctx, s.siteURL)
	if err != nil {
		return nil, err
	}
	volumes, err := s.mapper.VolumeMap(ctx, dexID)
	if err != nil {
		return nil, fmt.Errorf("volume map: %w", err)
	}

	chapters, notes := assignVolumes(volumes, siteChapters, s.lang)
	s.notes = notes

	return chapters, nil
}

// Notes lists placements that could not be decided with certainty.
func (s *volumeMapSource) Notes() []string { return s.notes }

func (s *volumeMapSource) Covers(ctx context.Context, manga *md.Manga, p formats.Progress) (md.ImageList, error) {
	return s.dex.Covers(ctx, manga, p)
}

func (s *volumeMapSource) Pages(ctx context.Context, chapters md.ChapterList, p formats.Progress) (md.ImageList, error) {
	return s.site.Pages(ctx, chapters, p)
}

// assignVolumes gives every chapter of site the volume that MangaDex has for
// the same chapter number. Numbers MangaDex lacks get the volume of their
// neighbours; the notes name the guesses.
func assignVolumes(volumes map[md.Identifier]md.Identifier, site md.ChapterList, lang language.Tag) (md.ChapterList, []string) {
	known := make([]md.ChapterInfo, 0, len(volumes))
	for chapter, volume := range volumes {
		known = append(known, md.ChapterInfo{Identifier: chapter, VolumeIdentifier: volume})
	}
	sort.SliceStable(known, func(i, j int) bool { return known[i].Identifier.Less(known[j].Identifier) })

	notes := make([]string, 0)
	if len(known) == 0 {
		notes = append(notes, "MangaDex has no volume information for this series; every chapter is in the Special volume")
	}

	var guessed []md.Identifier
	guessedVolume := make(map[md.Identifier]md.Identifier)
	result := make(md.ChapterList, 0, len(site))
	for _, ch := range site {
		id := ch.Info.Identifier
		ch.Info.Language = lang
		switch volume, ok := volumes[id]; {
		case id.IsSpecial():
			ch.Info.VolumeIdentifier = unvolumed()
		case ok:
			ch.Info.VolumeIdentifier = volume
		default:
			volume, certain := inferVolume(known, id)
			ch.Info.VolumeIdentifier = volume
			if !certain {
				if _, seen := guessedVolume[id]; !seen {
					guessed = append(guessed, id)
				}
				guessedVolume[id] = volume
			}
		}
		result = append(result, ch)
	}

	switch {
	case len(guessed) == 0:
	case len(guessed) <= maxListedGuesses:
		for _, id := range guessed {
			notes = append(notes, fmt.Sprintf(
				"chapter %v is not in MangaDex's volumes; its volume (%v) is a guess between two volumes",
				id, guessedVolume[id]))
		}
	default:
		sort.SliceStable(guessed, func(i, j int) bool { return guessed[i].Less(guessed[j]) })
		distinct := make(map[md.Identifier]bool)
		for _, volume := range volumes {
			distinct[volume] = true
		}
		notes = append(notes, fmt.Sprintf(
			"%d chapters (%v to %v) are not in MangaDex's volumes, which list only %d chapters in %d volumes; their volumes are guesses from the neighbouring chapters",
			len(guessed), guessed[0], guessed[len(guessed)-1], len(volumes), len(distinct)))
	}

	return result, notes
}
