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

// TestParseChineseEpisodeNumber: Bilibili-sourced releases number episodes
// 第N话, and without reading that the whole release is ungrabbable — the
// reason in the log is "episode unreadable", which looks like a parser bug
// to nobody because the title looks fine to a human.
//
// This is a pattern in the parser rather than a vocabulary entry
// deliberately: 第N话 is a different token for every episode, so teaching
// the vocabulary one token teaches one episode. A pattern generalises
// across every episode, which is what a numbering convention needs.
func TestParseChineseEpisodeNumber(t *testing.T) {
	cases := []struct {
		in   string
		ep   int
		bare int
	}{
		{"[Doomdos] - Tomb Raider King - 第3话 - [1080p BILIBILI COM WEB-DL]", 3, 0},
		{"[Doomdos] - BLEACH: Thousand-Year Blood War - The Calamity - 第43话 - [1080p BILIBILI COM WEB-DL]", 43, 0},
		{"[Doomdos] - Grand Blue Dreaming3 - 第12话 - [1080p BILIBILI COM WEB-DL]", 12, 0},
		// The characters can also appear in ordinary text. When a
		// conventional episode marker is present too, it wins: a bare
		// "- 47" is stronger evidence than a 第 that may be part of a
		// sentence.
		{"[Group] 第1话 is how they write it - 47 [1080p]", 0, 47},
		// Conventional formats unaffected.
		{"[SubsPlease] Show - 47 (1080p)", 0, 47},
		{"[ToonsHub] Show S01E47 1080p", 47, 0},
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

// TestBareNumberAllowsVersionSuffix: BD packs name files "01v2". Without the
// optional version suffix in the pattern the number is not read at all, so a
// batch resolves to no episode and sits in staging untouched.
func TestBareNumberAllowsVersionSuffix(t *testing.T) {
	cases := []struct {
		title string
		want  int
	}{
		{"[sam] Show - 01v2 [BD 1080p FLAC]", 1},
		{"[sam] Show - 01 [BD 1080p FLAC]", 1},
		{"[sam] Show - 12v3 [BD].mkv", 12},
		{"[sam] Show - 01.mkv", 1},
	}
	for _, c := range cases {
		if got := Parse(c.title).RawEpisode(); got != c.want {
			t.Errorf("Parse(%q).RawEpisode() = %d, want %d", c.title, got, c.want)
		}
	}
}

// TestSanitise: names must be safe on both Linux and Windows, since Syncthing
// moves files between them.
func TestSanitise(t *testing.T) {
	cases := map[string]string{
		"Show: The Sequel": "Show The Sequel",
		"Show/Slash":       "ShowSlash",
		"Trailing. Dots..": "Trailing. Dots",
		"CON":              "_CON",
		"Com1":             "_Com1",
		"Normal Show":      "Normal Show",
	}
	for in, want := range cases {
		if got := Sanitise(in); got != want {
			t.Errorf("Sanitise(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSanitiseKeepsNamesUsable: sanitising must not mangle ordinary names, only
// the characters that break on Windows (Syncthing crosses to the user's PC).
func TestSanitiseKeepsNamesUsable(t *testing.T) {
	if got := Sanitise("Tomb Raider King"); got != "Tomb Raider King" {
		t.Errorf("Sanitise = %q, want it unchanged", got)
	}
	if got := Sanitise("BLEACH: Thousand-Year Blood War"); got != "BLEACH Thousand-Year Blood War" {
		t.Errorf("Sanitise = %q, want the colon stripped only", got)
	}
}
