package quality

import (
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/release"
)

// TestResolutionPreferenceDiffersFromQuality: 2160p is higher quality than
// 1080p but not preferred, so it carries a penalty while still passing a 1080p
// floor. Conflating the two is what made a floor reject acceptable releases.
func TestResolutionPreferenceDiffersFromQuality(t *testing.T) {
	p := Default()
	if release.ResRank("2160p") <= release.ResRank("1080p") {
		t.Error("2160p must be higher quality than 1080p")
	}
	if p.ResolutionPenalty["2160p"] <= p.ResolutionPenalty["1080p"] {
		t.Error("2160p must be penalised relative to 1080p (acceptable, not preferred)")
	}
	if p.ResolutionRejected("2160p") {
		t.Error("2160p must not be rejected; it is acceptable")
	}
	if !p.ResolutionRejected("720p") {
		t.Error("720p must be rejected; it is below the floor")
	}
	// An unreadable resolution is below the floor by default: after the
	// learned vocabulary has had its chance, a title that does not carry
	// its quality in a readable form does not get the benefit of the
	// doubt. The fix for a genuinely-good release is to teach the token.
	if !p.ResolutionRejected("") {
		t.Error("an unreadable resolution must be rejected on the grab path")
	}
}

// TestCodecRankPrefersSourceEncode: the ranking runs from closest to the
// original encode downwards. x265 and AV1 are re-compressions of an existing
// x264 release, so they are worse, not better.
func TestCodecRankPrefersSourceEncode(t *testing.T) {
	p := Default()
	cases := []struct {
		codec string
		want  int
	}{
		{"x264", 0}, {"h.264", 0}, {"h264", 0}, {"avc", 0},
		{"x265", 20}, {"hevc", 20},
		{"av1", 30},
	}
	for _, c := range cases {
		got, ok := p.CodecRankOf(c.codec)
		if !ok {
			t.Errorf("codec %q unknown", c.codec)
			continue
		}
		if got != c.want {
			t.Errorf("CodecRankOf(%q) = %d, want %d", c.codec, got, c.want)
		}
	}
	if _, ok := p.CodecRankOf(""); ok {
		t.Error("an empty codec must report unknown, not rank 0")
	}
}

// TestRejectOnlyHardCases: only a batch and a below-floor resolution are hard
// rejects. Everything else is a demotion, so a watchable release is never
// excluded outright.
func TestRejectOnlyHardCases(t *testing.T) {
	p := Default()
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
		r := release.Parse(c.title)
		got, _ := p.Reject(&r)
		if got != c.want {
			t.Errorf("Reject(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

// TestFloorIsConfigurable: the resolution floor is the whole point of moving
// the policy out of the release package. Someone on a slow link, or with a
// large existing library, needs to be able to say 720p without rebuilding.
func TestFloorIsConfigurable(t *testing.T) {
	p := Default()
	if !p.ResolutionRejected("720p") {
		t.Fatal("720p must be rejected at the default 1080p floor")
	}

	p.ResolutionFloor = "720p"
	if p.ResolutionRejected("720p") {
		t.Error("720p must pass once the floor is 720p")
	}
	if !p.ResolutionRejected("480p") {
		t.Error("480p must still be rejected at a 720p floor")
	}
	// The floor is a comparison, not a ladder: anything at or above it
	// passes, including resolutions the penalty map demotes.
	if p.ResolutionRejected("2160p") {
		t.Error("2160p must pass a 720p floor; it is above it")
	}
}

// TestGroupOrderIsConfigurable: the group order is a ranking, not an
// allowlist, so an unlisted group is still eligible — it just loses.
func TestGroupOrderIsConfigurable(t *testing.T) {
	p := Default()
	if got := p.GroupRank("VARYG"); got != 0 {
		t.Errorf("GroupRank(VARYG) = %d, want 0 (first)", got)
	}
	if got := p.GroupRank("Nobody"); got != RankUnlisted {
		t.Errorf("GroupRank(Nobody) = %d, want %d", got, RankUnlisted)
	}
	// Normalisation: the parser and the store must resolve groups
	// identically or an offset learned in one would not apply in the other.
	if got := p.GroupRank("Erai_Raws"); got != p.GroupRank("Erai-Raws") {
		t.Error("group normalisation must be applied to the configured order")
	}

	p.GroupOrder = []string{"SubsPlease", "VARYG"}
	if p.GroupRank("SubsPlease") >= p.GroupRank("VARYG") {
		t.Error("reordering the policy must reorder the ranking")
	}
}

// TestRankDemotesRatherThanRejects: a dub and an x265 release are still
// watchable, so they lose to a better release rather than being excluded.
func TestRankDemotesRatherThanRejects(t *testing.T) {
	p := Default()
	good := release.Parse("[Group] Show - 01 [1080p x264]")
	dub := release.Parse("[Group] Show - 01 [1080p x264 DUB]")
	x265 := release.Parse("[Group] Show - 01 [1080p x265]")

	if p.Rank(&dub) <= p.Rank(&good) {
		t.Error("a dub must rank worse than the same release without one")
	}
	if p.Rank(&x265) <= p.Rank(&good) {
		t.Error("x265 must rank worse than x264")
	}
	for name, r := range map[string]*release.Release{"good": &good, "dub": &dub, "x265": &x265} {
		if rejected, why := p.Reject(r); rejected {
			t.Errorf("%s must not be rejected, only demoted (%s)", name, why)
		}
	}
}

// TestUnrecognisedResolutionIsNotRejected: a resolution the parser cannot
// place against the floor must not be rejected for failing to name its
// quality in a spelling we happen to know. Rejecting it would exclude a
// perfectly good release over a vocabulary gap.
func TestUnrecognisedResolutionIsNotRejected(t *testing.T) {
	p := Default()
	if p.ResolutionRejected("1440p") {
		t.Error("1440p is known and above the floor; it must pass")
	}
	// A spelling we do not know is unknown quality, not low quality.
	if p.ResolutionRejected("4320p") {
		t.Error("an unrecognised resolution must not be rejected")
	}
	// But an EMPTY one still is: that is a title carrying no quality at
	// all, which is a different failure.
	if !p.ResolutionRejected("") {
		t.Error("an empty resolution must still be rejected")
	}
}
