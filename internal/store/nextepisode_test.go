package store

import (
	"testing"
	"time"
)

// TestSetNextEpisodeClampsZero: the timetable renders "Ep 0" for a show that
// has been announced but has not premiered. Stored verbatim it breaks
// everything downstream — episode numbers are 1-based, plausible() rejects
// anything below 1, and the season-complete check (next > max) can never fire
// for a next of 0.
//
// Episode 1 is the honest reading: the next episode is the first one. The
// daily refresh corrects the time once the show actually appears.
func TestSetNextEpisodeClampsZero(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	at := time.Now().AddDate(0, 0, 7)

	for _, in := range []int{0, -3} {
		if err := s.SetNextEpisode(sh.ID, in, at); err != nil {
			t.Fatalf("SetNextEpisode(%d): %v", in, err)
		}
		n, got, err := s.NextEpisode(sh.ID)
		if err != nil {
			t.Fatalf("NextEpisode: %v", err)
		}
		if n != 1 {
			t.Errorf("SetNextEpisode(%d) stored next_ep = %d, want 1", in, n)
		}
		if got == nil {
			t.Errorf("SetNextEpisode(%d) stored no air time", in)
		}
	}
}

// TestSetNextEpisodeKeepsRealNumbers: the clamp must not disturb a genuine
// mid-season episode number.
func TestSetNextEpisodeKeepsRealNumbers(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	at := time.Now()
	if err := s.SetNextEpisode(sh.ID, 7, at); err != nil {
		t.Fatalf("SetNextEpisode: %v", err)
	}
	n, _, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatalf("NextEpisode: %v", err)
	}
	if n != 7 {
		t.Errorf("next_ep = %d, want 7", n)
	}
}
