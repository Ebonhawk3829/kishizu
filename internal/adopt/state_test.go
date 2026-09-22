package adopt

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestAdoptedEpisodeStatesRender: every lifecycle state an adoption creates,
// with NO air date, must render as something other than "hunting" for the
// states that are not hunting.
//
// StateOf handles Downloading explicitly; the fallthrough to hunting is real
// only for "wanted" episodes with no air date, which is deliberate — an
// untrained show must not silently disappear from the UI.
func TestAdoptedEpisodeStatesRender(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("DanMachi III", nil, 12)

	// Every lifecycle state, with NO air date, as adoption would create it.
	cases := map[episode.State]cycle.State{
		episode.Wanted:      cycle.Hunting, // deliberate: no air date means hunting, so the show stays visible
		episode.Downloading: cycle.Downloading,
		episode.Downloaded:  cycle.ReadyToWatch,
		episode.Watched:     cycle.UpToDate,
		episode.Deleted:     cycle.UpToDate,
	}
	for s, want := range cases {
		if err := st.SetEpisodeState(sh.ID, 1, s); err != nil {
			t.Fatal(err)
		}
		ep, err := st.GetEpisode(sh.ID, 1)
		if err != nil || ep == nil {
			t.Fatalf("episode for state %s: %v", s, err)
		}
		if got := cycle.StateOf(ep, time.Now()); got != want {
			t.Errorf("state %s with no air date renders as %q, want %q", s, got, want)
		}
	}
}
