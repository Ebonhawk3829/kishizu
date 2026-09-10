package match

import "testing"

// TestGroupOffsetDoesNotMatchBySubstring: an unrelated group must not inherit
// another group's offset just because its name contains it.
//
// Found via confidence: "BrandNewGroup" contains "A", so it silently borrowed
// group A's offset. That both mis-resolved the episode and inflated confidence,
// because the offset looked known when it was not.
func TestGroupOffsetDoesNotMatchBySubstring(t *testing.T) {
	m := &MemShow{Name: "Show", Max: 12, Offsets: map[string]int{"A": 40}}

	if _, ok := m.GroupOffset("BrandNewGroup"); ok {
		t.Error("BrandNewGroup matched group A by substring")
	}
	if _, ok := m.GroupOffset("VARYG"); ok {
		t.Error("VARYG matched group A by substring")
	}
}

// TestGroupOffsetExactStillWorks: the fix must not break ordinary lookups.
func TestGroupOffsetExactStillWorks(t *testing.T) {
	m := &MemShow{Name: "Show", Max: 12, Offsets: map[string]int{"SubsPlease": 40}}
	if v, ok := m.GroupOffset("SubsPlease"); !ok || v != 40 {
		t.Errorf("GroupOffset(SubsPlease) = %d,%v want 40,true", v, ok)
	}
}

// TestGroupOffsetNormalisesPunctuation: groups are written with hyphens,
// underscores and dots interchangeably, and must still match.
func TestGroupOffsetNormalisesPunctuation(t *testing.T) {
	m := &MemShow{Name: "Show", Max: 12, Offsets: map[string]int{"Erai-raws": 0}}
	for _, g := range []string{"Erai-raws", "Erai_raws", "Erai.raws", "erai-raws", "Erai raws"} {
		if _, ok := m.GroupOffset(g); !ok {
			t.Errorf("GroupOffset(%q) did not match Erai-raws", g)
		}
	}
}

// TestGroupOffsetSubstringForLongNames: the substring fallback is kept for
// bracketed variants, but only for names long enough to be distinctive.
func TestGroupOffsetSubstringForLongNames(t *testing.T) {
	m := &MemShow{Name: "Show", Max: 12, Offsets: map[string]int{"SubsPlease": 40}}
	if v, ok := m.GroupOffset("[SubsPlease]"); !ok || v != 40 {
		t.Errorf("GroupOffset([SubsPlease]) = %d,%v want 40,true", v, ok)
	}
}

// TestUnseenGroupIsNotConfidentWhenOffsetsDisagree: end-to-end check that an
// unseen group with contradictory known offsets is not treated as confident.
func TestUnseenGroupIsNotConfidentWhenOffsetsDisagree(t *testing.T) {
	m := &MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"ToonsHub": 0, "Erai-raws": 40, "SubsPlease": 7}}
	res := Match(m, "[BrandNewGroup] Tomb Raider King S01E09 1080p WEB-DL")
	if !res.Matched {
		t.Fatal("should match on alias")
	}
	if res.Confident() {
		t.Errorf("unseen group with disagreeing offsets should not be confident, got %.2f (%s)",
			res.Confidence, res.Reason)
	}
}
