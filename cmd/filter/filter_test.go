package filter

import (
	"testing"
	"time"

	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"foo", "a foo b", true},
		{"^foo$", "a foo b", false},
		{"!foo", "a foo b", false},
		{"!foo", "bar", true},
		{"(", "anything", false}, // invalid regex never matches
	}
	for _, tt := range tests {
		if got := MatchPattern(tt.pattern, tt.s); got != tt.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

func chapter(id string, lang language.Tag, published time.Time) md.Chapter {
	return md.Chapter{Info: md.ChapterInfo{
		ID:         id,
		Language:   lang,
		Published:  published,
		Identifier: md.NewIdentifier(id),
	}}
}

func ids(cl md.ChapterList) []string {
	out := make([]string, len(cl))
	for i, c := range cl {
		out[i] = c.Info.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterByLanguage(t *testing.T) {
	now := time.Now()
	cl := md.ChapterList{
		chapter("1", language.English, now),
		chapter("2", language.Japanese, now),
		chapter("3", language.English, now),
	}
	got := ids(FilterByLanguage(cl, language.English))
	if want := []string{"1", "3"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFilterByIdentifier(t *testing.T) {
	now := time.Now()
	cl := md.ChapterList{
		chapter("1", language.English, now),
		chapter("2", language.English, now),
		chapter("3", language.English, now),
	}
	got := ids(FilterByIdentifier(cl, "Identifier", ParseRanges("2..3")))
	if want := []string{"2", "3"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortByNewest(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cl := md.ChapterList{
		chapter("old", language.English, base),
		chapter("new", language.English, base.Add(48*time.Hour)),
		chapter("mid", language.English, base.Add(24*time.Hour)),
	}
	got := ids(SortByNewest(cl))
	if want := []string{"new", "mid", "old"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
