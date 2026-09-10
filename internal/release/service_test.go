package release

import "testing"

// TestServiceDistinguishesOtherwiseIdenticalReleases: two releases can match in
// group, resolution, codec, source and episode and differ only by streaming
// service and audio. Without these fields they are indistinguishable, so the
// user cannot express a preference between them.
func TestServiceDistinguishesOtherwiseIdenticalReleases(t *testing.T) {
	a := Parse("[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p NF WEB-DL AAC2.0 H.264 (Multi-Subs)")
	b := Parse("[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p AMZN WEB-DL DDP2.0 H.264 (Multi-Subs)")

	// Everything else really is identical, which is the point.
	if a.Group != b.Group || a.Resolution != b.Resolution ||
		a.Codec != b.Codec || a.Source != b.Source || a.RawEpisode() != b.RawEpisode() {
		t.Fatalf("titles differ outside service/audio: %+v vs %+v", a, b)
	}

	if a.Service != "nf" {
		t.Errorf("first Service = %q, want nf", a.Service)
	}
	if b.Service != "amzn" {
		t.Errorf("second Service = %q, want amzn", b.Service)
	}
	if a.Audio != "aac" {
		t.Errorf("first Audio = %q, want aac", a.Audio)
	}
	if b.Audio != "ddp" {
		t.Errorf("second Audio = %q, want ddp", b.Audio)
	}
}

// TestServiceSpellingsNormalise: the same platform is written many ways and must
// collapse to one token, or preferences learned from one spelling miss others.
func TestServiceSpellingsNormalise(t *testing.T) {
	cases := map[string]string{
		"[G] Show - 01 [1080p] NF WEB-DL":          "nf",
		"[G] Show - 01 [1080p] Netflix WEB-DL":     "nf",
		"[G] Show - 01 [1080p] AMZN WEB-DL":        "amzn",
		"[G] Show - 01 [1080p] Amazon WEB-DL":      "amzn",
		"[G] Show - 01 [1080p] CR WEB-DL":          "cr",
		"[G] Show - 01 [1080p] Crunchyroll WEB-DL": "cr",
		"[G] Show - 01 [1080p] DSNP WEB-DL":        "dsnp",
		"[G] Show - 01 [1080p] BILI WEB-DL":        "bili",
		"[G] Show - 01 [1080p] Bilibili WEB-DL":    "bili",
		"[G] Show - 01 [1080p] iQIYI WEB-DL":       "iqiyi",
	}
	for title, want := range cases {
		if got := Parse(title).Service; got != want {
			t.Errorf("Parse(%q).Service = %q, want %q", title, got, want)
		}
	}
}

// TestAudioSpellingsNormalise: channel counts and spellings vary; the codec is
// what matters.
func TestAudioSpellingsNormalise(t *testing.T) {
	cases := map[string]string{
		"[G] Show - 01 [1080p] AAC2.0": "aac",
		"[G] Show - 01 [1080p] AAC":    "aac",
		"[G] Show - 01 [1080p] DDP2.0": "ddp",
		"[G] Show - 01 [1080p] DDP5.1": "ddp",
		"[G] Show - 01 [1080p] DD+5.1": "ddp",
		"[G] Show - 01 [1080p] EAC3":   "ddp",
		"[G] Show - 01 [1080p] E-AC-3": "ddp",
		"[G] Show - 01 [1080p] FLAC":   "flac",
		"[G] Show - 01 [1080p] Opus":   "opus",
	}
	for title, want := range cases {
		if got := Parse(title).Audio; got != want {
			t.Errorf("Parse(%q).Audio = %q, want %q", title, got, want)
		}
	}
}

// TestServiceAbsentWhenNotPresent: a title with no service tag must not invent
// one. Short tokens like CR and NF would otherwise match inside ordinary words.
func TestServiceAbsentWhenNotPresent(t *testing.T) {
	for _, s := range []string{
		"[SubsPlease] Show - 47 (1080p) [F4B3C2D1]",
		"[Erai-raws] Frieren - 21 [1080p][Multiple Subtitle]",
		"[Group] Some Show - 01 [1080p] WEB-DL",
	} {
		if got := Parse(s).Service; got != "" {
			t.Errorf("Parse(%q).Service = %q, want empty", s, got)
		}
	}
}

// TestServiceDoesNotBreakExistingFields: adding these fields must not disturb
// what already parsed correctly.
func TestServiceDoesNotBreakExistingFields(t *testing.T) {
	r := Parse("[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p NF WEB-DL AAC2.0 H.264 (Multi-Subs)")
	if r.Group != "ToonsHub" {
		t.Errorf("Group = %q, want ToonsHub", r.Group)
	}
	if r.RawEpisode() != 47 {
		t.Errorf("RawEpisode = %d, want 47", r.RawEpisode())
	}
	if r.Resolution != "1080p" {
		t.Errorf("Resolution = %q, want 1080p", r.Resolution)
	}
	if r.Codec != "h.264" {
		t.Errorf("Codec = %q, want h.264", r.Codec)
	}
	if r.Source != "webdl" {
		t.Errorf("Source = %q, want webdl", r.Source)
	}
}
