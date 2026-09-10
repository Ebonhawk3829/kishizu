package store

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestMarkWatchedUpToLatchesWantedEpisodes: an episode that already has a row
// in "wanted" state must still be latched.
//
// Air-date projection creates a wanted row for every episode up to the
// schedule point, so nearly every episode has a row. The original
// implementation skipped any episode with a row, making this a no-op for
// exactly the common case.
func TestMarkWatchedUpToLatchesWantedEpisodes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	// Simulate what ProjectAirDates leaves behind: wanted rows with air dates.
	for i := 1; i <= 5; i++ {
		if err := s.UpsertEpisode(sh.ID, i, episode.Wanted, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	marked, err := s.MarkWatchedUpTo(sh.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if marked != 5 {
		t.Errorf("marked %d, want 5", marked)
	}
	for i := 1; i <= 5; i++ {
		ep, _ := s.GetEpisode(sh.ID, i)
		if ep.State != episode.Watched {
			t.Errorf("ep %d state = %s, want watched", i, ep.State)
		}
	}
}

// TestMarkWatchedUpToLeavesInFlightAlone: an episode already downloading or
// downloaded must not be re-latched, since that would be surprising.
func TestMarkWatchedUpToLeavesInFlightAlone(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	if err := s.UpsertEpisode(sh.ID, 1, episode.Watched, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEpisode(sh.ID, 2, episode.Downloading, "H", "rel"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEpisode(sh.ID, 3, episode.Downloaded, "H", "rel"); err != nil {
		t.Fatal(err)
	}

	marked, err := s.MarkWatchedUpTo(sh.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if marked != 0 {
		t.Errorf("marked %d, want 0 (all already in flight or consumed)", marked)
	}
	ep2, _ := s.GetEpisode(sh.ID, 2)
	if ep2.State != episode.Downloading {
		t.Errorf("ep 2 state = %s, want downloading (untouched)", ep2.State)
	}
	ep3, _ := s.GetEpisode(sh.ID, 3)
	if ep3.State != episode.Downloaded {
		t.Errorf("ep 3 state = %s, want downloaded (untouched)", ep3.State)
	}
}

// TestMarkWatchedUpToIsIdempotent: running it twice must not double-count or
// error, since the UI may be used repeatedly.
func TestMarkWatchedUpToIsIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	first, err := s.MarkWatchedUpTo(sh.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if first != 4 {
		t.Errorf("first marked %d, want 4", first)
	}
	second, err := s.MarkWatchedUpTo(sh.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if second != 0 {
		t.Errorf("second marked %d, want 0 (already watched)", second)
	}
}
