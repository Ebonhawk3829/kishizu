package adopt

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Does marking an episode "downloading" avoid the hunting label?
// StateOf's switch does not list Downloading, so it falls through to the
// "wanted or downloading" branch, which needs an air date to say anything
// other than hunting.
func TestDownloadingStillReadsAsHunting(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("DanMachi III", nil, 12)

	// Every lifecycle state, with NO air date, as adoption would create it.
	for _, s := range []episode.State{
		episode.Wanted, episode.Downloading, episode.Downloaded,
		episode.Watched, episode.Deleted,
	} {
		if err := st.SetEpisodeState(sh.ID, 1, s); err != nil {
			t.Fatal(err)
		}
		ep, _ := st.GetEpisode(sh.ID, 1)
		t.Logf("%-12s -> %s", s, cycle.StateOf(ep, time.Now()))
	}
}
