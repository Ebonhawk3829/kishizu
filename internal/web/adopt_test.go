package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/anilist"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/seadex"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

func testServerWithAdopt(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAdopt(t.TempDir(), t.TempDir(), &download.Fake{})
	return srv, st
}

func postJSON(t *testing.T, srv *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
	return rec
}

// TestAdoptPreviewRejectsBadURL: a URL with no entry id must be refused rather
// than silently resolving to entry 0.
func TestAdoptPreviewRejectsBadURL(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt/preview", map[string]string{"url": "https://example.com/nope"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptRejectsDuplicateEpisode: two files both tagged episode 1 would race
// in the reconciler, and which one won would depend on directory order. This
// must be caught before anything reaches Transmission.
func TestAdoptRejectsDuplicateEpisode(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files": []map[string]any{
			{"name": "a.mkv", "include": true, "episode": 1},
			{"name": "b.mkv", "include": true, "episode": 1},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("assigned twice")) {
		t.Errorf("body = %s, want a duplicate-episode error", rec.Body.String())
	}
}

// TestAdoptRejectsOutOfRange: the dropdown bounds the choice, so a number
// outside it is a mistake worth refusing.
func TestAdoptRejectsOutOfRange(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files": []map[string]any{
			{"name": "a.mkv", "include": true, "episode": 99},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptRequiresTitle: without a title there is nothing to name the show or
// the library directory after.
func TestAdoptRequiresTitle(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "   ",
		"info_hash": "abc",
		"files":     []map[string]any{{"name": "a.mkv", "include": true, "episode": 1}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptRejectsEmptySelection: adopting nothing is a mistake, not a no-op.
func TestAdoptRejectsEmptySelection(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files":     []map[string]any{{"name": "a.mkv", "include": false, "episode": 0}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptDisabledWithoutConfig: a server with no staging, library or RPC
// configured must say so rather than creating rows that can never resolve.
func TestAdoptDisabledWithoutConfig(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files":     []map[string]any{{"name": "a.mkv", "include": true, "episode": 1}},
	})
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptPreviewPrefersAniListTitle: the entry page displays the title
// right next to the torrent list, so the preview should carry that name —
// authoritative — rather than one parsed out of the release's filenames,
// which follow a group's naming scheme rather than any contract. The
// filename-derived title is only the fallback for when AniList cannot be
// reached.
func TestAdoptPreviewPrefersAniListTitle(t *testing.T) {
	srv, _ := testServerWithAdopt(t)

	// Stub SeaDex with a scene-style pack whose filenames would derive a
	// different title than AniList's.
	seadexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"items": [{
				"alID": 142074,
				"incomplete": false,
				"theoreticalBest": "",
				"notes": "",
				"expand": {"trs": [{
					"infoHash": "e211e5d1e227cb4a1a45907378723bd35cbc30ce",
					"url": "https://nyaa.si/view/1958847",
					"tracker": "Nyaa",
					"releaseGroup": "CRUCiBLE",
					"isBest": true,
					"dualAudio": true,
					"tags": [],
					"files": [
						{"name": "Trapped.in.a.Dating.Sim.S01E01.I.Hate.This.World.1080p.BluRay.Remux.Dual-Audio.FLAC2.0.H.264-CRUCiBLE.mkv"},
						{"name": "Trapped.in.a.Dating.Sim.S01E02.Hey.Girl.Wanna.Get.Some.Tea.1080p.BluRay.Remux.Dual-Audio.FLAC2.0.H.264-CRUCiBLE.mkv"}
					]
				}]}
			}]
		}`))
	}))
	defer seadexSrv.Close()

	anilistSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"Media":{"id":142074,"title":{"english":"Trapped in a Dating Sim: The World of Otome Games Is Tough for Mobs"}}}}`))
	}))
	defer anilistSrv.Close()

	oldAPI, oldEndpoint := seadex.API, anilist.Endpoint
	seadex.API = seadexSrv.URL + "/api/collections"
	anilist.Endpoint = anilistSrv.URL
	defer func() { seadex.API, anilist.Endpoint = oldAPI, oldEndpoint }()

	rec := postJSON(t, srv, "/api/adopt/preview", map[string]string{"url": "https://releases.moe/142074/"})
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Title string `json:"title"`
		Files []struct {
			Episode int  `json:"episode"`
			Include bool `json:"include"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	want := "Trapped in a Dating Sim: The World of Otome Games Is Tough for Mobs"
	if out.Title != want {
		t.Errorf("title = %q, want the AniList title %q", out.Title, want)
	}
	if len(out.Files) != 2 || !out.Files[0].Include || out.Files[0].Episode != 1 {
		t.Errorf("files = %+v, want two pre-assigned episodes", out.Files)
	}
}
