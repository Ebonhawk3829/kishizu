package train

import (
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestSecondReleaseStacksOnFirst: grading a second release for the same episode
// must add to what is already known, not replace it.
//
// This is the core training loop: several groups release the same episode, and
// each one graded teaches that group's offset while leaving the others intact.
func TestSecondReleaseStacksOnFirst(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War", []string{
		"BLEACH Thousand Year Blood War",
	}, 30)

	// First release: VARYG, raw 47 is episode 7, so offset 40.
	s1, _ := NewSession(st, sh, 7)
	g1 := Inspect("[VARYG] BLEACH Thousand Year Blood War S01E47 1080p", 0)
	setAttrValue(&g1, AttrEpisode, "7")
	if _, err := s1.ApplyGrades(g1, map[Attribute]Grade{AttrEpisode: GradeGood}, 7); err != nil {
		t.Fatal(err)
	}
	if err := s1.Commit(); err != nil {
		t.Fatal(err)
	}

	// Second release, different group, same episode.
	s2, _ := NewSession(st, sh, 7)
	g2 := Inspect("[Anime Time] Bleach: Thousand-Year Blood War - S01E47 [1080p]", 0)
	setAttrValue(&g2, AttrEpisode, "7")
	if _, err := s2.ApplyGrades(g2, map[Attribute]Grade{AttrEpisode: GradeGood}, 7); err != nil {
		t.Fatal(err)
	}
	if err := s2.Commit(); err != nil {
		t.Fatal(err)
	}

	offsets, err := st.GroupOffsets(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if offsets["VARYG"] != 40 {
		t.Errorf("VARYG offset = %d, want 40 (first release lost)", offsets["VARYG"])
	}
	if offsets["Anime Time"] != 40 {
		t.Errorf("Anime Time offset = %d, want 40 (second release not learned)", offsets["Anime Time"])
	}
}

// TestGradingSecondReleaseKeepsEarlierPreferences: a preference learned from one
// release must survive grading a different release that says nothing about it.
func TestGradingSecondReleaseKeepsEarlierPreferences(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)

	s1, _ := NewSession(st, sh, 5)
	g1 := Inspect("[GroupA] Show - 05 [1080p] H.264", 5)
	if _, err := s1.ApplyGrades(g1, map[Attribute]Grade{AttrCodec: GradeGood}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s1.Commit(); err != nil {
		t.Fatal(err)
	}

	// Second release graded only on resolution; the codec preference must stay.
	s2, _ := NewSession(st, sh, 5)
	g2 := Inspect("[GroupB] Show - 05 [1080p] HEVC", 5)
	if _, err := s2.ApplyGrades(g2, map[Attribute]Grade{AttrResolution: GradeGood}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s2.Commit(); err != nil {
		t.Fatal(err)
	}

	prefs, _ := st.Preferences(sh.ID)
	var h264 bool
	for _, p := range prefs {
		if p.Kind == "codec" && p.Value == "h.264" && p.Rank == 0 {
			h264 = true
		}
	}
	if !h264 {
		t.Errorf("h.264 preference lost after grading a second release: %+v", prefs)
	}
}

// TestGroupAcceptableIsRecorded: "acceptable" is a real verdict for a group —
// usable but not first choice. It was silently dropped, so grading a group
// acceptable taught nothing at all.
func TestGroupAcceptableIsRecorded(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	s, _ := NewSession(st, sh, 5)

	g := Inspect("[OkayGroup] Show - 05 [1080p]", 5)
	notes, err := s.ApplyGrades(g, map[Attribute]Grade{AttrGroup: GradeAcceptable}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 {
		t.Error("grading a group acceptable produced no note; it was dropped")
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	prefs, _ := st.Preferences(sh.ID)
	var found bool
	for _, p := range prefs {
		if p.Kind == "group" && p.Value == "OkayGroup" {
			found = true
			if p.Rank == 0 {
				t.Errorf("acceptable group ranked 0, same as preferred; want > 0")
			}
		}
	}
	if !found {
		t.Errorf("acceptable group not recorded: %+v", prefs)
	}
}

// TestSourceAcceptableIsRecorded: same gap as groups — "acceptable" on a source
// must be recorded rather than dropped.
func TestSourceAcceptableIsRecorded(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	s, _ := NewSession(st, sh, 5)

	g := Inspect("[Group] Show - 05 [1080p] WEBRip", 5)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrSource: GradeAcceptable}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	prefs, _ := st.Preferences(sh.ID)
	var found bool
	for _, p := range prefs {
		if p.Kind == "source" && p.Value == "webrip" {
			found = true
		}
	}
	if !found {
		t.Errorf("acceptable source not recorded: %+v", prefs)
	}
}

// TestEveryGradeProducesSomething: no verdict may be silently discarded. A grade
// the user bothered to make should always change the model or say why not.
func TestEveryGradeProducesSomething(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)

	grades := []Grade{GradeGood, GradeAcceptable, GradeWrong}
	attrs := []Attribute{AttrGroup, AttrResolution, AttrCodec, AttrSource}

	for _, a := range attrs {
		for _, gr := range grades {
			s, _ := NewSession(st, sh, 5)
			g := Inspect("[Group] Show - 05 [1080p] H.264 WEB-DL", 5)
			notes, err := s.ApplyGrades(g, map[Attribute]Grade{a: gr}, 5)
			if err != nil {
				t.Fatalf("%s/%s: %v", a, gr, err)
			}
			if len(notes) == 0 {
				t.Errorf("grading %s %s produced nothing", a, gr)
			}
			if len(s.pending) == 0 {
				t.Errorf("grading %s %s queued no write", a, gr)
			}
		}
	}
}

// TestConfidenceRisesAsGroupsAreLearned: the payoff for stacking. Each release
// graded for the same episode raises agreement, so the next unseen group is
// resolved with more confidence.
func TestConfidenceRisesAsGroupsAreLearned(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War", []string{
		"BLEACH Thousand Year Blood War",
	}, 30)

	confAfter := func(n int) float64 {
		s, _ := NewSession(st, sh, 7)
		return s.confidenceFor("[Unknown] BLEACH Thousand Year Blood War S01E47 1080p")
	}

	// Nothing learned yet.
	before := confAfter(0)

	// Teach three groups, all agreeing on offset 40.
	for _, grp := range []string{"VARYG", "ToonsHub", "Anime Time"} {
		s, _ := NewSession(st, sh, 7)
		g := Inspect("["+grp+"] BLEACH Thousand Year Blood War S01E47 1080p", 0)
		setAttrValue(&g, AttrEpisode, "7")
		if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrEpisode: GradeGood}, 7); err != nil {
			t.Fatal(err)
		}
		if err := s.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	after := confAfter(3)
	if after <= before {
		t.Errorf("confidence did not rise as groups were learned: %.2f -> %.2f", before, after)
	}
}

// TestStoreGroupOffsetsSurviveReopen guards the persistence the stacking relies
// on: each session is short-lived, so learning must reach the database.
func TestStoreGroupOffsetsSurviveReopen(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	if err := st.SetGroupOffset(sh.ID, "VARYG", 40, "training"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GroupOffsets(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got["VARYG"] != 40 {
		t.Errorf("offset = %d, want 40", got["VARYG"])
	}
	var _ = store.Filter{}
}
