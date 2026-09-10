package match

import "testing"

// TestConfidenceRisesWithKnownGroup: the same title is more confident once the
// group's offset is known. This is the core of "gets better with context".
func TestConfidenceRisesWithKnownGroup(t *testing.T) {
	title := "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL"

	unknown := &MemShow{Name: "Tomb Raider King", Max: 12, Offsets: map[string]int{}}
	known := &MemShow{Name: "Tomb Raider King", Max: 12, Offsets: map[string]int{"ToonsHub": 0}}

	a := Match(unknown, title)
	b := Match(known, title)

	if !a.Matched || !b.Matched {
		t.Fatalf("both should match: a=%+v b=%+v", a, b)
	}
	if b.Confidence <= a.Confidence {
		t.Errorf("confidence did not rise with a known group: unseen %.2f, known %.2f",
			a.Confidence, b.Confidence)
	}
}

// TestConfidenceRisesWithOffsetAgreement: an UNSEEN group is a safer guess when
// the known groups concur than when they contradict each other. This is what
// makes confidence accumulate rather than stay flat.
//
// Note that one group agreeing with itself is already agreement 1.0, so the
// comparison is agreement versus disagreement, not one group versus many.
func TestConfidenceRisesWithOffsetAgreement(t *testing.T) {
	title := "[BrandNewGroup] Tomb Raider King S01E09 1080p WEB-DL"

	// Three groups, all agreeing on offset 0.
	agree := &MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"ToonsHub": 0, "Erai-raws": 0, "SubsPlease": 0}}

	// Three groups, all disagreeing.
	disagree := &MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"ToonsHub": 0, "Erai-raws": 40, "SubsPlease": 7}}

	a := Match(disagree, title)
	b := Match(agree, title)

	if b.Confidence <= a.Confidence {
		t.Errorf("confidence did not rise with agreement: disagree %.2f, agree %.2f",
			a.Confidence, b.Confidence)
	}
}

// TestConfidenceFallsWhenOffsetsDisagree: when groups disagree about numbering,
// an unseen group is close to a coin flip and confidence must say so.
func TestConfidenceFallsWhenOffsetsDisagree(t *testing.T) {
	title := "[BrandNewGroup] Tomb Raider King S01E09 1080p WEB-DL"

	agree := &MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"A": 0, "B": 0, "C": 0}}
	disagree := &MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"A": 0, "B": 40, "C": 7}}

	a := Match(agree, title)
	b := Match(disagree, title)

	if b.Confidence >= a.Confidence {
		t.Errorf("confidence did not fall when offsets disagree: %.2f -> %.2f",
			a.Confidence, b.Confidence)
	}
}

// TestConfidentThreshold: a known group with a strong alias match is confident
// enough to act on; an unseen group with no agreement is not.
func TestConfidentThreshold(t *testing.T) {
	known := Match(&MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"ToonsHub": 0}},
		"[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL")
	if !known.Confident() {
		t.Errorf("known group should be confident, got %.2f", known.Confidence)
	}

	unseen := Match(&MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"A": 0, "B": 40, "C": 7}},
		"[BrandNewGroup] Tomb Raider King S01E09 1080p WEB-DL")
	if unseen.Confident() {
		t.Errorf("unseen group with disagreeing offsets should not be confident, got %.2f",
			unseen.Confidence)
	}
}

// TestConfidenceBounded: confidence must stay within 0..1 no matter the inputs.
func TestConfidenceBounded(t *testing.T) {
	cases := []struct {
		alias float64
		known bool
		agree float64
	}{
		{1, true, 1}, {0, false, 0}, {1, true, 0}, {0.6, false, 1},
	}
	for _, c := range cases {
		got := confidence(c.alias, c.known, c.agree)
		if got < 0 || got > 1 {
			t.Errorf("confidence(%v,%v,%v) = %.2f, out of range", c.alias, c.known, c.agree, got)
		}
	}
}

// TestOffsetAgreement: agreement counts GROUPS per offset, not distinct
// offsets. Three groups concurring is stronger evidence than one group alone,
// even though both yield a single distinct offset value.
func TestOffsetAgreement(t *testing.T) {
	cases := []struct {
		name    string
		offsets map[string]int
		want    float64
	}{
		{"no groups", map[string]int{}, 0},
		{"one group", map[string]int{"A": 5}, 1},
		{"three agree", map[string]int{"A": 0, "B": 0, "C": 0}, 1},
		{"two of three agree", map[string]int{"A": 0, "B": 0, "C": 40}, 2.0 / 3.0},
		{"all disagree", map[string]int{"A": 0, "B": 40, "C": 7}, 1.0 / 3.0},
	}
	for _, c := range cases {
		m := &MemShow{Name: "Show", Max: 12, Offsets: c.offsets}
		if got := offsetAgreement(m); got != c.want {
			t.Errorf("%s: offsetAgreement = %.3f, want %.3f", c.name, got, c.want)
		}
	}
}

// TestUnreadableEpisodeIsNotConfident: a title with no episode number cannot be
// confident, however well the alias matches.
func TestUnreadableEpisodeIsNotConfident(t *testing.T) {
	res := Match(&MemShow{Name: "Tomb Raider King", Max: 12,
		Offsets: map[string]int{"ToonsHub": 0}},
		"[ToonsHub] Tomb Raider King 1080p WEB-DL")
	if !res.Matched {
		t.Fatal("should match on alias")
	}
	if res.Episode != 0 {
		t.Errorf("Episode = %d, want 0", res.Episode)
	}
	if res.Confident() {
		t.Errorf("unreadable episode should not be confident, got %.2f", res.Confidence)
	}
}
