package release

import "testing"

func TestParseGroup(t *testing.T) {
	cases := map[string]string{
		"[SubsPlease] Show - 01 (1080p)":     "SubsPlease",
		"[Erai-raws] Show - 01 [1080p]":      "Erai-raws",
		"Show - 01 (1080p)":                  "",
		"[ToonsHub] Tomb Raider King S01E09": "ToonsHub",
	}
	for in, want := range cases {
		if got := Parse(in).Group; got != want {
			t.Errorf("Parse(%q).Group = %q, want %q", in, got, want)
		}
	}
}

func TestParseEpisode(t *testing.T) {
	cases := []struct {
		in   string
		ep   int
		bare int
	}{
		{"[SubsPlease] Show - 47 (1080p) [B657D64E].mkv", 0, 47},
		{"[ToonsHub] Show S01E47 1080p CR WEB-DL", 47, 0},
		{"[AnoZu] Bleach S17E41 1080p DSNP WEB-DL", 41, 0},
		{"[Erai-raws] Show - 07 [1080p]", 0, 7},
	}
	for _, tc := range cases {
		got := Parse(tc.in)
		if got.Episode != tc.ep {
			t.Errorf("Parse(%q).Episode = %d, want %d", tc.in, got.Episode, tc.ep)
		}
		if got.Bare != tc.bare {
			t.Errorf("Parse(%q).Bare = %d, want %d", tc.in, got.Bare, tc.bare)
		}
	}
}

// TestParseCodec is the regression test for a bug where "H.264" and "H.265"
// were not recognised: the original pattern used \b before "h", which fails
// because the following character is a dot, not a word character.
func TestParseCodec(t *testing.T) {
	cases := map[string]string{
		"[ToonsHub] Show S01E09 1080p CR WEB-DL AAC2.0 H.264": "h.264",
		"[ToonsHub] Show S01E09 1080p BILI WEB-DL H.265":      "h.265",
		"[Group] Show - 01 [1080p x265]":                      "x265",
		"[Group] Show - 01 [1080p AV1]":                       "av1",
		"[Group] Show - 01 [1080p HEVC]":                      "hevc",
		"[Group] Show - 01 [1080p]":                           "",
	}
	for in, want := range cases {
		if got := Parse(in).Codec; got != want {
			t.Errorf("Parse(%q).Codec = %q, want %q", in, got, want)
		}
	}
}

func TestParseResolution(t *testing.T) {
	cases := map[string]string{
		"Show - 01 (1080p)": "1080p",
		"Show - 01 (2160p)": "2160p",
		"Show - 01 (720p)":  "720p",
		"Show - 01 [4k]":    "4k",
		"Show - 01":         "",
	}
	for in, want := range cases {
		if got := Parse(in).Resolution; got != want {
			t.Errorf("Parse(%q).Resolution = %q, want %q", in, got, want)
		}
	}
}

func TestParseBatch(t *testing.T) {
	batches := []string{
		"[SubsPlease] Show (01-24) (1080p) [Batch]",
		"[Group] Show 01~12 [BD]",
		"[Group] Show - Complete Series",
	}
	for _, in := range batches {
		if !Parse(in).IsBatch {
			t.Errorf("Parse(%q).IsBatch = false, want true", in)
		}
	}
	notBatch := "[SubsPlease] Show - 47 (1080p)"
	if Parse(notBatch).IsBatch {
		t.Errorf("Parse(%q).IsBatch = true, want false", notBatch)
	}
}

func TestRawEpisodePrefersSxE(t *testing.T) {
	if got := Parse("[ToonsHub] Show S01E47 1080p").RawEpisode(); got != 47 {
		t.Errorf("RawEpisode = %d, want 47", got)
	}
	if got := Parse("[SubsPlease] Show - 47 (1080p)").RawEpisode(); got != 47 {
		t.Errorf("RawEpisode = %d, want 47", got)
	}
	if got := Parse("Show - Movie").RawEpisode(); got != 0 {
		t.Errorf("RawEpisode = %d, want 0", got)
	}
}

func TestNormalise(t *testing.T) {
	cases := map[string]string{
		"Bleach: Sennen Kessen Hen": "bleach sennen kessen hen",
		"Re:ZERO -Starting Life-":   "re zero starting life",
		"  Multiple   Spaces  ":     "multiple spaces",
		// Accented letters are letters, so they survive (lowercased). Stripping
		// them would break romaji titles that legitimately use macrons.
		"Café": "café",
	}
	for in, want := range cases {
		if got := Normalise(in); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokensDropsNoise(t *testing.T) {
	got := Tokens("Show the Season 2 Part 1")
	for _, noisy := range []string{"the", "season", "part"} {
		if got[noisy] {
			t.Errorf("token %q should have been dropped", noisy)
		}
	}
	if !got["show"] {
		t.Error("token \"show\" should be present")
	}
}

func TestTitleScore(t *testing.T) {
	aliases := []string{"Tomb Raider King", "Dogul Wang"}
	if got := TitleScore(aliases, "[ToonsHub] Tomb Raider King S01E09 1080p"); got < 1.0 {
		t.Errorf("exact alias should score 1.0, got %.2f", got)
	}
	if got := TitleScore(aliases, "[Group] Something Else - 01"); got > 0.5 {
		t.Errorf("unrelated title should score low, got %.2f", got)
	}
}
