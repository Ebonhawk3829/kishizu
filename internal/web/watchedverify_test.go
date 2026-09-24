package web

import (
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestWatchedVerifyReportsState: the verify endpoint must report the episode's
// current state without changing it, so a client can settle a signal whose
// outcome it could not observe.
func TestWatchedVerifyReportsState(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Clevatess Season 2", []string{"Clevatess"}, 13)
	_ = st.UpsertEpisode(sh.ID, 9, episode.Downloaded, "H", "rel")

	body := `{"path":"D:\\Anime\\Clevatess Season 2\\Clevatess Season 2 - E09.mkv"}`

	rec := post(t, srv, "/api/watched/verify", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"watched":false`) {
		t.Errorf("body = %s, want watched false before marking", rec.Body.String())
	}

	// Marking is a separate call; verify must not have done it.
	ep, _ := st.GetEpisode(sh.ID, 9)
	if ep.State != episode.Downloaded {
		t.Errorf("state = %s, want downloaded: verify must be read-only", ep.State)
	}

	if rec := post(t, srv, "/api/watched", body); rec.Code != 200 {
		t.Fatalf("mark status %d: %s", rec.Code, rec.Body.String())
	}

	rec = post(t, srv, "/api/watched/verify", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"watched":true`) {
		t.Errorf("body = %s, want watched true after marking", rec.Body.String())
	}
}

// TestWatchedVerifyRejectsUnmatched: a file that matches no tracked show must
// be refused, exactly as /api/watched refuses it. A client that cannot resolve
// a path cannot verify it either.
func TestWatchedVerifyRejectsUnmatched(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Show", []string{"Show"}, 12)
	_ = srv.st.SetGroupOffset(sh.ID, "A", 0, "training")
	_ = srv.st.SetGroupOffset(sh.ID, "B", 40, "training")

	body := `{"path":"/downloads/[BrandNewGroup] Show S01E09 1080p.mkv"}`
	rec := post(t, srv, "/api/watched/verify", body)
	if rec.Code != 422 {
		t.Errorf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}
