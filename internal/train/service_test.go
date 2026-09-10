package train

import "testing"

// TestServiceAndAudioAreGradable: the two releases that differ only by service
// and audio must both be gradable, so the user can express a preference.
func TestServiceAndAudioAreGradable(t *testing.T) {
	g := Inspect("[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p NF WEB-DL AAC2.0 H.264", 7)

	keys := map[Attribute]bool{}
	for _, a := range g.Attrs {
		keys[a.Key] = true
	}
	for _, k := range []Attribute{AttrService, AttrAudio} {
		if !keys[k] {
			t.Errorf("attribute %s missing from inspect output", k)
		}
	}
	if got := attrValue(g, AttrService); got != "nf" {
		t.Errorf("service = %q, want nf", got)
	}
	if got := attrValue(g, AttrAudio); got != "aac" {
		t.Errorf("audio = %q, want aac", got)
	}
}

// TestGradeServiceIsPreferenceNotFilter: a disliked service is demoted, never
// excluded. The same episode from another platform is still watchable.
func TestGradeServiceIsPreferenceNotFilter(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	s, _ := NewSession(st, sh, 5)

	g := Inspect("[Group] Show - 05 [1080p] NF WEB-DL", 5)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrService: GradeWrong}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	filters, _ := st.Filters(sh.ID)
	for _, f := range filters {
		if f.Kind == "service" {
			t.Errorf("service became a hard filter: %+v", f)
		}
	}
	prefs, _ := st.Preferences(sh.ID)
	var found bool
	for _, p := range prefs {
		if p.Kind == "service" && p.Value == "nf" && p.Rank > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("service not demoted: %+v", prefs)
	}
}

// TestGradeAudioIsPreferenceNotFilter: same reasoning as service. DDP5.1 may not
// play on the user's setup, but that ranks the release lower rather than
// refusing it.
func TestGradeAudioIsPreferenceNotFilter(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	s, _ := NewSession(st, sh, 5)

	g := Inspect("[Group] Show - 05 [1080p] DDP5.1", 5)
	if _, err := s.ApplyGrades(g, map[Attribute]Grade{AttrAudio: GradeWrong}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}

	filters, _ := st.Filters(sh.ID)
	for _, f := range filters {
		if f.Kind == "audio" {
			t.Errorf("audio became a hard filter: %+v", f)
		}
	}
	prefs, _ := st.Preferences(sh.ID)
	var found bool
	for _, p := range prefs {
		if p.Kind == "audio" && p.Value == "ddp" && p.Rank > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("audio not demoted: %+v", prefs)
	}
}

// TestServicePreferenceDistinguishesTwoReleases: end-to-end, the user can prefer
// one of two otherwise identical releases and the model records the difference.
func TestServicePreferenceDistinguishesTwoReleases(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War", []string{
		"BLEACH Thousand Year Blood War",
	}, 30)

	nf := "[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p NF WEB-DL AAC2.0 H.264"
	amzn := "[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p AMZN WEB-DL DDP2.0 H.264"

	// Prefer the Netflix one, demote the Amazon one.
	s1, _ := NewSession(st, sh, 7)
	g1 := Inspect(nf, 7)
	if _, err := s1.ApplyGrades(g1, map[Attribute]Grade{AttrService: GradeGood, AttrAudio: GradeGood}, 7); err != nil {
		t.Fatal(err)
	}
	if err := s1.Commit(); err != nil {
		t.Fatal(err)
	}

	s2, _ := NewSession(st, sh, 7)
	g2 := Inspect(amzn, 7)
	if _, err := s2.ApplyGrades(g2, map[Attribute]Grade{AttrService: GradeWrong, AttrAudio: GradeWrong}, 7); err != nil {
		t.Fatal(err)
	}
	if err := s2.Commit(); err != nil {
		t.Fatal(err)
	}

	prefs, _ := st.Preferences(sh.ID)
	rank := map[string]int{}
	for _, p := range prefs {
		rank[p.Kind+"/"+p.Value] = p.Rank
	}
	if rank["service/nf"] != 0 {
		t.Errorf("nf rank = %d, want 0", rank["service/nf"])
	}
	if rank["service/amzn"] <= rank["service/nf"] {
		t.Errorf("amzn rank %d should be worse than nf rank %d", rank["service/amzn"], rank["service/nf"])
	}
	if rank["audio/aac"] != 0 {
		t.Errorf("aac rank = %d, want 0", rank["audio/aac"])
	}
	if rank["audio/ddp"] <= rank["audio/aac"] {
		t.Errorf("ddp rank %d should be worse than aac rank %d", rank["audio/ddp"], rank["audio/aac"])
	}
}
