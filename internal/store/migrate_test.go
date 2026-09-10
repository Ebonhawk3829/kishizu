package store

import (
	"path/filepath"
	"testing"
)

// TestAddColumnsOnExistingDatabase: CREATE TABLE IF NOT EXISTS silently skips
// tables that already exist, so a column added later never lands on an older
// database. Reopening must add it.
func TestAddColumnsOnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Simulate a database created before the reason column existed.
	if _, err := s.db.Exec(`ALTER TABLE filter DROP COLUMN reason`); err != nil {
		t.Skipf("cannot drop column to simulate old schema: %v", err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	sh, err := s2.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	// Writing and reading a reason proves the column is present and usable.
	if err := s2.AddFilter(sh.ID, Filter{Kind: "resolution", Op: "min", Value: "1080p", Reason: "graded acceptable"}); err != nil {
		t.Fatalf("AddFilter with reason: %v", err)
	}
	got, err := s2.Filters(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Reason != "graded acceptable" {
		t.Errorf("filters = %+v, want the reason preserved", got)
	}
}

// TestReasonRoundTrip: reasons survive a write and read, so a rule can be
// revisited later rather than being an opaque rank.
func TestReasonRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddPreference(sh.ID, Preference{Kind: "codec", Value: "hevc", Rank: 99, Reason: "graded wrong"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Preferences(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Reason != "graded wrong" {
		t.Errorf("preferences = %+v, want the reason preserved", got)
	}
}

// TestEmptyReasonStaysNull: an absent reason must not be stored as "", so
// "no reason given" stays distinguishable from "reason is empty".
func TestEmptyReasonStaysNull(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddFilter(sh.ID, Filter{Kind: "batch", Op: "exclude", Value: "true"}); err != nil {
		t.Fatal(err)
	}
	var reason any
	if err := s.db.QueryRow(`SELECT reason FROM filter WHERE show_id = ?`, sh.ID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != nil {
		t.Errorf("reason = %v, want NULL", reason)
	}
}
