package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// watchedFile records a downloaded file for a show, as the reconciler would
// after filing a completed download.
func watchedFile(t *testing.T, st *store.Store, showID int64, number int, name string) {
	t.Helper()
	path := filepath.Join("/media/anime", name, name)
	if err := st.FinaliseEpisode(showID, number, path); err != nil {
		t.Fatalf("finalise: %v", err)
	}
}

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

// TestWatchedByPath: a player signal carries only the file path; the server
// resolves it by exact name against the paths it recorded when filing the
// download.
func TestWatchedByPath(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"})
	watchedFile(t, st, sh.ID, 9, "Tomb Raider King - E09.mkv")

	// The PC's path is a Windows path; only the base name matters.
	body := `{"path":"D:\\Anime\\Tomb Raider King\\Tomb Raider King - E09.mkv"}`
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

// TestWatchedRefusesUncertainMatch: a filename no stored episode owns must be
// refused, not guessed. A wrong guess here deletes a file the user may still
// want.
func TestWatchedRefusesUncertain(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"})
	watchedFile(t, srv.st, sh.ID, 9, "Tomb Raider King - E09.mkv")

	body := `{"path":"/downloads/[BrandNewGroup] Tomb Raider King S01E09 1080p.mkv"}`
	rec := post(t, srv, "/api/watched", body)
	if rec.Code != 422 {
		t.Errorf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Downloaded {
		t.Errorf("state = %v, want downloaded: an unknown filename must not mark anything", ep)
	}
}

// TestWatchedManualOverridesMatching: the UI's manual path skips matching
// entirely, since the user said so.
func TestWatchedManualOverridesMatching(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Show", nil)

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

// TestWatchedIsIdempotent: a duplicate watch signal must not fail or rewind.
func TestWatchedIsIdempotent(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"})
	watchedFile(t, srv.st, sh.ID, 9, "Tomb Raider King - E09.mkv")

	body := `{"path":"D:\\Anime\\Tomb Raider King\\Tomb Raider King - E09.mkv"}`
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
