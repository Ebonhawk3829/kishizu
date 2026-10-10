package web

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestWatchedAcceptsLibraryFilename: a filename kishizu recorded when it filed
// the download must be accepted, whatever directory the player reports it
// from — only the base name is compared.
func TestWatchedAcceptsLibraryFilename(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Clevatess Season 2", []string{"Clevatess"})
	path := filepath.Join("/media/anime", "Clevatess Season 2", "Clevatess Season 2 - E09.mkv")
	if err := st.FinaliseEpisode(sh.ID, 9, path); err != nil {
		t.Fatalf("finalise: %v", err)
	}

	// A Windows path, as a player on the user's machine would send it.
	body := `{"path":"D:\\Anime\\Clevatess Season 2\\Clevatess Season 2 - E09.mkv"}`
	rec := post(t, srv, "/api/watched", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"episode":9`) {
		t.Errorf("body = %s, want episode 9", rec.Body.String())
	}
	ep, _ := st.GetEpisode(sh.ID, 9)
	if ep.State != episode.Watched {
		t.Errorf("state = %s, want watched", ep.State)
	}
}

// TestWatchedRefusesUnknownFilename: a filename no stored episode owns is
// refused, not guessed. The watch signal resolves by exact name against the
// paths kishizu recorded; anything else has no identity here, and guessing
// one mis-attributes the watch (a franchise sibling's aliases score above
// threshold).
func TestWatchedRefusesUnknownFilename(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Show", []string{"Show"})
	path := filepath.Join("/media/anime", "Show", "Show - E09.mkv")
	if err := srv.st.FinaliseEpisode(sh.ID, 9, path); err != nil {
		t.Fatalf("finalise: %v", err)
	}

	// Same show, same episode number, but a name kishizu never recorded —
	// a release-style name from outside the pipeline.
	body := `{"path":"/downloads/[BrandNewGroup] Show S01E09 1080p.mkv"}`
	rec := post(t, srv, "/api/watched", body)
	if rec.Code != 422 {
		t.Errorf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Downloaded {
		t.Errorf("state = %v, want downloaded: an unknown filename must not mark anything", ep)
	}
}
