package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
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

	// The list endpoint must now report the future episode, not the past one.
	req2 := httptest.NewRequest("GET", "/shows", nil)
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	var shows []struct {
		NextSchedule *struct {
			Episode int    `json:"episode"`
			AirsAt  string `json:"airs_at"`
		} `json:"next_schedule"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &shows); err != nil {
		t.Fatal(err)
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

// TestAddShow: the UI endpoint creates a show with its aliases, and the name
// is an alias of itself.
func TestAddShow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	s := &Server{st: st}
	body := `{"name": "New Show", "aliases": ["NS"], "max_episode": 12}`
	req := httptest.NewRequest("POST", "/api/shows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("add-show: status %d: %s", rec.Code, rec.Body.String())
	}

	sh, err := st.GetShowByName("New Show")
	if err != nil || sh == nil {
		t.Fatalf("show not created: %v", err)
	}
	found := false
	for _, a := range sh.Aliases {
		if a == "New Show" {
			found = true
		}
	}
	if !found {
		t.Errorf("canonical name not among aliases: %v", sh.Aliases)
	}

	// A blank name is rejected.
	req2 := httptest.NewRequest("POST", "/api/shows", strings.NewReader(`{"name": "  "}`))
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != 400 {
		t.Errorf("blank name: status %d, want 400", rec2.Code)
	}
}
