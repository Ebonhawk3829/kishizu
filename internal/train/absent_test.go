package train

import (
	"strings"
	"testing"
)

// TestGradeAbsentWritesNothing: "the title does not say this" must not produce
// a rule. The value was inferred, not read, so there is nothing to prefer or
// exclude — and writing a rule about it would encode a hallucination.
func TestGradeAbsentWritesNothing(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", []string{"Dogul Wang"}, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL", 9)
	notes, err := s.ApplyGrades(g, map[Attribute]Grade{
		AttrResolution: GradeAbsent,
	}, 9)
	if err != nil {
		t.Fatalf("ApplyGrades: %v", err)
	}
	if len(s.pending) != 0 {
		t.Errorf("absent grade wrote %d rules: %+v", len(s.pending), s.pending)
	}
	if !strings.Contains(strings.Join(notes, " "), "not in title") {
		t.Errorf("notes = %v, want it to say the value is not in the title", notes)
	}
}

// TestGradeAbsentRetractsExistingRule: a value graded absent may already have
// rules from an earlier grade. Those were written on the same bad premise, so
// they must go too — otherwise "not in the title" leaves a live rule behind.
func TestGradeAbsentRetractsExistingRule(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", []string{"Dogul Wang"}, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL", 9)

	// First say 1080p is wanted, which writes a resolution floor.
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{
		AttrResolution: GradeGood,
	}, 9); err != nil {
		t.Fatalf("first ApplyGrades: %v", err)
	}
	if len(s.pending) == 0 {
		t.Fatal("setup: no rule written for the good grade")
	}

	// Then say the title never mentioned a resolution at all.
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{
		AttrResolution: GradeAbsent,
	}, 9); err != nil {
		t.Fatalf("second ApplyGrades: %v", err)
	}
	if len(s.pending) != 0 {
		t.Errorf("rule survived an absent grade: %+v", s.pending)
	}
}

// TestGradeAbsentDiffersFromWrong: "not in the title" and "in the title but
// unwanted" are different claims and must not collapse into one another.
// Wrong writes an exclusion; absent writes nothing.
func TestGradeAbsentDiffersFromWrong(t *testing.T) {
	title := "[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL"

	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", []string{"Dogul Wang"}, 12)

	wrongSess, _ := NewSession(st, sh, 9)
	wrongG := Inspect(title, 9)
	if _, err := wrongSess.ApplyGrades(wrongG, map[Attribute]Grade{
		AttrResolution: GradeWrong,
	}, 9); err != nil {
		t.Fatalf("wrong: %v", err)
	}

	absentSess, _ := NewSession(st, sh, 9)
	absentG := Inspect(title, 9)
	if _, err := absentSess.ApplyGrades(absentG, map[Attribute]Grade{
		AttrResolution: GradeAbsent,
	}, 9); err != nil {
		t.Fatalf("absent: %v", err)
	}

	if len(wrongSess.pending) == 0 {
		t.Error("GradeWrong wrote no rule; it should exclude the value")
	}
	if len(absentSess.pending) != 0 {
		t.Errorf("GradeAbsent wrote %d rules; it should write none", len(absentSess.pending))
	}
}

// TestParseGradeAbsent: the verdict is reachable from the wire format.
func TestParseGradeAbsent(t *testing.T) {
	for _, in := range []string{"absent", "none", "not present", "hallucinated", "invented"} {
		got, err := ParseGrade(in)
		if err != nil {
			t.Errorf("ParseGrade(%q): %v", in, err)
			continue
		}
		if got != GradeAbsent {
			t.Errorf("ParseGrade(%q) = %q, want %q", in, got, GradeAbsent)
		}
	}
}
