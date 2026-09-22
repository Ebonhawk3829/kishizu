package adopt

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Stage 4: after adoption, does the player watch signal resolve?
// matchFile tries FindByFileName (exact, on the stored path) first, then
// falls back to parsing. The library form is exempt from the confidence gate.
func TestSimWatchSignal(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("DanMachi III", nil, 12)
	// Adoption never trains, so there are no offsets at all.
	path := filepath.Join("/media/anime", "DanMachi III", "DanMachi III - E05.mkv")
	if err := st.FinaliseEpisode(sh.ID, 5, path); err != nil {
		t.Fatal(err)
	}

	// The exact-path lookup is what the watch signal hits first.
	ep, err := st.FindByFileName("DanMachi III - E05.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if ep == nil {
		t.Fatal("FindByFileName did not resolve the library filename")
	}
	t.Logf("resolved: show=%d ep=%d state=%s", ep.ShowID, ep.Number, ep.State)

	// And the sweep only considers watched episodes with a path.
	if err := st.UpsertEpisode(sh.ID, 5, episode.Watched, "", ""); err != nil {
		t.Fatal(err)
	}
	eps, _ := st.EpisodesForShow(sh.ID)
	for _, e := range eps {
		if e.State == episode.Watched && e.FilePath != "" {
			t.Logf("sweepable: ep%d path=%s", e.Number, e.FilePath)
		}
	}
}
