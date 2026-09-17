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

// TestResolutionPreferenceDiffersFromQuality: 2160p is higher quality than
// 1080p but not preferred, so it carries a penalty while still passing a 1080p
// floor. Conflating the two is what made a floor reject acceptable releases.
func TestResolutionPreferenceDiffersFromQuality(t *testing.T) {
	if ResRank("2160p") <= ResRank("1080p") {
		t.Error("2160p must be higher quality than 1080p")
	}
	if ResolutionPenalty["2160p"] <= ResolutionPenalty["1080p"] {
		t.Error("2160p must be penalised relative to 1080p (acceptable, not preferred)")
	}
	if ResolutionRejected("2160p") {
		t.Error("2160p must not be rejected; it is acceptable")
	}
	if !ResolutionRejected("720p") {
		t.Error("720p must be rejected; it is below the floor")
	}
	if ResolutionRejected("") {
		t.Error("an unknown resolution must not be rejected")
	}
}

// TestCodecRankPrefersSourceEncode: the ranking runs from closest to the
// original encode downwards. x265 and AV1 are re-compressions of an existing
// x264 release, so they are worse, not better.
func TestCodecRankPrefersSourceEncode(t *testing.T) {
	cases := []struct{ codec string; want int }{
		{"x264", 0}, {"h.264", 0}, {"h264", 0}, {"avc", 0},
		{"x265", 20}, {"hevc", 20},
		{"av1", 30},
	}
	for _, c := range cases {
		got, ok := CodecRankOf(c.codec)
		if !ok {
			t.Errorf("codec %q unknown", c.codec)
			continue
		}
		if got != c.want {
			t.Errorf("CodecRankOf(%q) = %d, want %d", c.codec, got, c.want)
		}
	}
	if _, ok := CodecRankOf(""); ok {
		t.Error("an empty codec must report unknown, not rank 0")
	}
}

// TestIsDubIgnoresDualAudio: dual-audio carries both tracks and is fine; only
// a dub-only release replaces the original performance.
func TestIsDubIgnoresDualAudio(t *testing.T) {
	cases := map[string]bool{
		"[Group] Show - 01 [1080p] (Dual Audio)": false, // dual: not a dub
		"[Group] Show - 01 [1080p] [MultiSub]":   false, // multi: not a dub
		"[Group] Show - 01 [1080p DUB]":         true,  // dub
		"[Group] Show - 01 [1080p]":             false, // neither
	}
	for title, want := range cases {
		if got := IsDub(title); got != want {
			t.Errorf("IsDub(%q) = %v, want %v", title, got, want)
		}
	}
}

// TestRuleRejectOnlyHardCases: only a batch and a below-floor resolution are
// hard rejects. Everything else is a demotion, so a watchable release is never
// excluded outright.
func TestRuleRejectOnlyHardCases(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"[Group] Show - 01 [1080p x264]", false},
		{"[Group] Show - 01 [1080p x265]", false}, // demoted, not rejected
		{"[Group] Show - 01 [1080p DUB]", false},  // demoted, not rejected
		{"[Group] Show - 01 [720p x264]", true},   // below floor
		{"[Group] Show - 01~12 [1080p]", true},    // batch (range)
		{"[Group] Show - 01 [Batch]", true},       // batch (explicit)
	}
	for _, c := range cases {
		r := Parse(c.title)
		got, _ := RuleReject(&r)
		if got != c.want {
			t.Errorf("RuleReject(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}
