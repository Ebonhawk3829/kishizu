package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// post issues a POST against the server's mux and returns the recorder.
func post(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func testServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(st)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return srv
}

// TestWatchedByPath: an mpv signal carries only the file path; the server does
// the matching, since it has the aliases and offsets.
func TestWatchedByPath(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")

	// The PC's path is a Windows path; only the base name matters.
	body := `{"path":"D:\\Anime\\[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL.mkv"}`
	rec := post(t, srv, "/api/watched", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"episode":9`) {
		t.Errorf("body = %s, want episode 9", rec.Body.String())
	}
	ep, _ := st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Watched {
		t.Errorf("episode not marked watched: %+v", ep)
	}
}

// TestWatchedRefusesUncertainMatch: a filename the model cannot confidently
// match must be refused, not guessed. A wrong guess here deletes a file the
// user may still want.
func TestWatchedRefusesUncertain(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	// Offsets disagree, so an unseen group is a coin flip.
	_ = srv.st.SetGroupOffset(sh.ID, "A", 0, "training")
	_ = srv.st.SetGroupOffset(sh.ID, "B", 40, "training")

	body := `{"path":"/downloads/[BrandNewGroup] Tomb Raider King S01E09 1080p.mkv"}`
	rec := post(t, srv, "/api/watched", body)
	if rec.Code != 422 {
		t.Errorf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep != nil {
		t.Errorf("uncertain match marked an episode: %+v", ep)
	}
}

// TestWatchedManualOverridesMatching: the UI's manual path skips matching
// entirely, since the user said so.
func TestWatchedManualOverridesMatching(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Show", nil, 12)

	body := `{"show_id":1,"episode":7}`
	rec := post(t, srv, "/api/watched", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 7)
	if ep == nil || ep.State != episode.Watched {
		t.Errorf("episode not marked: %+v", ep)
	}
}

// TestWatchedIsIdempotent: a duplicate mpv signal must not fail or rewind.
func TestWatchedIsIdempotent(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = srv.st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")

	body := `{"path":"[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL.mkv"}`
	if rec := post(t, srv, "/api/watched", body); rec.Code != 200 {
		t.Fatalf("first: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(t, srv, "/api/watched", body); rec.Code != 200 {
		t.Errorf("duplicate: %d %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep.State != episode.Watched {
		t.Errorf("state after duplicate = %s, want watched", ep.State)
	}
}
