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
