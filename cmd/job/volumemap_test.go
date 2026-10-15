package job

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/leotaku/kojirou/cmd/formats"
	"github.com/leotaku/kojirou/cmd/formats/wikipedia"
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
	if len(notes) != 1 || !strings.Contains(notes[0], "37 chapters (1 to 39)") || !strings.Contains(notes[0], "only 3 chapters in 2 volumes") {
		t.Errorf("notes = %q, want one summary of 37 chapters from 3 listed ones", notes)
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

// hangingCovers never answers until its context ends, like a stalled server.
type hangingCovers struct{ *recordingSource }

func (hangingCovers) Covers(ctx context.Context, _ *md.Manga, _ formats.Progress) (md.ImageList, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCoversOfAVolumeMapSourceDoNotWaitForever(t *testing.T) {
	old := coverTimeout
	coverTimeout = 50 * time.Millisecond
	t.Cleanup(func() { coverTimeout = old })

	dex := hangingCovers{&recordingSource{name: "mangadex"}}
	src := newVolumeMapSource(&recordingSource{name: "selector"}, dex, dex.recordingSource, "https://site/s", "dex-id", language.Vietnamese)

	done := make(chan error, 1)
	go func() {
		_, err := getCovers(context.Background(), src, Options{}, NopReporter{}, &md.Manga{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want the timeout as an error so that Run can carry on without covers")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("getCovers still waits for the covers")
	}
}

type fakeTabler struct {
	table *wikipedia.Table
	err   error
	asked string
}

func (f *fakeTabler) VolumeTable(_ context.Context, title string) (*wikipedia.Table, error) {
	f.asked = title
	return f.table, f.err
}

func wikiSource(t *testing.T, dexVolumes map[md.Identifier]md.Identifier, wiki *fakeTabler, site md.ChapterList) *volumeMapSource {
	t.Helper()
	dex := &recordingSource{
		name:    "mangadex",
		manga:   md.Manga{Info: md.MangaInfo{Title: "Blue Lock", ID: "dex-id"}},
		volumes: dexVolumes,
	}
	src := newVolumeMapSource(&recordingSource{name: "selector", chapters: site}, dex, dex, "https://site/s", "dex-id", language.Vietnamese)
	src.wiki = wiki
	if _, err := src.Skeleton(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	return src
}

func TestVolumeMapUsesWikipediaForWhatMangaDexLacks(t *testing.T) {
	vi := language.Vietnamese
	site := md.ChapterList{chap("s1", "1", "1", vi), chap("s5", "5", "1", vi), chap("s9", "9", "1", vi), chap("s12", "12", "1", vi)}
	wiki := &fakeTabler{table: &wikipedia.Table{
		Page:    "List of Blue Lock chapters",
		Volumes: volumeMap("1", "1", "2", "1", "3", "1", "4", "1", "5", "2", "6", "2", "9", "3", "10", "3"),
	}}
	// MangaDex only knows chapter 1 and disagrees about chapter 9.
	src := wikiSource(t, volumeMap("1", "1", "9", "7"), wiki, site)

	chapters, err := src.Chapters(context.Background(), "dex-id")
	if err != nil {
		t.Fatal(err)
	}
	if wiki.asked != "Blue Lock" {
		t.Errorf("Wikipedia was asked for %q, want MangaDex's title", wiki.asked)
	}
	want := map[string]string{"1": "1", "5": "2", "9": "7", "12": "Special"}
	for number, vol := range want {
		if g := volumeOf(chapters, number); g != vol {
			t.Errorf("chapter %s in volume %q, want %q (MangaDex first, then Wikipedia)", number, g, vol)
		}
	}
	if len(src.Notes()) != 0 {
		t.Errorf("notes = %q, want none", src.Notes())
	}
	if info := src.Info(); len(info) != 1 || !strings.Contains(info[0], "List of Blue Lock chapters") || !strings.Contains(info[0], "Volumes of 6 chapters") {
		t.Errorf("info = %q", info)
	}
}

func TestVolumeMapWikipediaFailureIsOnlyMentionedWhenItMatters(t *testing.T) {
	vi := language.Vietnamese
	failing := func() *fakeTabler { return &fakeTabler{err: fmt.Errorf("no page")} }

	complete := wikiSource(t, volumeMap("1", "1", "2", "1"), failing(), md.ChapterList{chap("s1", "1", "1", vi), chap("s2", "2", "1", vi)})
	if _, err := complete.Chapters(context.Background(), "dex-id"); err != nil || len(complete.Notes()) != 0 {
		t.Errorf("err %v notes %q: a failing Wikipedia must be silent when MangaDex knows everything", err, complete.Notes())
	}

	gaps := wikiSource(t, volumeMap("1", "1", "9", "2"), failing(), md.ChapterList{chap("s1", "1", "1", vi), chap("s5", "5", "1", vi)})
	if _, err := gaps.Chapters(context.Background(), "dex-id"); err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(gaps.Notes(), "|")
	if !strings.Contains(notes, "chapter 5") || !strings.Contains(notes, "Wikipedia could not supply the volumes (no page)") {
		t.Errorf("notes = %q, want the guess and why Wikipedia did not help", notes)
	}
}
