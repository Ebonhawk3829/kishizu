package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestWatchedAdvancesSchedule: watching the episode the schedule points at
// must move the pointer forward, or the UI keeps showing an air date that is
// already in the past.
//
// Mushoku Tensei showed this: the user watched ep 11 outside kishizu, the
// schedule still said "ep 11 airs Sep 6", and the card read "Ep 11 aired
// Sep 6" even though ep 12 was the one actually due. The pointer only moved
// on download confirmation, which never happens for episodes obtained
// outside the tool.
func TestWatchedAdvancesSchedule(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Show", nil, 14)
	// Schedule says ep 11 airs now; episodes 1..10 already watched.
	airs := time.Now().Add(-time.Hour)
	if err := st.SetNextEpisode(sh.ID, 11, airs); err != nil {
		t.Fatal(err)
	}
	if err := st.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ {
		_ = st.UpsertEpisode(sh.ID, i, episode.Watched, "", "")
	}

	s := &Server{st: st}
	body := `{"show_id": ` + fmt.Sprintf("%d", sh.ID) + `, "episode": 11}`
	req := httptest.NewRequest("POST", "/api/watched", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("watched: status %d: %s", rec.Code, rec.Body.String())
	}

	n, at, err := st.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Errorf("next_ep = %d, want 12 (pointer must move past the watched episode)", n)
	}
	if at == nil || !at.After(time.Now().Add(24*time.Hour)) {
		t.Errorf("next_airs_at = %v, want about a week out", at)
	}

	// The list endpoint must report the future episode even though the
	// schedule pointer was not touched by this request: the air line derives
	// from the projected episode rows, which follow watch progress.
	req2 := httptest.NewRequest("GET", "/shows", nil)
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	var shows []struct {
		Next         int `json:"next"`
		NextSchedule *struct {
			Episode int    `json:"episode"`
			AirsAt  string `json:"airs_at"`
		} `json:"next_schedule"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &shows); err != nil {
		t.Fatal(err)
	}
	if shows[0].Next != 12 {
		t.Errorf("next = %d, want 12", shows[0].Next)
	}
	if shows[0].NextSchedule == nil || shows[0].NextSchedule.Episode != 12 {
		t.Errorf("next_schedule = %+v, want episode 12", shows[0].NextSchedule)
	}
}

// TestWatchedUpToAdvancesSchedule: the bulk path must advance the pointer too.
func TestWatchedUpToAdvancesSchedule(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Show", nil, 14)
	airs := time.Now().Add(-time.Hour)
	_ = st.SetNextEpisode(sh.ID, 11, airs)
	_ = st.ProjectAirDates(sh.ID)

	s := &Server{st: st}
	body := `{"show_id": ` + fmt.Sprintf("%d", sh.ID) + `, "up_to": 11}`
	req := httptest.NewRequest("POST", "/api/watched-up-to", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("watched-up-to: status %d: %s", rec.Code, rec.Body.String())
	}

	n, _, err := st.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Errorf("next_ep = %d, want 12", n)
	}
}

// TestAddShow: the endpoint requires a schedule URL. A plain name is
// rejected — without a slug there is no air-date anchor, so the show could
// never be hunted correctly. The browse modal passes slugs directly and is
// covered by the adopt flow tests.
func TestAddShow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	s := &Server{st: st}

	// A plain name is rejected with a message that says what to do instead.
	req := httptest.NewRequest("POST", "/api/shows", strings.NewReader(`{"name": "New Show"}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("plain name: status %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "animeschedule.net") {
		t.Errorf("plain name rejection does not say what to do instead: %s", rec.Body.String())
	}

	// A blank name is rejected.
	req2 := httptest.NewRequest("POST", "/api/shows", strings.NewReader(`{"name": "  "}`))
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != 400 {
		t.Errorf("blank name: status %d, want 400", rec2.Code)
	}
}

// TestAddShowMisspelledURL covers the well-formed-but-wrong case: the URL
// passes every structural check (right host, /anime/ path) but the slug does
// not exist. The schedule's 404 is evidence the site is up and the show is
// not there — that is the user's typo, so it must come back as a 400 with a
// message that says what to do next, not a 500 that reads as a server fault.
func TestAddShowMisspelledURL(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	s := &Server{st: st}

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer stub.Close()
	restore := schedule.PinShowURL(stub.URL)
	defer restore()

	req := httptest.NewRequest("POST", "/api/shows",
		strings.NewReader(`{"name": "https://animeschedule.net/anime/fooo-bar"}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("misspelled slug: status %d, want 400", rec.Code)
	}
	for _, want := range []string{"no show at", "Browse this season"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("misspelled slug rejection missing %q: %s", want, rec.Body.String())
		}
	}
}
