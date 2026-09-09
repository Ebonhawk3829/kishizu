package train

import (
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestInspectBreaksIntoAttributes: one release yields several independently
// gradable attributes, which is the point of grading over accept/reject.
func TestInspectBreaksIntoAttributes(t *testing.T) {
	g := Inspect("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL AAC2.0 H.264", 9)

	want := map[Attribute]string{
		AttrGroup:      "ToonsHub",
		AttrResolution: "1080p",
		AttrCodec:      "h.264",
		AttrSource:     "webdl",
	}
	for k, v := range want {
		got := attrValue(g, k)
		if got != v {
			t.Errorf("attr %s = %q, want %q", k, got, v)
		}
	}
	if len(g.Attrs) < 6 {
		t.Errorf("only %d attributes, want at least 6", len(g.Attrs))
	}
}

// TestGradeResolutionSetsFloor: an acceptable resolution sets a FLOOR, not a
// ladder. Resolution is "at least this good", per the design.
func TestGradeResolutionSetsFloor(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeAcceptable}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	filters, err := st.Filters(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range filters {
		if f.Kind == "resolution" && f.Op == "min" && f.Value == "1080p" {
			found = true
		}
	}
	if !found {
		t.Errorf("no resolution floor written, got %+v", filters)
	}
}

// TestGradeCodecIsPreferenceNotFilter: a disliked codec is demoted, never
// excluded. A wrong codec is still watchable, so it must not block a download.
func TestGradeCodecIsPreferenceNotFilter(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[ToonsHub] Tomb Raider King S01E09 1080p HEVC", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrCodec: GradeWrong}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	filters, _ := st.Filters(sh.ID)
	for _, f := range filters {
		if f.Kind == "codec" {
			t.Errorf("codec became a hard filter: %+v", f)
		}
	}
	prefs, _ := st.Preferences(sh.ID)
	var found bool
	for _, p := range prefs {
		if p.Kind == "codec" && p.Value == "hevc" && p.Rank > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("codec not demoted, prefs = %+v", prefs)
	}
}

// TestGradeEpisodeGoodTeachesOffset: grading the episode "good" is what teaches
// the per-group offset.
func TestGradeEpisodeGoodTeachesOffset(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War", []string{"BLEACH Thousand Year Blood War"}, 30)
	s, _ := NewSession(st, sh, 7)

	// Raw 47 is episode 7, so the offset is 40. The episode attribute is
	// editable and defaults to the raw number, so the user's correction to 7 is
	// what must drive the offset.
	g := Inspect("[SubsPlease] BLEACH: Sennen Kessen-hen - 47 (1080p)", 0)
	if got := attrValue(g, AttrEpisode); got != "47" {
		t.Fatalf("episode value = %q, want \"47\" (raw, before correction)", got)
	}
	setAttrValue(&g, AttrEpisode, "7")

	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrEpisode: GradeGood}, 7); err != nil {
		t.Fatal(err)
	}
	if got := s.m.Offsets["SubsPlease"]; got != 40 {
		t.Errorf("SubsPlease offset = %d, want 40", got)
	}
}

// TestEpisodeHintDistinguishesRawFromResolved: the hint must say what the title
// literally says, so "47" is never mistaken for the user's own episode number.
func TestEpisodeHintDistinguishesRawFromResolved(t *testing.T) {
	g := Inspect("[SubsPlease] BLEACH: Sennen Kessen-hen - 47 (1080p)", 0)
	var a AttrValue
	for _, x := range g.Attrs {
		if x.Key == AttrEpisode {
			a = x
		}
	}
	if !a.Editable {
		t.Error("episode attribute must be editable")
	}
	if !strings.Contains(a.Hint, "47") {
		t.Errorf("hint = %q, want it to mention the raw number 47", a.Hint)
	}
	if !strings.Contains(a.Hint, "really is") {
		t.Errorf("hint = %q, want it to prompt for the real episode", a.Hint)
	}
}

// TestEpisodeHintWhenOffsetApplied: once an offset is known the hint says so,
// rather than repeating the raw number as if it were unresolved.
func TestEpisodeHintWhenOffsetApplied(t *testing.T) {
	g := Inspect("[SubsPlease] BLEACH: Sennen Kessen-hen - 47 (1080p)", 7)
	var a AttrValue
	for _, x := range g.Attrs {
		if x.Key == AttrEpisode {
			a = x
		}
	}
	if a.Value != "7" {
		t.Errorf("value = %q, want \"7\" (the user's episode)", a.Value)
	}
	if !strings.Contains(a.Hint, "47") || !strings.Contains(a.Hint, "offset") {
		t.Errorf("hint = %q, want it to show raw 47 and note the offset", a.Hint)
	}
}

// TestGradeWrongEpisodeDoesNotTeachOffset: "wrong" on the episode must NOT
// derive an offset. The number is not this episode, so there is nothing to
// learn from it — the user should supply a correct example instead.
func TestGradeWrongEpisodeDoesNotTeachOffset(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[ToonsHub] Tomb Raider King S01E05 1080p", 5)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrEpisode: GradeWrong}, 9); err != nil {
		t.Fatal(err)
	}
	if len(s.m.Offsets) != 0 {
		t.Errorf("offsets learned from a wrong episode: %v", s.m.Offsets)
	}
}

// TestGradesDiscardedOnCancel: filters and preferences are deferred to Commit,
// so abandoning a session leaves no trace. Previously rejections wrote straight
// to the database while offsets waited, so cancelling kept half the learning.
func TestGradesDiscardedOnCancel(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[ToonsHub] Tomb Raider King S01E09 1080p HEVC", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{
		AttrCodec:      GradeWrong,
		AttrResolution: GradeGood,
	}, 9); err != nil {
		t.Fatal(err)
	}
	// No Commit: session abandoned.

	if f, _ := st.Filters(sh.ID); len(f) != 0 {
		t.Errorf("filters written without commit: %+v", f)
	}
	if p, _ := st.Preferences(sh.ID); len(p) != 0 {
		t.Errorf("preferences written without commit: %+v", p)
	}
}

// TestGradeGroupExclude: disliking a group excludes it outright.
func TestGradeGroupExclude(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[BadGroup] Tomb Raider King S01E09 1080p", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrGroup: GradeWrong}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	filters, _ := st.Filters(sh.ID)
	var found bool
	for _, f := range filters {
		if f.Kind == "group" && f.Op == "exclude" && f.Value == "BadGroup" {
			found = true
		}
	}
	if !found {
		t.Errorf("group not excluded, filters = %+v", filters)
	}
}

// TestParseGrade tolerates the obvious spellings.
func TestParseGrade(t *testing.T) {
	cases := map[string]Grade{
		"good": GradeGood, "g": GradeGood, "yes": GradeGood,
		"acceptable": GradeAcceptable, "ok": GradeAcceptable, "meh": GradeAcceptable,
		"wrong": GradeWrong, "w": GradeWrong, "no": GradeWrong,
		"": GradeUnknown, "?": GradeUnknown, "skip": GradeUnknown,
	}
	for in, want := range cases {
		got, err := ParseGrade(in)
		if err != nil {
			t.Errorf("ParseGrade(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseGrade(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseGrade("banana"); err == nil {
		t.Error("expected an error for an unknown grade")
	}
}

// TestGradesAreIndependent: grading one attribute must not touch another.
// A release can be the right episode at a bad resolution from a good group.
func TestGradesAreIndependent(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[GoodGroup] Tomb Raider King S01E09 480p", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{
		AttrResolution: GradeWrong, // 480p is too low
		AttrGroup:      GradeGood,  // but the group is fine
	}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	filters, _ := st.Filters(sh.ID)
	for _, f := range filters {
		if f.Kind == "group" {
			t.Errorf("group was excluded despite a good grade: %+v", f)
		}
	}
	prefs, _ := st.Preferences(sh.ID)
	var groupPref bool
	for _, p := range prefs {
		if p.Kind == "group" && p.Value == "GoodGroup" {
			groupPref = true
		}
	}
	if !groupPref {
		t.Errorf("good group not preferred, prefs = %+v", prefs)
	}
}

func attrValue(g GradedRelease, k Attribute) string {
	for _, a := range g.Attrs {
		if a.Key == k {
			return a.Value
		}
	}
	return ""
}

// setAttrValue simulates the user correcting an editable attribute in the UI.
func setAttrValue(g *GradedRelease, k Attribute, v string) {
	for i := range g.Attrs {
		if g.Attrs[i].Key == k {
			g.Attrs[i].Value = v
			return
		}
	}
}

// TestFlushPendingDeduplicates: grading the same attribute twice keeps the
// last verdict rather than writing two conflicting rows.
func TestFlushPendingDeduplicates(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	g := Inspect("[ToonsHub] Tomb Raider King S01E09 1080p HEVC", 9)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrCodec: GradeWrong}, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrCodec: GradeGood}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	prefs, _ := st.Preferences(sh.ID)
	n := 0
	for _, p := range prefs {
		if p.Kind == "codec" && p.Value == "hevc" {
			n++
			if p.Rank != 0 {
				t.Errorf("last grade did not win: rank = %d, want 0", p.Rank)
			}
		}
	}
	if n != 1 {
		t.Errorf("got %d codec rows for hevc, want 1: %+v", n, prefs)
	}
}

// TestStoreFilterRoundTrip guards the store helpers the grader depends on.
func TestStoreFilterRoundTrip(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)

	if err := st.AddFilter(sh.ID, store.Filter{Kind: "resolution", Op: "min", Value: "1080p"}); err != nil {
		t.Fatal(err)
	}
	filters, err := st.Filters(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 1 || !strings.Contains(filters[0].Value, "1080p") {
		t.Errorf("filters = %+v", filters)
	}
}
