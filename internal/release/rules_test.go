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

// TestIsDubIgnoresDualAudio: dual-audio carries both tracks and is fine; only
// a dub-only release replaces the original performance.
func TestIsDubIgnoresDualAudio(t *testing.T) {
	cases := map[string]bool{
		"[Group] Show - 01 [1080p] (Dual Audio)": false, // dual: not a dub
		"[Group] Show - 01 [1080p] [MultiSub]":   false, // multi: not a dub
		"[Group] Show - 01 [1080p DUB]":          true,  // dub
		"[Group] Show - 01 [1080p]":              false, // neither
	}
	for title, want := range cases {
		if got := IsDub(title); got != want {
			t.Errorf("IsDub(%q) = %v, want %v", title, got, want)
		}
	}
}
