package train

import (
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestResolutionGradesDoNotContradict: grading a resolution wrong and then
// acceptable must not leave both "exclude 2160p" and "min 2160p" standing.
//
// Found on real data: show 6 ended up with both rules, which would make the
// listener's behaviour undefined once it starts consuming filters.
func TestResolutionGradesDoNotContradict(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[Feibanyama] Tomb Raider King S01E09 2160p HEVC", 9)

	// First: 2160p is too big, exclude it.
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeWrong}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if !hasFilter(t, st, sh.ID, "resolution", "exclude", "2160p") {
		t.Fatal("expected exclude 2160p after the first grade")
	}

	// Then: actually it is fine, accept it as the floor.
	s2, _ := NewSession(st, sh, 9)
	if _, err := s2.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeAcceptable}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s2.Commit(); err != nil {
		t.Fatal(err)
	}

	if hasFilter(t, st, sh.ID, "resolution", "exclude", "2160p") {
		t.Error("exclude 2160p survived an acceptable grade; rules now contradict")
	}
	if !hasFilter(t, st, sh.ID, "resolution", "min", "2160p") {
		t.Error("expected min 2160p floor")
	}
}

// TestExcludeResolutionRetractsLowerFloor: excluding a resolution must retract a
// floor at or below it, since "min 1080p" and "exclude 2160p" can both hold but
// "min 2160p" and "exclude 2160p" cannot.
func TestExcludeResolutionRetractsLowerFloor(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)

	s, _ := NewSession(st, sh, 9)
	g := Inspect("[Feibanyama] Tomb Raider King S01E09 2160p HEVC", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeAcceptable}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if !hasFilter(t, st, sh.ID, "resolution", "min", "2160p") {
		t.Fatal("expected min 2160p floor")
	}

	s2, _ := NewSession(st, sh, 9)
	if _, err := s2.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeWrong}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s2.Commit(); err != nil {
		t.Fatal(err)
	}
	if hasFilter(t, st, sh.ID, "resolution", "min", "2160p") {
		t.Error("min 2160p survived an exclude 2160p grade; rules now contradict")
	}
}

// TestRetractWithinSession: a contradiction introduced and resolved inside one
// session must not reach the database at all.
func TestRetractWithinSession(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[Feibanyama] Tomb Raider King S01E09 2160p HEVC", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeWrong}, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeAcceptable}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	if hasFilter(t, st, sh.ID, "resolution", "exclude", "2160p") {
		t.Error("exclude 2160p written despite being retracted in the same session")
	}
	if !hasFilter(t, st, sh.ID, "resolution", "min", "2160p") {
		t.Error("expected min 2160p floor")
	}
}

func hasFilter(t *testing.T, st *store.Store, showID int64, kind, op, value string) bool {
	t.Helper()
	filters, err := st.Filters(showID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range filters {
		if f.Kind == kind && f.Op == op && f.Value == value {
			return true
		}
	}
	return false
}
