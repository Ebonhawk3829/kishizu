package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestAdoptEndToEnd: a confirmed adoption must create the show with source
// seadex and mark the episodes downloading, which is what makes Reconcile pick
// the files up. Transmission is a stub that records what it was handed.
func TestAdoptEndToEnd(t *testing.T) {
	var added map[string]any
	tx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "T")
			w.WriteHeader(http.StatusConflict)
			return
		}
		var body struct {
			Method    string `json:"method"`
			Arguments struct {
				Filename    string `json:"filename"`
				DownloadDir string `json:"download-dir"`
			} `json:"arguments"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		added = map[string]any{
			"method": body.Method,
			"magnet": body.Arguments.Filename,
			"dir":    body.Arguments.DownloadDir,
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
	srv.SetAdopt(t.TempDir(), t.TempDir(), tx.URL)

	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/112124/",
		"title":     "DanMachi III",
		"info_hash": "HASH123",
		"files": []map[string]any{
			{"name": "01.mkv", "include": true, "episode": 1},
			{"name": "02.mkv", "include": true, "episode": 2},
			{"name": "NCOP.mkv", "include": false, "episode": 0},
		},
	})
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	// Transmission got the magnet and a per-show directory.
	if added["method"] != "torrent-add" {
		t.Errorf("method = %v, want torrent-add", added["method"])
	}
	if got := added["magnet"]; got != "magnet:?xt=urn:btih:HASH123&dn=DanMachi III" {
		t.Errorf("magnet = %v", got)
	}
	if got := added["dir"]; got == "" || filepath.Base(got.(string)) != "DanMachi III" {
		t.Errorf("download dir = %v, want a DanMachi III directory", got)
	}

	// The show exists with source seadex.
	shows, _ := st.ListShows()
	if len(shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(shows))
	}
	if shows[0].Source != store.SourceSeaDex {
		t.Errorf("source = %q, want seadex", shows[0].Source)
	}
	if shows[0].MaxEpisode != 2 {
		t.Errorf("max_episode = %d, want 2", shows[0].MaxEpisode)
	}

	// Two episodes downloading, the excluded one not created at all.
	eps, _ := st.EpisodesForShow(shows[0].ID)
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2", len(eps))
	}
	for _, e := range eps {
		if e.State != episode.Downloading {
			t.Errorf("episode %d state = %s, want downloading", e.Number, e.State)
		}
	}

	// The infohash is recorded, so a later re-grab skips this release.
	seen, _ := st.HasSeen("HASH123")
	if !seen {
		t.Error("infohash not recorded as seen")
	}
}
