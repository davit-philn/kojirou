package job

import (
	"context"
	"image"
	"strings"
	"testing"

	"github.com/leotaku/kojirou/cmd/formats"
	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func chap(id, number, volume string, lang language.Tag) md.Chapter {
	vol := md.NewWithFallback(volume, "Special")
	return md.Chapter{Info: md.ChapterInfo{
		ID:               id,
		Identifier:       md.NewIdentifier(number),
		VolumeIdentifier: vol,
		Language:         lang,
		GroupNames:       []string{"g-" + id[:1]},
	}}
}

func volumeOf(cl md.ChapterList, number string) string {
	for _, c := range cl {
		if c.Info.Identifier.Equal(md.NewIdentifier(number)) {
			return c.Info.VolumeIdentifier.String()
		}
	}

	return "<missing>"
}

func TestFillGaps(t *testing.T) {
	en, vi := language.English, language.Vietnamese
	main := md.ChapterList{
		chap("m1", "1", "1", vi), chap("m2", "2", "1", vi), chap("m3", "3", "1", vi),
		chap("m4", "4", "2", vi), chap("m5", "5", "2", vi),
		chap("m9", "9", "", vi),  // not in a volume yet
		chap("e2", "7", "9", en), // other language: must not count as present
	}
	extra := md.ChapterList{
		chap("x2", "2", "", vi),    // duplicate of a main chapter: ignored
		chap("x35", "3.5", "", vi), // between volume 1 and 2: a guess
		chap("x45", "4.5", "", vi), // inside volume 2? no: between 4 and 5, same volume
		chap("x7", "7", "", vi),    // missing in Vietnamese (only English exists)
		chap("x8", "8", "", vi),    // between volume 2 and the unvolumed chapter 9
		chap("x10", "10", "", vi),  // newer than everything
		chap("x10b", "10", "", vi), // duplicate inside the extra source
	}

	got, notes := fillGaps(main, extra, vi)

	if len(got) != len(main)+5 {
		t.Fatalf("got %d chapters, want %d (main + 3.5, 4.5, 7, 8, 10)", len(got), len(main)+5)
	}
	for i, c := range main {
		if got[i].Info.ID != c.Info.ID {
			t.Errorf("main chapter %d was changed or reordered", i)
		}
	}
	want := map[string]string{
		"3.5": "1",       // lo=3 (vol 1), hi=4 (vol 2): guess, earlier volume
		"4.5": "2",       // between two chapters of volume 2: certain
		"7":   "2",       // lo=5 (vol 2), hi=9 (Special): guess, earlier volume
		"8":   "2",       // same
		"10":  "Special", // newer than all known chapters
	}
	for number, vol := range want {
		if g := volumeOf(got[len(main):], number); g != vol {
			t.Errorf("chapter %v placed in volume %q, want %q", number, g, vol)
		}
	}
	for _, c := range got[len(main):] {
		if c.Info.Language != vi {
			t.Errorf("added chapter %v has language %v", c.Info.Identifier, c.Info.Language)
		}
	}

	joined := strings.Join(notes, "\n")
	for _, number := range []string{"3.5", "7", "8"} {
		if !strings.Contains(joined, "chapter "+number+" ") {
			t.Errorf("expected a note about guessed chapter %v, notes: %v", number, notes)
		}
	}
	for _, number := range []string{"4.5", "10"} {
		if strings.Contains(joined, "chapter "+number+" ") {
			t.Errorf("chapter %v is certain but was reported as a guess", number)
		}
	}
}

func TestFillGapsBeforeFirstChapterAndEmptyMain(t *testing.T) {
	vi := language.Vietnamese
	main := md.ChapterList{chap("m5", "5", "2", vi)}
	got, notes := fillGaps(main, md.ChapterList{chap("x1", "1", "", vi)}, vi)
	if v := volumeOf(got[1:], "1"); v != "2" || len(notes) != 1 {
		t.Errorf("chapter before the first known one: volume %q, notes %v", v, notes)
	}

	got, notes = fillGaps(nil, md.ChapterList{chap("x1", "1", "", vi)}, vi)
	if v := volumeOf(got, "1"); v != "Special" || len(notes) != 0 {
		t.Errorf("with an empty main source: volume %q, notes %v", v, notes)
	}
}

// fakeSource records which chapters it was asked to download.
type fakeSource struct {
	name     string
	chapters md.ChapterList
	asked    []string
	coverFor []md.Identifier
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Skeleton(_ context.Context, id string) (*md.Manga, error) {
	return &md.Manga{Info: md.MangaInfo{Title: f.name + " title", ID: id}, Volumes: map[md.Identifier]md.Volume{}}, nil
}
func (f *fakeSource) Chapters(context.Context, string) (md.ChapterList, error) {
	return f.chapters, nil
}
func (f *fakeSource) Covers(_ context.Context, m *md.Manga, _ formats.Progress) (md.ImageList, error) {
	for id := range m.Volumes {
		f.coverFor = append(f.coverFor, id)
	}
	return md.ImageList{}, nil
}
func (f *fakeSource) Pages(_ context.Context, cl md.ChapterList, _ formats.Progress) (md.ImageList, error) {
	images := md.ImageList{}
	for _, c := range cl {
		f.asked = append(f.asked, c.Info.ID)
		images = append(images, md.Image{
			Image:             image.NewGray(image.Rect(0, 0, 1, 1)),
			ChapterIdentifier: c.Info.Identifier,
			VolumeIdentifier:  c.Info.VolumeIdentifier,
		})
	}
	return images, nil
}

func TestFillSourceRoutesPagesAndCovers(t *testing.T) {
	vi := language.Vietnamese
	main := &fakeSource{name: "main", chapters: md.ChapterList{chap("m1", "1", "1", vi), chap("m3", "3", "1", vi)}}
	extra := &fakeSource{name: "extra", chapters: md.ChapterList{chap("x1", "1", "", vi), chap("x2", "2", "", vi)}}
	s := newFillSource(main, extra, "https://example.org/series", vi)

	manga, err := s.Skeleton(context.Background(), "main-id")
	if err != nil || manga.Info.Title != "main title" {
		t.Fatalf("skeleton must come from the main source: %+v, %v", manga, err)
	}
	chapters, err := s.Chapters(context.Background(), "main-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 3 || volumeOf(chapters, "2") != "1" {
		t.Fatalf("chapters = %+v", chapters)
	}

	images, err := s.Pages(context.Background(), chapters, nopProgress{})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 3 {
		t.Errorf("got %d images", len(images))
	}
	if strings.Join(main.asked, ",") != "m1,m3" || strings.Join(extra.asked, ",") != "x2" {
		t.Errorf("pages routed wrongly: main %v, extra %v", main.asked, extra.asked)
	}

	if _, err := s.Covers(context.Background(), &md.Manga{Volumes: map[md.Identifier]md.Volume{md.NewIdentifier("1"): {}}}, nopProgress{}); err != nil {
		t.Fatal(err)
	}
	if len(main.coverFor) != 1 || len(extra.coverFor) != 0 {
		t.Errorf("covers must come from the main source only")
	}
}

func TestOptionsValidateFillSource(t *testing.T) {
	good := FillSource{URL: "https://example.org/series"}
	good.Config.ChapterListSelector = "a"
	good.Config.ImageListSelector = "img"

	tests := []struct {
		name   string
		mutate func(*Options)
		want   string
	}{
		{"with selector main source", func(o *Options) { o.Source = &good.Config }, "main source"},
		{"bad url", func(o *Options) { f := good; f.URL = "file:///x"; o.Fill = &f }, "http(s) URL"},
		{"bad selectors", func(o *Options) { f := good; f.Config.ChapterListSelector = "a["; o.Fill = &f }, "fill source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{Identifier: "x", Format: FormatCBZ, Fill: &good}
			tt.mutate(&o)
			err := o.validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
	if err := (Options{Identifier: "x", Format: FormatCBZ, Fill: &good}).validate(); err != nil {
		t.Errorf("valid fill options rejected: %v", err)
	}
}
