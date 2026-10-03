package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestProjectAirDatesKeepsWatchedHistory: a hiatus moves the schedule anchor
// forward, and re-projecting from that anchor must not stamp future dates
// onto episodes that already aired and were watched.
//
// BLEACH went on break: the anchor jumped from "ep 7 airs Oct 5" to
// "ep 9 airs Oct 19". Re-projecting 1..10 from Oct 19 rewrote ep 7 to
// "airs Oct 5" — coincidentally right — but ep 8 to Oct 12 and ep 9 to
// Oct 19, dates those episodes never had. Any schedule view built from
// episode rows then showed phantom entries for finished episodes.
func TestProjectAirDatesKeepsWatchedHistory(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 14)

	// Episodes 1..8 aired weekly from Aug 24; the user watched them all.
	// The air dates are seeded directly, as they would have been projected
	// week by week while the season was airing.
	start := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	for i := 1; i <= 8; i++ {
		if err := s.UpsertEpisode(sh.ID, i, episode.Watched, "", ""); err != nil {
			t.Fatal(err)
		}
		// setAirsAt now refuses watched rows, so seed the history directly.
		if err := s.seedAirsAt(sh.ID, i, start.AddDate(0, 0, 7*(i-1))); err != nil {
			t.Fatal(err)
		}
	}
	// The pre-hiatus anchor: ep 9 airs Oct 19 (the real weekly slot).
	if err := s.SetNextEpisode(sh.ID, 9, start.AddDate(0, 0, 56)); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}

	// The hiatus: the page now says ep 9 airs Nov 9, three weeks late.
	hiatus := start.AddDate(0, 0, 77)
	if err := s.SetNextEpisode(sh.ID, 9, hiatus); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}

	// Watched episodes keep the dates they earned before the hiatus.
	for num := 1; num <= 8; num++ {
		ep, err := s.GetEpisode(sh.ID, num)
		if err != nil || ep == nil {
			t.Fatalf("ep %d: %v", num, err)
		}
		want := start.AddDate(0, 0, 7*(num-1))
		if ep.AirsAt == nil || !ep.AirsAt.Equal(want) {
			t.Errorf("ep %d airs_at = %v, want %v (history must not move)",
				num, ep.AirsAt, want)
		}
	}

	// The unaired episode at the anchor still moves with the schedule.
	ep, _ := s.GetEpisode(sh.ID, 9)
	if ep == nil || ep.AirsAt == nil || !ep.AirsAt.Equal(hiatus) {
		t.Errorf("ep 9 airs_at = %v, want %v (the anchor is authoritative)",
			ep.AirsAt, hiatus)
	}
}
