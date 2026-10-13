package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	md "github.com/leotaku/kojirou/mangadex"
)

func TestInteractiveClientFailsFast(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"boom"}]}`))
	}))
	defer srv.Close()
	api, _ := url.Parse(srv.URL + "/")
	covers, _ := url.Parse(srv.URL + "/covers/")
	SetMangadexClient(md.NewClient().WithBaseURLs(*api, *covers))
	t.Cleanup(func() { SetMangadexClient(md.NewClient()) })

	start := time.Now()
	_, err := InteractiveClient().Describe(context.Background(), "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("interactive client took %v to give up", elapsed)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("the server's own error detail should be kept, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("made %d attempts, want 1 try + 2 retries", got)
	}
}

func TestClientsAreIndependentAndHonourBaseURL(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"result":"ok","data":{"id":"x","attributes":{"title":{"en":"T"}},"relationships":[]}}`))
	}))
	defer srv.Close()
	api, _ := url.Parse(srv.URL + "/")
	covers, _ := url.Parse(srv.URL + "/covers/")
	SetMangadexClient(md.NewClient().WithBaseURLs(*api, *covers))
	t.Cleanup(func() { SetMangadexClient(md.NewClient()) })

	if PatientClient() == InteractiveClient() {
		t.Fatal("clients must be distinct")
	}
	for _, c := range []*md.Client{PatientClient(), InteractiveClient()} {
		if _, err := c.Describe(context.Background(), "x"); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 2 {
		t.Errorf("both clients should reach the configured API, got %d hits", hits)
	}
}
