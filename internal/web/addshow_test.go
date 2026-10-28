package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// midSeasonPage is a show with five episodes already out: leadingAiredEpisode
// is the last one aired, and the raw countdown advertises episode 6.
const midSeasonPage = `<html><head>
<title>Overgeared (Overgeared English) | AnimeSchedule</title>
</head><body>
<div id="anime-wrapper" itemid="https://animeschedule.net/anime/overgeared" route="overgeared" leadingAiredEpisode="5" status="Ongoing">
  <div class="countdown-container">
    <div class="countdown-text-container header-countdown-text-raw" title="raw">
      <span>Ep 6</span>
      <span class="countdown-text"> Raw: </span>
    </div>
    <time class="countdown-time countdown-time-raw" data-countdown-target="2026-11-01T14:30:00Z">5d</time>
  </div>
</div>
</body></html>`

// stubSchedule points the schedule package at a local page and restores it
// after the test. The add and preview paths fetch the show's page over the
// network, so the only way to exercise them is to stand in for the site.
func stubSchedule(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/anime/overgeared") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(midSeasonPage))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(schedule.PinShowURL(srv.URL))
}

func testServerWithStore(t *testing.T) (*Server, *store.Store) {
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
	return srv, st
}

// TestPreviewReportsAiredCount: the preview tells the UI how many episodes are
// already out, so it can decide whether to offer the backfill choice — and it
// creates nothing.
func TestPreviewReportsAiredCount(t *testing.T) {
	srv, st := testServerWithStore(t)
	stubSchedule(t)

	rec := postJSON(t, srv, "/api/shows/preview", map[string]string{"name": "https://animeschedule.net/anime/overgeared"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		NextEpisode int `json:"next_episode"`
		Episodes    int `json:"episodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.NextEpisode != 6 {
		t.Errorf("next_episode = %d, want 6", got.NextEpisode)
	}
	shows, err := st.ListShows()
	if err != nil {
		t.Fatalf("ListShows: %v", err)
	}
	if len(shows) != 0 {
		t.Errorf("preview created %d shows, want 0", len(shows))
	}
}

// TestAddStartNextSkipsBacklog: adding with start "next" pins the floor at the
// page's next episode, so the already-aired backlog is never hunted and the
// anchor is that episode rather than episode 1.
func TestAddStartNextSkipsBacklog(t *testing.T) {
	srv, st := testServerWithStore(t)
	stubSchedule(t)

	rec := postJSON(t, srv, "/api/shows", map[string]any{"name": "https://animeschedule.net/anime/overgeared", "start": "next"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	sh := onlyShow(t, st)
	if sh.StartEp != 6 {
		t.Errorf("StartEp = %d, want 6", sh.StartEp)
	}
	if got := st.NextUnwatched(sh.ID); got != 6 {
		t.Errorf("NextUnwatched = %d, want 6", got)
	}
	if ep, _ := st.GetEpisode(sh.ID, 1); ep != nil {
		t.Error("episode 1 has a row: the backlog was not skipped")
	}
	if ep, _ := st.GetEpisode(sh.ID, 6); ep == nil {
		t.Error("the floor episode has no row")
	}
}

// TestAddDefaultBackfillsFromOne: with no choice (or "first"), the show starts
// at episode 1 and the backlog is in play — the original behaviour, preserved
// for scripts and for anything added before the choice existed.
func TestAddDefaultBackfillsFromOne(t *testing.T) {
	srv, st := testServerWithStore(t)
	stubSchedule(t)

	// A URL is the only accepted input; the browse modal builds the same shape.
	rec := postJSON(t, srv, "/api/shows",
		map[string]string{"name": "https://animeschedule.net/anime/overgeared"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	sh := onlyShow(t, st)
	if sh.StartEp != 0 {
		t.Errorf("StartEp = %d, want 0 (whole season)", sh.StartEp)
	}
	if got := st.NextUnwatched(sh.ID); got != 1 {
		t.Errorf("NextUnwatched = %d, want 1", got)
	}
	if ep, _ := st.GetEpisode(sh.ID, 1); ep == nil {
		t.Error("episode 1 should be the anchor when backfilling")
	}
}

// TestAddRejectsBareSlug: a bare slug is not a schedule URL. The browse modal
// builds the page URL from its slug, and a plain name is indistinguishable
// from a slug, so the server accepts only URLs.
func TestAddRejectsBareSlug(t *testing.T) {
	srv, st := testServerWithStore(t)

	rec := postJSON(t, srv, "/api/shows", map[string]string{"name": "overgeared"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if shows, _ := st.ListShows(); len(shows) != 0 {
		t.Errorf("a rejected add created %d shows", len(shows))
	}
}

// TestAddRejectsBadStart: a typo in the choice is a bad request, not a silent
// default that downloads the whole season.
func TestAddRejectsBadStart(t *testing.T) {
	srv, _ := testServerWithStore(t)
	stubSchedule(t)

	rec := postJSON(t, srv, "/api/shows", map[string]any{"name": "https://animeschedule.net/anime/overgeared", "start": "newest"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func onlyShow(t *testing.T, st *store.Store) *store.Show {
	t.Helper()
	shows, err := st.ListShows()
	if err != nil {
		t.Fatalf("ListShows: %v", err)
	}
	if len(shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(shows))
	}
	return shows[0]
}
