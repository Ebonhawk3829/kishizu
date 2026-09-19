package adopt

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Simulates an adopted season end to end against the real store and cycle
// code: a show with no slug and no air dates, episodes marked downloading,
// then finalised the way Reconcile would.
func TestSimAdoptedSeason(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("DanMachi III", nil, 12)
	for i := 1; i <= 12; i++ {
		if err := st.UpsertEpisode(sh.ID, i, episode.Downloading, "HASH", "rel"); err != nil {
			t.Fatal(err)
		}
	}

	eps, _ := st.EpisodesForShow(sh.ID)
	var states []cycle.State
	for _, ep := range eps {
		states = append(states, cycle.StateOf(ep, time.Now()))
	}
	t.Logf("DOWNLOADING states: %v", states)
	if d, ok := cycle.PollInterval(states); ok {
		t.Logf("POLL: %v (show IS due -> would be polled)", d)
	} else {
		t.Logf("POLL: not due")
	}

	for i := 1; i <= 12; i++ {
		p := filepath.Join("/media/anime", "DanMachi III", "DanMachi III - E01.mkv")
		if err := st.FinaliseEpisode(sh.ID, i, p); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ = st.EpisodesForShow(sh.ID)
	states = nil
	for _, ep := range eps {
		states = append(states, cycle.StateOf(ep, time.Now()))
	}
	t.Logf("FINALISED states: %v", states)
	t.Logf("NextUnwatched: %d", st.NextUnwatched(sh.ID))
}
