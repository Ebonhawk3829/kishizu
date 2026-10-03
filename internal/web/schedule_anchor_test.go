package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// The schedule endpoint serves the anchor and nothing else: one entry per
// show, the next unaired episode and its air time. These tests pin that
// contract, because the endpoint is what a dashboard widget renders and the
// whole point of the anchor model is that nothing beyond the countdown is
// stored or invented.

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeSchedule(t *testing.T, rec *httptest.ResponseRecorder) struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Count    int    `json:"count"`
	Episodes []struct {
		Show    string `json:"show"`
		Episode int    `json:"episode"`
		AirsAt  string `json:"airs_at"`
		State   string `json:"state"`
	} `json:"episodes"`
} {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Count    int    `json:"count"`
		Episodes []struct {
			Show    string `json:"show"`
			Episode int    `json:"episode"`
			AirsAt  string `json:"airs_at"`
			State   string `json:"state"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return out
}

// TestScheduleServesTheAnchor: a show with a countdown inside the default
// 7-day window appears exactly once, as its next episode.
func TestScheduleServesTheAnchor(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Show", nil, 12)
	if err := st.SetNextEpisode(sh.ID, 8, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	got := decodeSchedule(t, get(t, srv, "/api/schedule"))
	if got.Count != 1 || len(got.Episodes) != 1 {
		t.Fatalf("count = %d, episodes = %d, want 1 each", got.Count, len(got.Episodes))
	}
	e := got.Episodes[0]
	if e.Show != "Show" || e.Episode != 8 {
		t.Errorf("entry = %s ep%d, want Show ep8 (the anchor)", e.Show, e.Episode)
	}
}

// TestScheduleExcludesWatchedAnchor: an episode the user has already watched
// is not "coming", even while the daily refresh has not yet moved the anchor
// past it. The anchor's air time may be stale for up to 24h; the watch state
// is not.
func TestScheduleExcludesWatchedAnchor(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Show", nil, 12)
	if err := st.SetNextEpisode(sh.ID, 8, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEpisode(sh.ID, 8, episode.Watched, "H", "rel"); err != nil {
		t.Fatal(err)
	}

	got := decodeSchedule(t, get(t, srv, "/api/schedule"))
	if got.Count != 0 {
		t.Errorf("count = %d, want 0: a watched episode is not coming", got.Count)
	}
}

// TestScheduleExcludesOutOfWindow: an anchor further out than the window is
// not listed. The default window is the coming week.
func TestScheduleExcludesOutOfWindow(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Show", nil, 12)
	if err := st.SetNextEpisode(sh.ID, 8, time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	got := decodeSchedule(t, get(t, srv, "/api/schedule"))
	if got.Count != 0 {
		t.Errorf("count = %d, want 0: the anchor is outside the 7-day window", got.Count)
	}
}

// TestScheduleWindowParameter: ?from= and ?to= move the window, so a widget
// can ask for a specific week rather than only the coming one.
func TestScheduleWindowParameter(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Show", nil, 12)
	// 40 days out: outside the default window, inside a 60-day one.
	airTime := time.Now().Add(40 * 24 * time.Hour)
	if err := st.SetNextEpisode(sh.ID, 8, airTime); err != nil {
		t.Fatal(err)
	}

	got := decodeSchedule(t, get(t, srv, "/api/schedule"))
	if got.Count != 0 {
		t.Fatalf("default window: count = %d, want 0", got.Count)
	}

	from := time.Now().Format(time.RFC3339)
	to := time.Now().Add(60 * 24 * time.Hour).Format(time.RFC3339)
	got = decodeSchedule(t, get(t, srv, "/api/schedule?from="+from+"&to="+to))
	if got.Count != 1 {
		t.Errorf("60-day window: count = %d, want 1", got.Count)
	}
}
