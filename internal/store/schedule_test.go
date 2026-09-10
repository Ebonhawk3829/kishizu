package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestNextEpisodeRoundTrip: the schedule's next-episode point survives a write
// and read.
func TestNextEpisodeRoundTrip(t *testing.T) {
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
	n, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Errorf("n = %d, want 8", n)
	}
	if at == nil || !at.Equal(airs) {
		t.Errorf("at = %v, want %v", at, airs)
	}
}

// TestNextEpisodeNilWhenUnknown: a show not on the schedule has no point, and
// that must be distinguishable from a zero time.
func TestNextEpisodeNilWhenUnknown(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	n, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || at != nil {
		t.Errorf("n = %d, at = %v, want 0, nil", n, at)
	}
}

// TestProjectAirDates: episodes before the schedule's next episode project back
// a week at a time, and each gets an airs_at even if the episode row did not
// exist yet.
func TestProjectAirDates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	// Ep 8 airs Sep 13; so ep 7 aired Sep 6, ep 6 Aug 29.
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if err := s.SetNextEpisode(sh.ID, 8, airs); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}

	cases := map[int]string{
		6: "2026-08-30", // Sep 13 minus 2 weeks (UTC date may shift; checked loosely below)
		7: "2026-09-06",
		8: "2026-09-13",
	}
	for ep, wantDate := range cases {
		e, err := s.GetEpisode(sh.ID, ep)
		if err != nil {
			t.Fatal(err)
		}
		if e == nil || e.AirsAt == nil {
			t.Errorf("ep %d has no airs_at", ep)
			continue
		}
		if got := e.AirsAt.Format("2006-01-02"); got != wantDate {
			t.Errorf("ep %d airs_at = %s, want %s", ep, got, wantDate)
		}
	}
}

// TestAdvanceScheduleOnConfirm: the schedule point is held until a download
// confirms the episode. Grabbing ep 8 moves the pointer to ep 9, one week on.
func TestAdvanceScheduleOnConfirm(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	_ = s.SetNextEpisode(sh.ID, 8, airs)

	if err := s.AdvanceSchedule(sh.ID, 8); err != nil {
		t.Fatal(err)
	}
	n, at, _ := s.NextEpisode(sh.ID)
	if n != 9 {
		t.Errorf("n = %d, want 9", n)
	}
	want := airs.AddDate(0, 0, 7)
	if at == nil || !at.Equal(want) {
		t.Errorf("at = %v, want %v", at, want)
	}
}

// TestAdvanceScheduleIgnoresBackfill: grabbing an OLDER episode must not move
// the pointer, since the schedule's next episode has not been confirmed.
func TestAdvanceScheduleIgnoresBackfill(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	_ = s.SetNextEpisode(sh.ID, 8, airs)

	if err := s.AdvanceSchedule(sh.ID, 5); err != nil {
		t.Fatal(err)
	}
	n, _, _ := s.NextEpisode(sh.ID)
	if n != 8 {
		t.Errorf("n = %d, want 8 (backfill must not advance the pointer)", n)
	}
}
