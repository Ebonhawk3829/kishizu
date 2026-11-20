package release

import "testing"

// TestResolutionQualityAscends: the floor comparison must order by quality, so
// a higher resolution always passes a lower floor.
//
// Regression: this was briefly derived by inverting the preference penalty,
// which made 720p rank above 1080p — because the penalty for a below-floor
// resolution is a demotion, not a quality measure. The two are different
// concepts and must stay separate maps.
func TestResolutionQualityAscends(t *testing.T) {
	order := []string{"480p", "720p", "1080p", "1440p", "2160p"}
	for i := 1; i < len(order); i++ {
		if ResRank(order[i-1]) >= ResRank(order[i]) {
			t.Errorf("ResRank(%s)=%d >= ResRank(%s)=%d; quality must ascend",
				order[i-1], ResRank(order[i-1]), order[i], ResRank(order[i]))
		}
	}
	if ResRank("bogus") != 0 {
		t.Errorf("unknown resolution = %d, want 0", ResRank("bogus"))
	}
}

// TestAudioKindClassifiesLanguageTracks: the policy only rejects English dubs.
// Original audio, dual-audio, non-English dubs and titles that say nothing are
// all acceptable, and "Multi-Subs" is about subtitles, not audio.
func TestAudioKindClassifiesLanguageTracks(t *testing.T) {
	cases := map[string]AudioKind{
		"[Group] Show - 01 [1080p] (Dual Audio)":                                          AudioDual,
		"[Group] Show - 01 [1080p] [Multi-Audio]":                                         AudioDual,
		"[ToonsHub] Show S01E09 1080p DUAL AAC2.0 H.264":                                  AudioDual,
		"[Group] Show - 01 [Dual Audio, Multi-Subs, Japanese Dub]":                        AudioDual,
		"[Group] Show - 01 [1080p] [MultiSub]":                                            AudioUnknown,
		"[Group] Show - 01 [1080p] [Multi-Subs]":                                          AudioUnknown,
		"[Group] Show - 01 [1080p DUB]":                                                   AudioEnglishDub,
		"[Group] Show - 01 [1080p Dubbed]":                                                AudioEnglishDub,
		"[Group] Show - 01 [English Dub]":                                                 AudioEnglishDub,
		"[Group] Show - 01 [ENG DUB]":                                                     AudioEnglishDub,
		"[Group] Show - 01 [EN-DUB]":                                                      AudioEnglishDub,
		"[Group] Show - 01 [English Audio]":                                               AudioEnglishDub,
		"[Group] Show - 01 [Multi-Subs, Dub]":                                             AudioEnglishDub,
		"[ToonsHub] Tomb Raider King S01E09 1080p (Dogul Wang, Multi-Subs, Japanese Dub)": AudioOtherDub,
		"[Group] Show - 01 [French Dub]":                                                  AudioOtherDub,
		"[Group] Show - 01 [Japanese Audio]":                                              AudioOriginal,
		"[Group] Show - 01 [Original Audio]":                                              AudioOriginal,
		"[ToonsHub] Tomb Raider King S01E09 1080p (Dogul Wang, Multi-Subs, Korean Audio)": AudioOriginal,
		"[Group] Show - 01 [1080p]":                                                       AudioUnknown,
		"[Group] Show - 01 [1080p] [RAW]":                                                 AudioUnknown,
	}
	for title, want := range cases {
		if got := Parse(title).AudioKind; got != want {
			t.Errorf("Parse(%q).AudioKind = %q, want %q", title, got, want)
		}
	}
}

// TestIsDubMeansEnglishDub: the compatibility helper keeps its old callers
// working but now follows the UX rule — only an English dub is a dub.
func TestIsDubMeansEnglishDub(t *testing.T) {
	cases := map[string]bool{
		"[Group] Show - 01 [1080p] (Dual Audio)":               false,
		"[Group] Show - 01 [1080p] [MultiSub]":                 false,
		"[Group] Show - 01 [1080p DUB]":                        true,
		"[Group] Show - 01 [1080p] (Dogul Wang, Japanese Dub)": false,
		"[Group] Show - 01 [1080p] (Dogul Wang, Korean Audio)": false,
		"[Group] Show - 01 [1080p]":                            false,
	}
	for title, want := range cases {
		if got := IsDub(title); got != want {
			t.Errorf("IsDub(%q) = %v, want %v", title, got, want)
		}
	}
}
