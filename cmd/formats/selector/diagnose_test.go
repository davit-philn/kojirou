package selector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func serve(t *testing.T, html string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestImageAttrListSkipsPlaceholders(t *testing.T) {
	srv := serve(t, `<div id="r">
		<img src="data:image/gif;base64,R0lGOD" data-src="/real/1.jpg">
		<img src="/b.jpg">
		<img data-src="https://cdn.example.org/c.jpg">
	</div>`)

	cfg := SourceConfig{ImageListSelector: "#r img", ImageAttr: " data-src , src "}
	got, err := FetchImages(context.Background(), srv.URL+"/ch", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{srv.URL + "/real/1.jpg", srv.URL + "/b.jpg", "https://cdn.example.org/c.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// With the default attribute the placeholder is skipped, not used.
	cfg.ImageAttr = ""
	got, err = FetchImages(context.Background(), srv.URL+"/ch", cfg)
	if err != nil || !reflect.DeepEqual(got, []string{srv.URL + "/b.jpg"}) {
		t.Errorf("default attribute: got %v, %v", got, err)
	}
}

func TestSplitAttrs(t *testing.T) {
	if got := splitAttrs(""); !reflect.DeepEqual(got, []string{"src"}) {
		t.Errorf("empty -> %v", got)
	}
	if got := splitAttrs(" a, ,b ,"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("list -> %v", got)
	}
}

func TestNoImagesErrorsExplainWhatWasSeen(t *testing.T) {
	tests := []struct {
		name string
		html string
		cfg  SourceConfig
		want []string
	}{
		{
			name: "elements matched but the attribute has no address",
			html: `<html><body><div class="v"><img class="lazy" data-x="/1.jpg"><img class="lazy"></div></body></html>`,
			cfg:  SourceConfig{ImageListSelector: ".v img", ImageAttr: "src"},
			want: []string{"2 elements matched", `"src"`, "[class data-x]", "image_attr"},
		},
		{
			name: "selector matches nothing",
			html: `<html><head><title>Some Series</title></head><body><a href="/x">x</a><img src="/a.jpg"><img src="/b.jpg"></body></html>`,
			cfg:  SourceConfig{ImageListSelector: ".reader img"},
			want: []string{`nothing matched ".reader img"`, `page title: "Some Series"`, "1 <a> and 2 <img>", "JavaScript", "Ctrl+U"},
		},
		{
			name: "browser check page",
			html: `<html><head><title>Just a moment...</title></head><body>Checking your browser before accessing the site</body></html>`,
			cfg:  SourceConfig{ImageListSelector: ".reader img"},
			want: []string{"browser check", "anti-bot", "cannot pass it"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serve(t, tt.html)
			_, err := FetchImages(context.Background(), srv.URL+"/ch", tt.cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error does not mention %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestNoChaptersErrorExplainsWhatWasSeen(t *testing.T) {
	srv := serve(t, `<html><head><title>Series page</title></head><body><ul><li>No links here</li></ul></body></html>`)
	_, err := FetchChapterList(context.Background(), srv.URL+"/s", SourceConfig{ChapterListSelector: ".chapters a"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, w := range []string{"no chapters found", `nothing matched ".chapters a"`, `"Series page"`, "chapter list"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error does not mention %q:\n%v", w, err)
		}
	}
}
