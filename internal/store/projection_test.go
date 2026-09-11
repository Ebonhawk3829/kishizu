package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestProjectAirDatesGoesPastWatched: when the user has watched beyond the
// schedule's next-episode point, the following episode must still get an air
// date.
//
// The schedule said "ep 11 airs Sep 6" but the user had watched 11, so ep 12
// was the one actually due. Projecting only up to next_ep left ep 12 with no
// row and no air date, so it was invisible to the cycle and never hunted.
func TestProjectAirDatesGoesPastWatched(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 14)
	airs := time.Date(2026, 9, 6, 15, 0, 0, 0, time.UTC)
	if err := s.SetNextEpisode(sh.ID, 11, airs); err != nil {
		t.Fatal(err)
	}
	// The user has watched through ep 11 — past the schedule point.
	for i := 1; i <= 11; i++ {
		if err := s.UpsertEpisode(sh.ID, i, episode.Watched, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}

	ep, err := s.GetEpisode(sh.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	if ep == nil {
		t.Fatal("ep 12 has no row; it would never be hunted")
	}
	if ep.AirsAt == nil {
		t.Fatal("ep 12 has no air date; it would never be hunted")
	}
	// One week after ep 11's air date.
	want := airs.AddDate(0, 0, 7)
	if !ep.AirsAt.Equal(want) {
		t.Errorf("ep 12 airs_at = %v, want %v", ep.AirsAt, want)
	}
}

// TestProjectAirDatesBackfillsEarlier: episodes before the schedule point get
// dates stepping back a week each.
func TestProjectAirDatesBackfillsEarlier(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 14)
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	_ = s.SetNextEpisode(sh.ID, 8, airs)
	if err := s.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}

	cases := map[int]time.Time{
		6: airs.AddDate(0, 0, -14),
		7: airs.AddDate(0, 0, -7),
		8: airs,
	}
	for num, want := range cases {
		ep, _ := s.GetEpisode(sh.ID, num)
		if ep == nil || ep.AirsAt == nil {
			t.Errorf("ep %d missing air date", num)
			continue
		}
		if !ep.AirsAt.Equal(want) {
			t.Errorf("ep %d airs_at = %v, want %v", num, ep.AirsAt, want)
		}
	}
}

// TestProjectAirDatesLeavesWantedAlone: projection must not overwrite the
// state of an episode already in flight.
func TestProjectAirDatesLeavesWantedAlone(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 14)
	_ = s.UpsertEpisode(sh.ID, 3, episode.Downloading, "H", "rel")
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	_ = s.SetNextEpisode(sh.ID, 8, airs)
	if err := s.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}

	ep, _ := s.GetEpisode(sh.ID, 3)
	if ep.State != episode.Downloading {
		t.Errorf("ep 3 state = %s, want downloading (untouched)", ep.State)
	}
}
