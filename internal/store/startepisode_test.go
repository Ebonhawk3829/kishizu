package store

import (
	"testing"
	"time"
)

// TestStartEpisodeFloorsProgress: a show added mid-season with "start at the
// next episode" floors progress at that episode, so the watch gate never
// reaches back into the backlog the user chose to skip.
func TestStartEpisodeFloorsProgress(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", nil)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}

	if got := s.NextUnwatched(sh.ID); got != 1 {
		t.Fatalf("fresh show NextUnwatched = %d, want 1", got)
	}
	if err := s.SetStartEpisode(sh.ID, 6); err != nil {
		t.Fatalf("SetStartEpisode: %v", err)
	}
	if got := s.NextUnwatched(sh.ID); got != 6 {
		t.Errorf("NextUnwatched after floor = %d, want 6", got)
	}
	// The floor is not a watched signal: nothing is invented for the skipped
	// episodes, so they can never be rendered as consumed.
	eps, err := s.EpisodesForShow(sh.ID)
	if err != nil {
		t.Fatalf("EpisodesForShow: %v", err)
	}
	if len(eps) != 0 {
		t.Errorf("SetStartEpisode created %d episode rows, want 0", len(eps))
	}
	fresh, err := s.GetShow(sh.ID)
	if err != nil {
		t.Fatalf("GetShow: %v", err)
	}
	if fresh.StartEp != 6 {
		t.Errorf("StartEp = %d, want 6", fresh.StartEp)
	}
}

// TestStartEpisodePinsAnchor: with a floor set, the effective anchor is the
// floor. The anchor episode is materialised; episode 1 is not.
func TestStartEpisodePinsAnchor(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", nil)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	if err := s.SetStartEpisode(sh.ID, 6); err != nil {
		t.Fatalf("SetStartEpisode: %v", err)
	}
	if err := s.SetNextEpisode(sh.ID, 6, time.Now().AddDate(0, 0, 3)); err != nil {
		t.Fatalf("SetNextEpisode: %v", err)
	}
	n, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatalf("NextEpisode: %v", err)
	}
	if n != 6 {
		t.Errorf("effective anchor = %d, want 6", n)
	}
	if at == nil {
		t.Error("effective anchor has no air time")
	}
	if ep, _ := s.GetEpisode(sh.ID, 6); ep == nil {
		t.Error("floor episode has no row")
	}
	if ep, _ := s.GetEpisode(sh.ID, 1); ep != nil {
		t.Error("episode 1 was materialised despite the floor")
	}
}

// TestStartEpisodeClearsBelowTwo: setting the floor to 1 clears it, so the
// whole season is in play again.
func TestStartEpisodeClearsBelowTwo(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", nil)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	if err := s.SetStartEpisode(sh.ID, 5); err != nil {
		t.Fatalf("SetStartEpisode(5): %v", err)
	}
	if err := s.SetStartEpisode(sh.ID, 1); err != nil {
		t.Fatalf("SetStartEpisode(1): %v", err)
	}
	if got := s.NextUnwatched(sh.ID); got != 1 {
		t.Errorf("NextUnwatched after clearing the floor = %d, want 1", got)
	}
	fresh, err := s.GetShow(sh.ID)
	if err != nil {
		t.Fatalf("GetShow: %v", err)
	}
	if fresh.StartEp != 0 {
		t.Errorf("StartEp = %d after clear, want 0", fresh.StartEp)
	}
}
