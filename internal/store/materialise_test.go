package store

import (
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestSetNextEpisodeMaterialisesAnchorRow: the anchor episode must have a row,
// or the state derivation cannot see it.
//
// The state a show card renders is derived from episode rows. An anchor
// episode with no row is an aired, unwatched episode that has nowhere to be
// hunting from, so it reads as up-to-date and is never grabbed. The consume
// paths stamp exposure as they advance the anchor, so this only arises for a
// consume that predates the stamp or any later path that moves the anchor
// without one — the backstop is what this pins.
func TestSetNextEpisodeMaterialisesAnchorRow(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)
	// Ep1 consumed, so the effective anchor is ep2 rather than the user's
	// progress — the row that must exist is the episode being hunted.
	_ = s.UpsertEpisode(sh.ID, 1, episode.Watched, "", "")

	if err := s.SetNextEpisode(sh.ID, 2, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	ep, err := s.GetEpisode(sh.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ep == nil {
		t.Fatal("anchor episode has no row: it can never read as hunting")
	}
	if ep.State != episode.Wanted {
		t.Errorf("state = %q, want wanted", ep.State)
	}
}

// TestMaterialiseLeavesExistingRowsAlone: creation is the only thing this does.
//
// A terminal row means the episode is consumed and the anchor is merely
// stale; a wanted or later row needs no help. Overwriting either would
// resurrect an episode or discard a grab in flight.
func TestMaterialiseLeavesExistingRowsAlone(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)

	// Distinct episodes: the latch refuses to move one row backwards, so a
	// shared number would test the latch rather than this method.
	for n, st := range map[int]episode.State{
		5: episode.Watched,
		6: episode.Downloading,
		7: episode.Downloaded,
	} {
		_ = s.UpsertEpisode(sh.ID, n, st, "H", "rel")
		if err := s.SetNextEpisode(sh.ID, n, time.Now()); err != nil {
			t.Fatal(err)
		}
		ep, err := s.GetEpisode(sh.ID, n)
		if err != nil {
			t.Fatal(err)
		}
		if ep.State != st {
			t.Errorf("ep%d state = %q, want %q left untouched", n, ep.State, st)
		}
	}
}

// TestMaterialiseOnWatchSignal: consuming the premiere must materialise the
// next episode, so the show is not left inert by a row gap.
//
// This is the PSYREN shape: ep1 consumed, the anchor at ep2, and no row for
// ep2 until something creates one. The watch path stamps exposure on the next
// episode, which creates the row; this pins that the row appears and reads as
// wanted.
func TestMaterialiseOnWatchSignal(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)

	_ = s.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -7))
	_ = s.UpsertEpisode(sh.ID, 1, episode.Watched, "", "")
	if err := s.StampExposure(sh.ID, s.NextUnwatched(sh.ID)); err != nil {
		t.Fatal(err)
	}

	ep, err := s.GetEpisode(sh.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ep == nil || ep.State != episode.Wanted {
		t.Fatalf("next episode row = %+v, want a wanted row for ep2", ep)
	}
}

// TestMaterialiseUsesEffectiveAnchorNotCursor: when the user is behind the
// season, the episode they are owed is the one that must get a row.
//
// The effective anchor is the earlier of the cursor and the user's progress.
// A user at ep4 with the site at ep6 is owed ep4; materialising the cursor
// here would create a row for ep6 and leave ep4 — the episode actually owed —
// with none, so the show would still be inert.
func TestMaterialiseUsesEffectiveAnchorNotCursor(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)

	// The user is behind: watched through ep4, so the effective anchor is ep5
	// while the site's countdown sits at ep6.
	for i := 1; i <= 4; i++ {
		_ = s.UpsertEpisode(sh.ID, i, episode.Watched, "", "")
	}
	if err := s.SetNextEpisode(sh.ID, 6, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The effective anchor is ep5 (progress + 1), so that is the row that
	// appears — not the cursor's ep6.
	if ep, _ := s.GetEpisode(sh.ID, 5); ep == nil {
		t.Fatal("ep5 has no row: the episode the user is owed is invisible")
	}
	if ep, _ := s.GetEpisode(sh.ID, 6); ep != nil {
		t.Error("ep6 row was created; the cursor is not what the user is owed")
	}
}
