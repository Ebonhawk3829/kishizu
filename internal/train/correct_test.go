package train

import "testing"

// TestCorrectedGroupDrivesOffset: when the user corrects the group, the offset
// must land on their group, not the parser's.
//
// VARYG puts its name at the end after a hyphen, so the parser reads no group
// at all. Without this the offset is recorded under "(none)" and VARYG
// releases never resolve.
func TestCorrectedGroupDrivesOffset(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War", []string{
		"BLEACH Thousand Year Blood War",
	}, 30)
	s, _ := NewSession(st, sh, 7)

	// A group the parser cannot see at all: no brackets, no trailing hyphen.
	title := "BLEACH Thousand Year Blood War S01E47 1080p NF WEB-DL AAC2.0 H.264 (Bleach: Sennen Kessen-hen)"
	g := Inspect(title, 0)

	if got := attrValue(g, AttrGroup); got != "" {
		t.Fatalf("expected the parser to miss the group, got %q", got)
	}
	setAttrValue(&g, AttrGroup, "VARYG")
	setAttrValue(&g, AttrEpisode, "7")

	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrEpisode: GradeGood}, 7); err != nil {
		t.Fatal(err)
	}
	if got := s.m.Offsets["VARYG"]; got != 40 {
		t.Errorf("VARYG offset = %d, want 40", got)
	}
	if _, ok := s.m.Offsets["(none)"]; ok {
		t.Errorf("offset recorded under (none): %v", s.m.Offsets)
	}
}

// TestAllAttributesEditable: every attribute can be corrected, since the parser
// will always meet titles it reads wrongly.
func TestAllAttributesEditable(t *testing.T) {
	g := Inspect("[Group] Show - 05 [1080p]", 5)
	for _, a := range g.Attrs {
		if !a.Editable {
			t.Errorf("attribute %s is not editable", a.Key)
		}
	}
}

// TestCorrectedResolutionUsed: a corrected resolution must drive the floor,
// not the parsed one.
func TestCorrectedResolutionUsed(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	s, _ := NewSession(st, sh, 5)

	g := Inspect("[Group] Show - 05 [weird-res-tag]", 5)
	setAttrValue(&g, AttrResolution, "1080p")

	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrResolution: GradeAcceptable}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if !hasFilter(t, st, sh.ID, "resolution", "min", "1080p") {
		t.Error("corrected resolution did not set the floor")
	}
}
