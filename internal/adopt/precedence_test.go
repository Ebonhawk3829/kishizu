package adopt

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// The airing case: an episode that is downloading AND has an air date inside
// the window. Today it reads hunting, which is what makes DueShows poll at
// the aggressive rate while a download is in flight.
func TestAiringDownloadingStillHunting(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Airing Show", nil, 12)
	// Air date 1 hour ago: inside the 72h window.
	aired := time.Now().Add(-time.Hour)
	if err := st.SetNextEpisode(sh.ID, 5, aired); err != nil {
		t.Fatal(err)
	}
	if err := st.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEpisode(sh.ID, 5, episode.Downloading, "H", "rel"); err != nil {
		t.Fatal(err)
	}

	ep, _ := st.GetEpisode(sh.ID, 5)
	t.Logf("airsAt set: %v", ep.AirsAt != nil)
	t.Logf("state: %s", cycle.StateOf(ep, time.Now()))
}
