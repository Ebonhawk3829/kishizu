package store

import (
	"path/filepath"
	"testing"
)

// TestMatcherGroupOffsetDoesNotMatchBySubstring: the store's Matcher had its own
// group lookup with unrestricted substring matching, so "BrandNewGroup"
// inherited group A's offset even after MemShow was fixed. Both implementations
// must behave the same way.
func TestMatcherGroupOffsetDoesNotMatchBySubstring(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupOffset(sh.ID, "A", 40, "training"); err != nil {
		t.Fatal(err)
	}
	m, err := s.NewMatcher(sh)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.GroupOffset("BrandNewGroup"); ok {
		t.Error("BrandNewGroup matched group A by substring")
	}
	if v, ok := m.GroupOffset("A"); !ok || v != 40 {
		t.Errorf("GroupOffset(A) = %d,%v want 40,true", v, ok)
	}
}

// TestMatcherGroupOffsetNormalisesPunctuation: the same group written with
// different punctuation must still resolve.
func TestMatcherGroupOffsetNormalisesPunctuation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupOffset(sh.ID, "Erai-raws", 0, "training"); err != nil {
		t.Fatal(err)
	}
	m, err := s.NewMatcher(sh)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"Erai-raws", "Erai_raws", "erai-raws", "[Erai-raws]"} {
		if _, ok := m.GroupOffset(g); !ok {
			t.Errorf("GroupOffset(%q) did not match Erai-raws", g)
		}
	}
}
