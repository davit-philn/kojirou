package job

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/leotaku/kojirou/cmd/formats"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func volumeMap(pairs ...string) map[md.Identifier]md.Identifier {
	m := make(map[md.Identifier]md.Identifier)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[md.NewIdentifier(pairs[i])] = md.NewIdentifier(pairs[i+1])
	}

	return m
}

func TestAssignVolumes(t *testing.T) {
	vi := language.Vietnamese
	volumes := volumeMap("1", "1", "2", "1", "3", "1", "4", "2", "5", "2")
	site := md.ChapterList{
		chap("s1", "1", "1", vi), // the site's own volume rule must be overridden
		chap("s3", "3", "1", vi), chap("s4", "4", "1", vi),
		chap("s35", "3.5", "1", vi), // between volumes 1 and 2: a guess
		chap("s45", "4.5", "1", vi), // inside volume 2
		chap("s6", "6", "1", vi),    // newer than every volume
	}

	got, notes := assignVolumes(volumes, site, vi)

	if len(got) != len(site) {
		t.Fatalf("got %d chapters, want %d", len(got), len(site))
	}
	for i, c := range site {
		if got[i].Info.ID != c.Info.ID {
			t.Errorf("chapter %d changed identity", i)
		}
	}
	want := map[string]string{"1": "1", "3": "1", "4": "2", "3.5": "1", "4.5": "2", "6": "Special"}
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
	got, notes := assignVolumes(nil, md.ChapterList{chap("s1", "1", "1", vi), chap("s2", "2", "1", vi)}, vi)
	for _, c := range got {
		if v := c.Info.VolumeIdentifier.String(); v != "Special" {
			t.Errorf("chapter %v in volume %q, want Special", c.Info.Identifier, v)
		}
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "no volume information") {
		t.Errorf("notes = %q, want a hint that MangaDex has no volumes", notes)
	}
}

// MangaDex often knows only the later volumes of a series; the earlier
// chapters then get the first known volume, with one summarising note rather
// than one note per chapter.
func TestAssignVolumesSummarisesManyGuesses(t *testing.T) {
	vi := language.Vietnamese
	var site md.ChapterList
	for n := 1; n <= 40; n++ {
		site = append(site, chap(fmt.Sprintf("s%d", n), fmt.Sprint(n), "1", vi))
	}
	got, notes := assignVolumes(volumeMap("30", "3", "31", "3", "40", "4"), site, vi)

	if v := volumeOf(got, "1"); v != "3" {
		t.Errorf("chapter 1 in volume %q, want 3", v)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "37 chapters (1 to 39)") {
		t.Errorf("notes = %q, want one summary of 37 chapters", notes)
	}
}

type recordingSource struct {
	name     string
	manga    md.Manga
	chapters md.ChapterList
	volumes  map[md.Identifier]md.Identifier
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
func (f *recordingSource) VolumeMap(_ context.Context, id string) (map[md.Identifier]md.Identifier, error) {
	f.asked = append(f.asked, "volumes:"+id)
	return f.volumes, nil
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
		name:    "mangadex",
		manga:   md.Manga{Info: md.MangaInfo{Title: "Blue Lock", ID: "dex-id"}},
		volumes: volumeMap("1", "4"),
	}
	src := newVolumeMapSource(site, dex, dex, "https://site/series", "dex-id", vi)

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
	if got := strings.Join(dex.asked, ","); got != "skeleton:dex-id,volumes:dex-id" {
		t.Errorf("MangaDex was asked %q", got)
	}
}

func TestVolumesFromNeedsAWebsite(t *testing.T) {
	err := Options{Identifier: "x", Format: FormatCBZ, VolumesFrom: "abc"}.validate()
	if err == nil || !strings.Contains(err.Error(), "website") {
		t.Errorf("err = %v, want a hint that a website is needed", err)
	}
}
