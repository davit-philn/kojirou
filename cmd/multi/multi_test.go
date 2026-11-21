package multi

import (
	"reflect"
	"testing"
)

func TestNormalizeIgnoresCaseAccentsPunctuationAndBrackets(t *testing.T) {
	for _, title := range []string{"Blue Lock", "BLUE-LOCK", "  blue   lock  ", "Blue Lock (Tiếng Việt)", "Blue Lock [Raw]"} {
		if got := Normalize(title); got != "blue lock" {
			t.Errorf("Normalize(%q) = %q", title, got)
		}
	}
	if got := Normalize("Đấu La Đại Lục"); got != "dau la dai luc" {
		t.Errorf("got %q", got)
	}
}

func TestGroupCandidatesJoinsTheSameSeriesAndKeepsSpinOffsApart(t *testing.T) {
	groups := GroupCandidates([]Candidate{
		{Source: "a", Title: "Blue Lock", ID: "a1"},
		{Source: "b", Title: "BLUE LOCK (Tiếng Việt)", ID: "b1"},
		{Source: "mangadex", Title: "Blue Lock", ID: "m1"},
		{Source: "c", Title: "Blue Lock: Episode Nagi", ID: "c1"},
		{Source: "a", Title: "Lock Blue", ID: "a2"}, // same words, other order
		{Source: "b", Title: "Other", ID: "b2"},
	})
	if len(groups) != 3 {
		t.Fatalf("%d groups: %+v", len(groups), groups)
	}
	first := groups[0]
	if first.Sources != 3 || len(first.Members) != 4 || first.Title != "Blue Lock" {
		t.Errorf("first group = %+v", first)
	}
	for _, g := range groups[1:] {
		if g.Sources != 1 {
			t.Errorf("%+v should hold one source", g)
		}
	}
}

func ch(numbers ...string) []Chapter {
	out := make([]Chapter, len(numbers))
	for i, n := range numbers {
		out[i] = Chapter{Number: n, ID: "id" + n}
	}
	return out
}

func TestMergeShowsWhoHasWhatAndWhatIsMissing(t *testing.T) {
	table := Merge(map[string][]Chapter{
		"a":        ch("1", "2", "3", "5", "6"),
		"b":        ch("3", "4", "6", "7", "7.5", "Extra"),
		"mangadex": ch("1", "2", "3", "4", "5", "6", "7", "8"),
	})
	if table.First != "1" || table.Last != "8" {
		t.Errorf("range %s..%s", table.First, table.Last)
	}
	if len(table.Gaps) != 0 {
		t.Errorf("gaps = %v, want none: mangadex has every number", table.Gaps)
	}
	if len(table.Others) != 1 || table.Others[0].Number != "Extra" {
		t.Errorf("others = %+v", table.Others)
	}
	byNumber := map[string]Row{}
	for _, r := range table.Rows {
		byNumber[r.Number] = r
	}
	if len(byNumber["3"].Sources) != 3 || len(byNumber["7.5"].Sources) != 1 {
		t.Errorf("sources of 3 / 7.5: %+v %+v", byNumber["3"].Sources, byNumber["7.5"].Sources)
	}
	stats := map[string]SourceStat{}
	for _, s := range table.Stats {
		stats[s.Source] = s
	}
	if !reflect.DeepEqual(stats["a"].Missing, []string{"4"}) || stats["a"].Last != "6" {
		t.Errorf("a = %+v", stats["a"])
	}
	if !reflect.DeepEqual(stats["b"].Missing, []string{"1-2", "5"}) {
		t.Errorf("b = %+v", stats["b"])
	}
}

func TestMergeFindsChaptersNoSourceHas(t *testing.T) {
	table := Merge(map[string][]Chapter{
		"a": ch("1", "2", "5", "6", "9"),
		"b": ch("2", "3", "9", "10"),
	})
	if want := []string{"4", "7-8"}; !reflect.DeepEqual(table.Gaps, want) {
		t.Errorf("gaps = %v, want %v", table.Gaps, want)
	}
	if table.Last != "10" {
		t.Errorf("last = %s", table.Last)
	}
	if got := Merge(nil); len(got.Rows) != 0 || len(got.Gaps) != 0 {
		t.Errorf("empty merge = %+v", got)
	}
}

func TestRankPutsTheTitleThatContainsTheSearchFirst(t *testing.T) {
	groups := []Group{
		{Title: "Unrelated", Sources: 3},
		{Title: "Blue Lock", Sources: 1},
		{Title: "Blue Lock Episode Nagi", Sources: 2},
	}
	Rank(groups, "blue lock")
	if groups[0].Title != "Blue Lock Episode Nagi" || groups[1].Title != "Blue Lock" || groups[2].Title != "Unrelated" {
		t.Errorf("order = %+v", groups)
	}
}
