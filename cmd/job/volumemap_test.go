package job

import (
	"context"
	"strings"
	"testing"

	"github.com/leotaku/kojirou/cmd/formats"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func TestAssignVolumes(t *testing.T) {
	vi := language.Vietnamese
	dex := md.ChapterList{
		chap("d1", "1", "1", vi), chap("d2", "2", "1", vi), chap("d3", "3", "1", vi),
		chap("d4", "4", "2", vi), chap("d5", "5", "2", vi),
		chap("d7", "7", "", vi), // MangaDex has no volume for it yet
		chap("e1", "6", "9", language.English),
	}
	site := md.ChapterList{
		chap("s1", "1", "1", vi), // site's own volume rule must be overridden
		chap("s3", "3", "1", vi), chap("s4", "4", "1", vi),
		chap("s35", "3.5", "1", vi), // between volumes 1 and 2: a guess
		chap("s45", "4.5", "1", vi), // inside volume 2
		chap("s6", "6", "1", vi),    // not volumed on MangaDex (only English has it): newer than all volumes
		chap("s9", "9", "1", vi),    // newer than all volumed chapters
	}

	got, notes := assignVolumes(dex, site, vi)

	if len(got) != len(site) {
		t.Fatalf("got %d chapters, want %d", len(got), len(site))
	}
	for i, c := range site {
		if got[i].Info.ID != c.Info.ID {
			t.Errorf("chapter %d changed identity", i)
		}
	}
	want := map[string]string{"1": "1", "3": "1", "4": "2", "3.5": "1", "4.5": "2", "6": "Special", "9": "Special"}
	for number, vol := range want {
		if g := volumeOf(got, number); g != vol {
			t.Errorf("chapter %v in volume %q, want %q", number, g, vol)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "3.5") {
		t.Errorf("notes = %q, want one guess about chapter 3.5", notes)
	}
}

func TestAssignVolumesWithoutVolumeInformation(t *testing.T) {
	vi := language.Vietnamese
	got, notes := assignVolumes(
		md.ChapterList{chap("d1", "1", "", vi)},
		md.ChapterList{chap("s1", "1", "1", vi), chap("s2", "2", "1", vi)},
		vi)
	for _, c := range got {
		if v := c.Info.VolumeIdentifier.String(); v != "Special" {
			t.Errorf("chapter %v in volume %q, want Special", c.Info.Identifier, v)
		}
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "no volume information") {
		t.Errorf("notes = %q, want a hint that MangaDex has no volumes", notes)
	}
}

type recordingSource struct {
	name     string
	manga    md.Manga
	chapters md.ChapterList
	asked    []string
}

func (f *recordingSource) Name() string { return f.name }
func (f *recordingSource) Skeleton(_ context.Context, id string) (*md.Manga, error) {
	f.asked = append(f.asked, "skeleton:"+id)
	m := f.manga
	return &m, nil
}
func (f *recordingSource) Chapters(_ context.Context, id string) (md.ChapterList, error) {
	f.asked = append(f.asked, "chapters:"+id)
	return f.chapters, nil
}
func (f *recordingSource) Covers(context.Context, *md.Manga, formats.Progress) (md.ImageList, error) {
	f.asked = append(f.asked, "covers")
	return nil, nil
}
func (f *recordingSource) Pages(context.Context, md.ChapterList, formats.Progress) (md.ImageList, error) {
	f.asked = append(f.asked, "pages")
	return nil, nil
}

func TestVolumeMapSourceAddressesEachSideByItsOwnIdentifier(t *testing.T) {
	vi := language.Vietnamese
	site := &recordingSource{name: "selector", chapters: md.ChapterList{chap("s1", "1", "1", vi)}}
	dex := &recordingSource{
		name:     "mangadex",
		manga:    md.Manga{Info: md.MangaInfo{Title: "Blue Lock", ID: "dex-id"}},
		chapters: md.ChapterList{chap("d1", "1", "4", vi)},
	}
	src := newVolumeMapSource(site, dex, "https://site/series", "dex-id", vi)

	manga, err := src.Skeleton(context.Background(), "https://site/series")
	if err != nil || manga.Info.ID != "dex-id" || manga.Info.Title != "Blue Lock" {
		t.Fatalf("skeleton = %+v, %v; want MangaDex's series", manga, err)
	}
	chapters, err := src.Chapters(context.Background(), manga.Info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v := volumeOf(chapters, "1"); v != "4" {
		t.Errorf("chapter 1 in volume %q, want 4 (from MangaDex)", v)
	}
	if chapters[0].Info.ID != "s1" {
		t.Errorf("chapter comes from %q, want the site", chapters[0].Info.ID)
	}
	if got := strings.Join(site.asked, ","); got != "chapters:https://site/series" {
		t.Errorf("site was asked %q", got)
	}
	if got := strings.Join(dex.asked, ","); got != "skeleton:dex-id,chapters:dex-id" {
		t.Errorf("MangaDex was asked %q", got)
	}
}

func TestVolumesFromNeedsAWebsite(t *testing.T) {
	err := Options{Identifier: "x", Format: FormatCBZ, VolumesFrom: "abc"}.validate()
	if err == nil || !strings.Contains(err.Error(), "website") {
		t.Errorf("err = %v, want a hint that a website is needed", err)
	}
}
