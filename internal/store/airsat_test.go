package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestEpisodesForShowLoadsAirsAt: the list query must return airs_at, not just
// the single-episode getter.
//
// EpisodesForShow and EpisodesByState did not select the column, so every
// caller saw a nil air date. The cycle package then fell back to "hunting"
// for every episode, which meant the four-state model never actually ran and
// every show looked permanently due.
func TestEpisodesForShowLoadsAirsAt(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if err := s.SetNextEpisode(sh.ID, 8, airs); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}

	eps, err := s.EpisodesForShow(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) == 0 {
		t.Fatal("no episodes")
	}
	for _, ep := range eps {
		if ep.AirsAt == nil {
			t.Errorf("ep %d has nil AirsAt from EpisodesForShow", ep.Number)
		}
	}
}

// TestEpisodesByStateLoadsAirsAt: same column, other query.
func TestEpisodesByStateLoadsAirsAt(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	_ = s.SetNextEpisode(sh.ID, 8, airs)
	_ = s.ProjectAirDates(sh.ID)

	eps, err := s.EpisodesByState("wanted")
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) == 0 {
		t.Fatal("no wanted episodes")
	}
	for _, ep := range eps {
		if ep.AirsAt == nil {
			t.Errorf("ep %d has nil AirsAt from EpisodesByState", ep.Number)
		}
	}
}

// TestAirsAtRoundTrips: the value that comes back must equal the one stored,
// not merely be non-nil.
func TestAirsAtRoundTrips(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	airs := time.Date(2026, 9, 13, 14, 0, 0, 0, time.UTC)
	_ = s.SetNextEpisode(sh.ID, 8, airs)
	_ = s.ProjectAirDates(sh.ID)

	ep, err := s.GetEpisode(sh.ID, 8)
	if err != nil || ep == nil {
		t.Fatalf("get ep 8: %v", err)
	}
	if ep.AirsAt == nil {
		t.Fatal("ep 8 has no airs_at")
	}
	if !ep.AirsAt.Equal(airs) {
		t.Errorf("airs_at = %v, want %v", ep.AirsAt, airs)
	}
}
