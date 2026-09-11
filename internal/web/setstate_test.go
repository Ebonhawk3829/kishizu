package web

import (
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestSetStateMarksDownloaded: an episode obtained outside kishizu can be
// marked downloaded, so it stops being hunted and shows as ready to watch.
func TestSetStateMarksDownloaded(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Show", nil, 12)
	_ = srv.st.UpsertEpisode(sh.ID, 5, episode.Wanted, "", "")

	rec := post(t, srv, "/api/set-state",
		`{"show_id":1,"episode":5,"state":"downloaded"}`)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 5)
	if ep.State != episode.Downloaded {
		t.Errorf("state = %s, want downloaded", ep.State)
	}
}

// TestSetStateRefusesTerminalRewind: a watched or deleted episode must not be
// sent backwards — that would resurrect something already consumed.
func TestSetStateRefusesTerminalRewind(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Show", nil, 12)
	_ = srv.st.UpsertEpisode(sh.ID, 5, episode.Watched, "", "")

	rec := post(t, srv, "/api/set-state",
		`{"show_id":1,"episode":5,"state":"downloaded"}`)
	if rec.Code != 409 {
		t.Errorf("status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 5)
	if ep.State != episode.Watched {
		t.Errorf("state = %s, want watched (unchanged)", ep.State)
	}
}

// TestSetStateRejectsWatched: watched has its own endpoint, which records the
// timestamp and triggers the sweep. Allowing it here would bypass that.
func TestSetStateRejectsWatched(t *testing.T) {
	srv := testServer(t)
	_, _ = srv.st.CreateShow("Show", nil, 12)

	rec := post(t, srv, "/api/set-state",
		`{"show_id":1,"episode":5,"state":"watched"}`)
	if rec.Code != 400 {
		t.Errorf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/api/watched") {
		t.Errorf("body should point at /api/watched, got %s", rec.Body.String())
	}
}

// TestSetStateRequiresShowAndEpisode: a partial request must fail loudly.
func TestSetStateRequiresShowAndEpisode(t *testing.T) {
	srv := testServer(t)
	if rec := post(t, srv, "/api/set-state", `{"state":"downloaded"}`); rec.Code != 400 {
		t.Errorf("status %d, want 400", rec.Code)
	}
}
