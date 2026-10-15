package wikipedia

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	md "github.com/leotaku/kojirou/mangadex"
)

// A trimmed copy of the structure of the real "List of Blue Lock chapters".
const sample = `''[[Blue Lock]]'' is a manga.<ref name="x"/>

__TOC__

== ''Blue Lock'' volumes ==
{{Graphic novel list/header
|OneLanguage     = no
}}
{{Graphic novel list
|VolumeNumber    = 1
|OriginalRelDate = November 16, 2018<ref name="JP-vol1">{{cite web|url=https://example.org}}</ref>
|ChapterListCol1 =
*1. {{nihongo|"Dream"|夢|Yume}}
*2. {{nihongo|"Moving In"|入寮|Nyūryō}}
|ChapterListCol2 =
*3. {{nihongo|"Monster"|かいぶつ|Kaibutsu}}
*4. {{nihongo|"Right Now"|今こそ|Ima Koso}}
}}
{{Graphic novel list
|VolumeNumber    = 2
|ChapterListCol1 =
*5. {{nihongo|"The "Zero" Soccer"|サッカーの0|Sakkā no Zero}}
*6. {{nihongo|"1 {{=}} Individual"|1 {{=}} 個性|Ichi {{=}} Kosei}}
|ChapterListCol2 =
*6.5. {{nihongo|"Extra"|x|x}}
}}
{{Graphic novel list/footer}}

=== Chapters not yet in ''tankōbon'' format ===
*7. {{nihongo|"Not collected"|x|x}}

== ''Blue Lock: Episode Nagi'' volumes ==
{{Graphic novel list
|VolumeNumber    = 1
|ChapterListCol1 =
*1. {{nihongo|"Nagi"|x|x}}
}}
{{Graphic novel list
|VolumeNumber    = 2
|ChapterListCol1 =
*2. {{nihongo|"Nagi 2"|x|x}}
}}
{{Graphic novel list
|VolumeNumber    = 3
|ChapterListCol1 =
*3. {{nihongo|"Nagi 3"|x|x}}
}}

== References ==
{{reflist}}
`

func volumeOf(m map[md.Identifier]md.Identifier, chapter string) string {
	v, ok := m[md.NewIdentifier(chapter)]
	if !ok {
		return "<none>"
	}

	return v.String()
}

func TestParseVolumes(t *testing.T) {
	got := ParseVolumes(sample, "Blue Lock")
	want := map[string]string{"1": "1", "2": "1", "3": "1", "4": "1", "5": "2", "6": "2", "6.5": "2", "7": "<none>"}
	for chapter, volume := range want {
		if g := volumeOf(got, chapter); g != volume {
			t.Errorf("chapter %s in volume %s, want %s", chapter, g, volume)
		}
	}
	if len(got) != 7 {
		t.Errorf("got %d chapters, want 7 (the spin-off must not leak in)", len(got))
	}

	// With an unrelated title the section with the most volumes (the spin-off here) wins.
	if g := ParseVolumes(sample, "Something Else"); volumeOf(g, "3") != "3" {
		t.Errorf("fallback did not pick the section with most volumes: %v", g)
	}
	if g := ParseVolumes("no tables here", "x"); len(g) != 0 {
		t.Errorf("got %v for a page without tables", g)
	}
}

func fakeWikipedia(t *testing.T, searchTitles ...string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "kojirou") {
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		q := r.URL.Query()
		switch q.Get("action") {
		case "query":
			var hits []map[string]string
			for _, title := range searchTitles {
				hits = append(hits, map[string]string{"title": title})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": hits}})
		case "parse":
			text := "nothing useful"
			if q.Get("page") == "List of Blue Lock chapters" {
				text = sample
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"parse": map[string]string{"wikitext": text}})
		}
	}))
	t.Cleanup(srv.Close)

	return NewWithAPI(srv.URL)
}

func TestVolumeTableFindsTheRightPage(t *testing.T) {
	c := fakeWikipedia(t, "List of Blue Lock characters", "List of Another Series chapters", "List of Blue Lock chapters")
	table, err := c.VolumeTable(context.Background(), "Blue Lock")
	if err != nil {
		t.Fatal(err)
	}
	if table.Page != "List of Blue Lock chapters" || volumeOf(table.Volumes, "5") != "2" {
		t.Errorf("table = %+v", table)
	}
}

func TestVolumeTableFailures(t *testing.T) {
	if _, err := fakeWikipedia(t).VolumeTable(context.Background(), "Blue Lock"); err == nil {
		t.Error("no search hits must be an error")
	}
	if _, err := fakeWikipedia(t, "List of Blue Lock characters").VolumeTable(context.Background(), "Blue Lock"); err == nil {
		t.Error("a page that is not a chapter list must be an error")
	}
	if _, err := fakeWikipedia(t, "List of Blue Lock chapters").VolumeTable(context.Background(), " "); err == nil {
		t.Error("an empty title must be an error")
	}
	if _, err := NewWithAPI("http://127.0.0.1:1/").VolumeTable(context.Background(), "x"); err == nil {
		t.Error("an unreachable server must be an error")
	}
}
