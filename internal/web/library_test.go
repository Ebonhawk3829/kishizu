package web

import (
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/naming"
)

// TestWatchedAcceptsLibraryFilename: a filename kishizu wrote itself must be
// accepted without the confidence gate.
//
// A library name has no release group, so it scores only 0.5 — below the
// 0.75 threshold. Requiring confidence here rejected every watch signal,
// since the player sends back exactly the name kishizu wrote.
func TestWatchedAcceptsLibraryFilename(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Clevatess Season 2", []string{"Clevatess"}, 13)
	_ = st.UpsertEpisode(sh.ID, 9, episode.Downloaded, "H", "rel")

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
//
// It now goes through the naming scheme rather than a fixed regex, so a
// configured layout is recognised just as the default is.
func TestIsLibraryForm(t *testing.T) {
	srv := testServer(t)
	scheme, err := naming.Resolve(naming.PresetKishizu, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetNaming(scheme)

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
		if !srv.isLibraryForm(s) {
			t.Errorf("isLibraryForm(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if srv.isLibraryForm(s) {
			t.Errorf("isLibraryForm(%q) = true, want false", s)
		}
	}
}

// TestIsLibraryFormFollowsTheScheme: a custom layout must be recognised too.
// A scheme that cannot read its own output leaves every watch signal to the
// fuzzy path, where a library name scores too low to pass the confidence gate
// and nothing is ever marked watched.
func TestIsLibraryFormFollowsTheScheme(t *testing.T) {
	srv := testServer(t)
	scheme, err := naming.Resolve(naming.PresetSonarr, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetNaming(scheme)

	if !srv.isLibraryForm("Show - S01E09.mkv") {
		t.Error("sonarr scheme must recognise its own name")
	}
	if srv.isLibraryForm("Show - E09.mkv") {
		t.Error("sonarr scheme must not claim the kishizu form")
	}
}

// TestServerAlwaysHasAScheme: a server with no scheme could not recognise
// kishizu's own filenames, which silently breaks deletion — the watch signal
// would fall through to the fuzzy path, where a library name scores too low
// to pass the confidence gate. So New installs the default rather than
// leaving it to the caller.
func TestServerAlwaysHasAScheme(t *testing.T) {
	srv := testServer(t)
	if srv.naming == nil {
		t.Fatal("New must install a default naming scheme")
	}
	if !srv.isLibraryForm("Show - E09.mkv") {
		t.Error("the default scheme must recognise the default layout")
	}
}
