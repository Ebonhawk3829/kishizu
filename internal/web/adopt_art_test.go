package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/anilist"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestAdoptSetsCoverArt: an adopted season has no art from animeschedule, and
// SeaDex exposes none, so the poster comes from AniList using the id in the
// entry URL. A failure must not fail the adoption.
func TestAdoptSetsCoverArt(t *testing.T) {
	// Stub AniList: answer the GraphQL query with a cover URL.
	anilistSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"Media":{"id":112124,"coverImage":{"large":"https://example.test/cover.jpg"}}}}`))
	}))
	defer anilistSrv.Close()

	tx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "T")
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Write([]byte(`{"result":"success","arguments":{}}`))
	}))
	defer tx.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAdopt(t.TempDir(), t.TempDir(), &download.Fake{})

	// Point the AniList client at the stub for this test.
	old := anilist.Endpoint
	anilist.Endpoint = anilistSrv.URL
	defer func() { anilist.Endpoint = old }()

	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/112124/",
		"title":     "DanMachi III",
		"info_hash": "HASH123",
		"files": []map[string]any{
			{"name": "01.mkv", "include": true, "episode": 1},
		},
	})
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	shows, _ := st.ListShows()
	if len(shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(shows))
	}
	if shows[0].ImageURL != "https://example.test/cover.jpg" {
		t.Errorf("ImageURL = %q, want the AniList cover", shows[0].ImageURL)
	}
}
