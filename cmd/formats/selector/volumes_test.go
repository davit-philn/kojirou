package selector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	md "github.com/leotaku/kojirou/mangadex"
	"golang.org/x/text/language"
)

func TestParseVolumeRanges(t *testing.T) {
	got, err := parseVolumeRanges("1:1-8, 2:9..17;\n Special:100, 3:3.5-4")
	if err != nil {
		t.Fatal(err)
	}
	want := []volumeRange{{"1", 1, 8}, {"2", 9, 17}, {"Special", 100, 100}, {"3", 3.5, 4}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("range %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestVolumeRuleErrors(t *testing.T) {
	base := `{"chapter_list_selector":"a","image_list_selector":"img",`
	tests := []struct {
		name, json, want string
	}{
		{"no colon", base + `"volume_ranges":"1-8"}`, "volume:first-last"},
		{"reversed", base + `"volume_ranges":"1:9-3"}`, "after the last"},
		{"not numbers", base + `"volume_ranges":"1:a-b"}`, "plain numbers"},
		{"unsafe name", base + `"volume_ranges":"../x:1-3"}`, "letters and digits"},
		{"only separators", base + `"volume_ranges":" , ; "}`, "no ranges"},
		{"bad regex", base + `"volume_regex":"Tập(("}`, "invalid volume_regex"},
		{"no capture group", base + `"volume_regex":"Tập\\s*\\d+"}`, "capture group"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want error containing %q", err, tt.want)
			}
		})
	}

	cfg, err := ParseConfig([]byte(base + `"volume_ranges":"1:1-8","volume_regex":"Tập\\s*(\\d+)"}`))
	if err != nil || cfg.VolumeRanges != "1:1-8" {
		t.Errorf("valid rules rejected: %v", err)
	}
}

// volumesOf lists chapter number -> volume for a series page whose chapters are
// given newest first as title/URL pairs.
func volumesOf(t *testing.T, cfg SourceConfig, links [][2]string) map[string]string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(`<div class="c">`)
		for _, l := range links {
			fmt.Fprintf(&b, `<a href="%s">%s</a>`, l[1], l[0])
		}
		b.WriteString(`</div>`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()
	cfg.BaseURL = srv.URL
	cfg.ChapterListSelector = ".c a"
	cfg.ImageListSelector = "img"

	chapters, err := NewProvider(cfg, language.English).Chapters(context.Background(), srv.URL+"/s")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range chapters {
		out[c.Info.Identifier.String()] = c.Info.VolumeIdentifier.String()
	}

	return out
}

func eq(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %v, want %v", got, want)
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("chapter %s: volume %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestDefaultPutsEverythingInVolumeOne(t *testing.T) {
	got := volumesOf(t, SourceConfig{}, [][2]string{{"Chương 3", "/c/3"}, {"Chương 2", "/c/2"}, {"Chương 1", "/c/1"}})
	eq(t, got, map[string]string{"1": "1", "2": "1", "3": "1"})
}

func TestVolumeRangesSplitByChapterNumber(t *testing.T) {
	links := [][2]string{{"Chương 10", "/c/10"}, {"Chương 8.5", "/c/8.5"}, {"Chương 8", "/c/8"}, {"Chương 4", "/c/4"}, {"Chương 1", "/c/1"}}
	got := volumesOf(t, SourceConfig{VolumeRanges: "1:1-4, 2:5-9"}, links)
	// chapter 10 is outside every range and no chapters_per_volume is set
	eq(t, got, map[string]string{"1": "1", "4": "1", "8": "2", "8.5": "2", "10": "Special"})
}

func TestVolumeRegexFromTitleAndURL(t *testing.T) {
	links := [][2]string{
		{"Tập 3 - Chương 7", "/x/7"},
		{"Vol.2 Chương 5", "/x/5"},
		{"Chương 3", "/tap-1/chuong-3"},
		{"Chương 1", "/misc/1"},
	}
	got := volumesOf(t, SourceConfig{VolumeRegex: `(?i)(?:Tập\s*|Vol\.?\s*|tap-)(\d+)`}, links)
	eq(t, got, map[string]string{"7": "3", "5": "2", "3": "1", "1": "Special"})
}

func TestVolumeRulePrecedenceAndFallback(t *testing.T) {
	links := [][2]string{{"Tập 9 Chương 20", "/c/20"}, {"Chương 12", "/c/12"}, {"Chương 2", "/c/2"}, {"Chương 1", "/c/1"}}
	cfg := SourceConfig{VolumeRanges: "1:1-2", VolumeRegex: `Tập\s*(\d+)`, ChaptersPerVolume: 5}
	got := volumesOf(t, cfg, links)
	// ranges win; the regex handles chapter 20; chapter 12 matches no rule and
	// falls back to chapters_per_volume, which counts by position in the list
	eq(t, got, map[string]string{"1": "1", "2": "1", "12": "1", "20": "9"})
}

func TestInvalidRulesFailAtFetch(t *testing.T) {
	p := NewProvider(SourceConfig{ChapterListSelector: "a", ImageListSelector: "img", VolumeRanges: "bad"}, language.English)
	if _, err := p.Chapters(context.Background(), "http://127.0.0.1:1/x"); err == nil || !strings.Contains(err.Error(), "volume_ranges") {
		t.Errorf("got %v", err)
	}
	_ = md.Chapter{}
}

func TestChapterIdentifierPrefersChapterKeyword(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Chương 141", "141"},
		{"Tập 3 - Chương 7", "7"},
		{"Vol.2 Chapter 5.5: Title", "5.5"},
		{"Ch.12", "12"},
		{"Episode 9", "9"},
		{"#15 Something", "15"},
		{"Chap 3 (Tập 1)", "3"},
		{"Tập 4", "4"}, // no chapter keyword: first number
		{"Oneshot", "17"},
		{"Each 5 minutes", "5"}, // "ch" inside a word must not count as a keyword
	}
	for _, tt := range tests {
		if got := chapterIdentifier(tt.title, 17).String(); got != tt.want {
			t.Errorf("chapterIdentifier(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}
