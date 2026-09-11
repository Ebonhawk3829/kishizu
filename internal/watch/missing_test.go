package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestCheckMissingFlagsVanishedFile: an episode kishizu downloaded whose file
// has gone must be flagged, so the user can re-grab or accept the watch signal.
func TestCheckMissingFlagsVanishedFile(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)

	// A file that existed and is now gone.
	p := filepath.Join(lib, "Show - E05.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertEpisode(sh.ID, 5, episode.Downloaded, "H", "rel")
	_ = st.SetFilePath(sh.ID, 5, p)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}

	h := New(st, lib, 2)
	missing, err := h.CheckMissing()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 {
		t.Fatalf("got %d missing, want 1", len(missing))
	}
	ep, _ := st.GetEpisode(sh.ID, 5)
	if ep.State != episode.Missing {
		t.Errorf("state = %s, want missing", ep.State)
	}
}

// TestCheckMissingIgnoresNoPath: an episode marked downloaded by hand has no
// server-side file, so it is NOT missing.
//
// This is the "I have it on my PC" case. Warning about files kishizu never
// created would be noise, and would need a dismiss button to work around.
func TestCheckMissingIgnoresNoPath(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)

	// Marked downloaded via the UI, no path — the file is on the user's PC.
	_ = st.UpsertEpisode(sh.ID, 5, episode.Downloaded, "", "")

	h := New(st, lib, 2)
	missing, err := h.CheckMissing()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Errorf("got %d missing, want 0 (no file_path)", len(missing))
	}
	ep, _ := st.GetEpisode(sh.ID, 5)
	if ep.State != episode.Downloaded {
		t.Errorf("state = %s, want downloaded (untouched)", ep.State)
	}
}

// TestCheckMissingIgnoresPresentFile: a file that is still there is not missing.
func TestCheckMissingIgnoresPresentFile(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)

	p := filepath.Join(lib, "Show - E05.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertEpisode(sh.ID, 5, episode.Downloaded, "H", "rel")
	_ = st.SetFilePath(sh.ID, 5, p)

	h := New(st, lib, 2)
	missing, err := h.CheckMissing()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Errorf("got %d missing, want 0 (file present)", len(missing))
	}
}

// TestCheckMissingSkipsWatched: an already-watched episode is not missing —
// its file may legitimately have been deleted by the sweep.
func TestCheckMissingSkipsWatched(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)

	p := filepath.Join(lib, "Show - E05.mkv")
	_ = st.UpsertEpisode(sh.ID, 5, episode.Watched, "H", "rel")
	_ = st.SetFilePath(sh.ID, 5, p) // file already gone

	h := New(st, lib, 2)
	missing, err := h.CheckMissing()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Errorf("got %d missing, want 0 (already watched)", len(missing))
	}
}
