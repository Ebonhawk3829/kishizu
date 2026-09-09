package release

import "testing"

// TestParseTrailingGroup: some uploaders put the group at the end after a
// hyphen instead of in brackets at the front. VARYG does this, and the group
// was silently lost, so no offset could ever be learned for it.
func TestParseTrailingGroup(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{
			"### BLEACH Thousand Year Blood War S01E47 1080p NF WEB-DL AAC2.0 H.264-VARYG (Bleach: Sennen Kessen-hen - Soukoku-tan, Multi-Subs)",
			"VARYG",
		},
		{"Some Show - 05 [1080p] - MyGroup", "MyGroup"},
		{"Show Name S01E01 1080p WEB-DL-GroupX", "GroupX"},
	}
	for _, c := range cases {
		if got := Parse(c.title).Group; got != c.want {
			t.Errorf("Parse(%q).Group = %q, want %q", c.title[:40], got, c.want)
		}
	}
}

// TestParseBracketGroupStillWins: a leading bracket group must not be
// overridden by a trailing token.
func TestParseBracketGroupStillWins(t *testing.T) {
	got := Parse("[SubsPlease] Show - 47 (1080p)").Group
	if got != "SubsPlease" {
		t.Errorf("Group = %q, want SubsPlease", got)
	}
}

// TestParseTrailingQualityIsNotGroup: a trailing quality tag must not be
// mistaken for a group name, or every release gets a nonsense group.
func TestParseTrailingQualityIsNotGroup(t *testing.T) {
	for _, s := range []string{
		"Some Show - 05 [1080p] - AAC",
		"Some Show - 05 [1080p] - x264",
		"Some Show - 05 [1080p] - 1080p",
		"Some Show - 05 [1080p] - Multi-Subs",
	} {
		if got := Parse(s).Group; got != "" {
			t.Errorf("Parse(%q).Group = %q, want empty (quality tag)", s, got)
		}
	}
}

// TestParseTrailingGroupKeepsOtherFields: adding trailing-group detection must
// not disturb the fields that already parsed correctly.
func TestParseTrailingGroupKeepsOtherFields(t *testing.T) {
	r := Parse("### BLEACH Thousand Year Blood War S01E47 1080p NF WEB-DL AAC2.0 H.264-VARYG (Bleach: Sennen Kessen-hen)")
	if r.RawEpisode() != 47 {
		t.Errorf("RawEpisode = %d, want 47", r.RawEpisode())
	}
	if r.Resolution != "1080p" {
		t.Errorf("Resolution = %q, want 1080p", r.Resolution)
	}
	if r.Codec != "h.264" {
		t.Errorf("Codec = %q, want h.264", r.Codec)
	}
}
