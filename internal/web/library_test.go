package web

import (
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestWatchedAcceptsLibraryFilename: a filename kishizu wrote itself must be
// accepted without the confidence gate.
//
// A library name has no release group, so it scores only 0.5 — below the
// 0.75 threshold. Requiring confidence here rejected every watch signal,
// since the mpv script sends back exactly the name kishizu wrote.
func TestWatchedAcceptsLibraryFilename(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Clevatess Season 2", []string{"Clevatess"}, 13)
	_ = st.UpsertEpisode(sh.ID, 9, episode.Downloaded, "H", "rel")

	// A Windows path, as mpv on the user's PC would send it.
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

// TestWatchedStillRejectsUncertainNonLibraryNames: the confidence gate must
// still apply to names that are not kishizu's own output, or a wrong guess
// could delete a file the user wants.
func TestWatchedStillRejectsUncertainNonLibraryNames(t *testing.T) {
	srv := testServer(t)
	sh, _ := srv.st.CreateShow("Show", []string{"Show"}, 12)
	// Offsets disagree, so an unseen group is a coin flip.
	_ = srv.st.SetGroupOffset(sh.ID, "A", 0, "training")
	_ = srv.st.SetGroupOffset(sh.ID, "B", 40, "training")

	body := `{"path":"/downloads/[BrandNewGroup] Show S01E09 1080p.mkv"}`
	rec := post(t, srv, "/api/watched", body)
	if rec.Code != 422 {
		t.Errorf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// TestIsLibraryForm: the recogniser must accept kishizu's own names and reject
// ordinary release titles.
func TestIsLibraryForm(t *testing.T) {
	yes := []string{
		"Clevatess Season 2 - E09.mkv",
		"Tomb Raider King - E10.mkv",
		"Show - E1.mp4",
	}
	no := []string{
		"[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL",
		"Show - 09 [1080p]",
		"[Group] Show - 47 (1080p)",
	}
	for _, s := range yes {
		if !isLibraryForm(s) {
			t.Errorf("isLibraryForm(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isLibraryForm(s) {
			t.Errorf("isLibraryForm(%q) = true, want false", s)
		}
	}
}
