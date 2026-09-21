package web

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestAdoptEndToEnd: a confirmed adoption must create the show with source
// seadex and mark the episodes downloading, which is what makes Reconcile pick
// the files up. The downloader is a fake that records what it was handed.
//
// Asserting against the fake rather than a stubbed Transmission RPC is the
// point of the Downloader interface: the test should not care which client is
// behind it, only that the magnet and a per-show directory were handed over.
func TestAdoptEndToEnd(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	dl := &download.Fake{}
	srv.SetAdopt(t.TempDir(), t.TempDir(), dl)

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

	// The downloader got the magnet and a per-show directory.
	if dl.Count() != 1 {
		t.Fatalf("downloader got %d adds, want 1", dl.Count())
	}
	added := dl.Last()
	if added.Magnet != "magnet:?xt=urn:btih:HASH123&dn=DanMachi III" {
		t.Errorf("magnet = %q", added.Magnet)
	}
	if filepath.Base(added.Dir) != "DanMachi III" {
		t.Errorf("download dir = %q, want a DanMachi III directory", added.Dir)
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
