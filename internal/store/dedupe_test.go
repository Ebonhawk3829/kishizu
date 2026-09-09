package store

import (
	"path/filepath"
	"testing"
)

// TestAddFilterIsIdempotent: rejecting the same thing twice must not create two
// rows. These tables were originally plain INSERTs, so repeated rejections
// piled up duplicates that later made the unique indexes impossible to create.
func TestAddFilterIsIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, err := s.CreateShow("Tomb Raider King", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	f := Filter{Kind: "batch", Op: "exclude", Value: "true"}
	for i := 0; i < 3; i++ {
		if err := s.AddFilter(sh.ID, f); err != nil {
			t.Fatalf("AddFilter %d: %v", i, err)
		}
	}
	got, err := s.Filters(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d filters, want 1: %+v", len(got), got)
	}
}

// TestAddPreferenceUpdatesRank: re-grading the same value replaces the rank
// instead of adding a conflicting second row.
func TestAddPreferenceUpdatesRank(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, err := s.CreateShow("Tomb Raider King", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddPreference(sh.ID, Preference{Kind: "codec", Value: "hevc", Rank: 99}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPreference(sh.ID, Preference{Kind: "codec", Value: "hevc", Rank: 0}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Preferences(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d preferences, want 1: %+v", len(got), got)
	}
	if got[0].Rank != 0 {
		t.Errorf("rank = %d, want 0 (last write wins)", got[0].Rank)
	}
}

// TestMigrateDedupesLegacyRows: a database written before the unique indexes
// existed contains duplicates. Opening it must clean them rather than fail.
func TestMigrateDedupesLegacyRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Build a legacy-shaped database: no unique indexes, duplicate rows.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sh, err := s.CreateShow("Tomb Raider King", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS uq_filter`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.db.Exec(
			`INSERT INTO filter (show_id, kind, op, value) VALUES (?, 'batch', 'exclude', 'true')`,
			sh.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	// Reopening must succeed and leave exactly one row.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen legacy db: %v", err)
	}
	defer s2.Close()

	got, err := s2.Filters(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d filters after migration, want 1: %+v", len(got), got)
	}
}
