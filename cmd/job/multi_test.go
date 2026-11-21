package job

import (
	"context"
	"sort"
	"strings"
	"testing"

	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func newTestMulti(cfg MultiSource, members ...contentSource) *multiSource {
	s := &multiSource{cfg: cfg, lang: language.Vietnamese, origin: map[string]multiOrigin{}, members: members}
	return s
}

func TestMultiSourceTakesEachChapterFromTheBestSourceThatHasIt(t *testing.T) {
	vi := language.Vietnamese
	a := &fakeSource{name: "a", chapters: md.ChapterList{chap("a1", "1", "1", vi), chap("a2", "2", "1", vi), chap("a4", "4", "1", vi)}}
	b := &fakeSource{name: "b", chapters: md.ChapterList{chap("b2", "2", "1", vi), chap("b3", "3", "1", vi), chap("b4", "4", "1", vi), chap("b5", "5", "1", vi)}}
	c := &fakeSource{name: "c", chapters: md.ChapterList{chap("c5", "5", "1", vi), chap("c6", "6", "1", vi), chap("e7", "7", "1", language.English)}}
	cfg := MultiSource{
		Title:   "Demo",
		Members: []MultiMember{{Name: "a", ID: "A"}, {Name: "b", ID: "B"}, {Name: "c", ID: "C"}},
		Choice:  map[string]int{"4": 1}, // chapter 4 by hand from b
	}
	s := newTestMulti(cfg, a, b, c)

	manga, err := s.Skeleton(context.Background(), "x")
	if err != nil || manga.Info.Title != "Demo" {
		t.Fatalf("skeleton = %+v, %v", manga, err)
	}
	chapters, err := s.Chapters(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 6 {
		t.Fatalf("%d chapters, want 1..6 (the English 7 does not count): %+v", len(chapters), chapters)
	}
	want := map[string]int{"1": 0, "2": 0, "3": 1, "4": 1, "5": 1, "6": 2}
	for _, ch := range chapters {
		o := s.origin[ch.Info.ID]
		if w := want[ch.Info.Identifier.String()]; o.member != w {
			t.Errorf("chapter %v comes from member %d, want %d", ch.Info.Identifier, o.member, w)
		}
	}

	if _, err := s.Pages(context.Background(), chapters, nopProgress{}); err != nil {
		t.Fatal(err)
	}
	// Each source is asked for the chapters it was chosen for, under its own IDs.
	sort.Strings(a.asked)
	sort.Strings(b.asked)
	if strings.Join(a.asked, ",") != "a1,a2" || strings.Join(b.asked, ",") != "b3,b4,b5" || strings.Join(c.asked, ",") != "c6" {
		t.Errorf("asked: a=%v b=%v c=%v", a.asked, b.asked, c.asked)
	}
}

func TestMultiSourceVolumesByCountAndOneFailingSource(t *testing.T) {
	vi := language.Vietnamese
	good := &fakeSource{name: "good", chapters: md.ChapterList{chap("g1", "1", "1", vi), chap("g8", "8", "1", vi), chap("g9", "9", "1", vi), chap("g20", "20", "1", vi)}}
	s := newTestMulti(MultiSource{Members: []MultiMember{{Name: "bad", ID: "B"}, {Name: "good", ID: "G"}}, ChaptersPerVolume: 8}, &errSource{}, good)
	chapters, err := s.Chapters(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ch := range chapters {
		got[ch.Info.Identifier.String()] = ch.Info.VolumeIdentifier.String()
	}
	if got["1"] != "1" || got["8"] != "1" || got["9"] != "2" || got["20"] != "3" {
		t.Errorf("volumes = %v", got)
	}
	if !strings.Contains(strings.Join(s.Notes(), "|"), "bad:") {
		t.Errorf("the failing source is not reported: %v", s.Notes())
	}

	// With every source failing it is an error.
	if _, err := newTestMulti(MultiSource{Members: []MultiMember{{Name: "bad", ID: "B"}}}, &errSource{}).Chapters(context.Background(), "x"); err == nil {
		t.Error("expected an error when no source can be read")
	}
}

type errSource struct{ fakeSource }

func (*errSource) Chapters(context.Context, string) (md.ChapterList, error) {
	return nil, context.DeadlineExceeded
}

func TestOptionsValidateMultiSource(t *testing.T) {
	ok := MultiSource{Members: []MultiMember{{Name: "a", ID: "http://x/y"}}}
	if err := (Options{Identifier: "x", Format: FormatCBZ, Multi: &ok}).validate(); err != nil {
		t.Errorf("valid options rejected: %v", err)
	}
	for name, m := range map[string]MultiSource{"no members": {}, "no id": {Members: []MultiMember{{Name: "a"}}}} {
		m := m
		if err := (Options{Identifier: "x", Format: FormatCBZ, Multi: &m}).validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestVolumeRangesPutChaptersIntoVolumesByHand(t *testing.T) {
	vi := language.Vietnamese
	chapters := md.ChapterList{chap("a1", "1", "9", vi), chap("a8", "8", "9", vi), chap("a9", "9", "9", vi), chap("a95", "9.5", "9", vi), chap("a20", "20", "9", vi)}
	applyVolumeRanges(chapters, "1:1-8, 2:9-17; 3:18")
	got := map[string]string{}
	for _, c := range chapters {
		got[c.Info.Identifier.String()] = c.Info.VolumeIdentifier.String()
	}
	if got["1"] != "1" || got["8"] != "1" || got["9"] != "2" || got["9.5"] != "2" || got["20"] != "9" {
		t.Errorf("volumes = %v (chapter 20 is in no range and keeps its volume)", got)
	}
	for _, bad := range []string{"1-8", "x:1-8", "1:8-1", "1:a"} {
		if _, err := parseVolumeRanges(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if err := (Options{Identifier: "x", Format: FormatCBZ, Multi: &MultiSource{Members: []MultiMember{{Name: "a", ID: "u"}}, VolumeRanges: "oops"}}).Validate(); err == nil {
		t.Error("options with bad ranges must not validate")
	}
}
