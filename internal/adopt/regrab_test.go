package adopt

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// The safety valve: after unlatching a downloaded episode, does a re-grab
// skip the release we already have?
//
// MarkSeen records the infohash permanently, and HasSeen is the first check
// in the listener's evaluate. So the previously-grabbed release is excluded
// and the next best one wins.
func TestRegrabSkipsPreviousRelease(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Show", nil, 12)

	// Grab release A, then it lands on disk.
	if err := st.MarkSeen("HASH_A", sh.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEpisode(sh.ID, 5, episode.Downloading, "HASH_A", "Release A"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join("/media/anime", "Show", "Show - E05.mkv")
	if err := st.FinaliseEpisode(sh.ID, 5, p); err != nil {
		t.Fatal(err)
	}

	ep, _ := st.GetEpisode(sh.ID, 5)
	t.Logf("after download: state=%s hash=%s title=%s", ep.State, ep.InfoHash, ep.ReleaseTitle)

	// Unlatch, as the Re-grab button does.
	if err := st.Unlatch(sh.ID, 5); err != nil {
		t.Fatal(err)
	}
	ep, _ = st.GetEpisode(sh.ID, 5)
	t.Logf("after unlatch:  state=%s hash=%q title=%q path=%q", ep.State, ep.InfoHash, ep.ReleaseTitle, ep.FilePath)

	// The seen check is what excludes the old release on the next grab.
	seenA, _ := st.HasSeen("HASH_A")
	seenB, _ := st.HasSeen("HASH_B")
	t.Logf("HasSeen(A)=%v (must be true: skipped next time)", seenA)
	t.Logf("HasSeen(B)=%v (must be false: eligible)", seenB)

	if !seenA {
		t.Error("previous release not recorded as seen; a re-grab could take it again")
	}
	if seenB {
		t.Error("a different release is wrongly marked seen")
	}
	if ep.State != episode.Wanted {
		t.Errorf("state after unlatch = %s, want wanted", ep.State)
	}
}
