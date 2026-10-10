package mangadex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

const searchJSON = `{"result":"ok","limit":24,"offset":0,"total":2,"data":[
 {"id":"aaa","type":"manga","attributes":{
   "title":{"ja-ro":"Romaji Title","en":"English Title"},
   "description":{},
   "status":"ongoing","year":2020,
   "availableTranslatedLanguages":["vi","en"]},
  "relationships":[
   {"id":"c1","type":"cover_art","attributes":{"fileName":"cover one.jpg"}},
   {"id":"u1","type":"author","attributes":{"name":"Jane Doe"}},
   {"id":"u2","type":"artist"}]},
 {"id":"bbb","type":"manga","attributes":{"title":{"ja":"Only Japanese"},"description":{"en":"Hello"}},"relationships":[]}
]}`

func newSearchClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL + "/")
	covers, _ := url.Parse("https://covers.example.org/covers/")

	return NewClient().WithBaseURLs(*base, *covers)
}

func TestSearch(t *testing.T) {
	var query url.Values
	c := newSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		_, _ = w.Write([]byte(searchJSON))
	})

	list, total, err := c.Search(context.Background(), SearchParams{Title: "dragon", Language: "vi"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("total=%d len=%d", total, len(list))
	}

	got := list[0]
	if got.Title != "English Title" || got.Description != "" || got.Year != 2020 {
		t.Errorf("unexpected summary: %+v", got)
	}
	if !reflect.DeepEqual(got.Authors, []string{"Jane Doe"}) || !reflect.DeepEqual(got.Languages, []string{"en", "vi"}) {
		t.Errorf("authors/languages: %+v", got)
	}
	if want := "https://covers.example.org/covers/aaa/cover%20one.jpg.256.jpg"; got.CoverURL != want {
		t.Errorf("cover = %q, want %q", got.CoverURL, want)
	}
	if list[1].Title != "Only Japanese" || list[1].CoverURL != "" || list[1].Description != "Hello" {
		t.Errorf("second summary: %+v", list[1])
	}

	if query.Get("title") != "dragon" || query.Get("order[relevance]") != "desc" {
		t.Errorf("query = %v", query)
	}
	if !reflect.DeepEqual(query["availableTranslatedLanguage[]"], []string{"vi"}) {
		t.Errorf("language filter = %v", query["availableTranslatedLanguage[]"])
	}
	if !reflect.DeepEqual(query["contentRating[]"], []string{"safe", "suggestive"}) {
		t.Errorf("adult content must be excluded by default: %v", query["contentRating[]"])
	}
	if query.Get("hasAvailableChapters") != "true" {
		t.Errorf("should only list series with chapters: %v", query)
	}
}

func TestSearchBrowseOrdersByFollowsAndAdult(t *testing.T) {
	var query url.Values
	c := newSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		_, _ = w.Write([]byte(`{"result":"ok","total":0,"data":[]}`))
	})

	if _, _, err := c.Search(context.Background(), SearchParams{IncludeAdult: true, Limit: 500}); err != nil {
		t.Fatal(err)
	}
	if query.Get("order[followedCount]") != "desc" || query.Get("title") != "" {
		t.Errorf("browse should order by follows: %v", query)
	}
	if len(query["contentRating[]"]) != 4 {
		t.Errorf("adult ratings missing: %v", query["contentRating[]"])
	}
	if query.Get("limit") != "24" {
		t.Errorf("limit should be clamped to the default, got %q", query.Get("limit"))
	}
}

func TestDescribeAndErrors(t *testing.T) {
	c := newSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manga/aaa" {
			_, _ = w.Write([]byte(`{"result":"ok","data":{"id":"aaa","attributes":{"title":{"en":"T"}},"relationships":[]}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"Manga not found"}]}`))
	})

	s, err := c.Describe(context.Background(), "aaa")
	if err != nil || s.Title != "T" {
		t.Fatalf("got %+v, %v", s, err)
	}
	if _, err := c.Describe(context.Background(), "zzz"); err == nil {
		t.Error("expected an error for an unknown series")
	}
}
