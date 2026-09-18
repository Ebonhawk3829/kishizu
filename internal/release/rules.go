package release

import (
	"regexp"
	"strings"
)

// Global rules: the preferences that hold for every show, so they never need
// grading per release.
//
// These exist because the per-release grading surface was doing work that is
// actually constant. Nobody wants a batch. Nobody prefers a dub. Nobody wants a
// re-encoded x265 over the original x264. Asking those questions once per
// release was wasted effort, and worse, it let a per-release answer contradict
// a preference that was never really in doubt.
//
// Training now teaches only two things: the offset for a group, and the
// vocabulary a group writes in. Everything here is settled in advance.

// ResolutionPenalty orders resolutions by how much they cost a release's rank.
// Lower is better, so this is a penalty rather than a quality score.
//
// Distinct from ResolutionRank in shared.go, which orders resolutions by
// ascending quality so the trainer and listener can compare against a floor.
// Same information, opposite direction, and both are needed: the floor is a
// comparison, this is a ranking.
//
// 1080p and 1440p are equally preferred: 1440p is not meaningfully better for
// anime, and treating it as a strict upgrade would churn. 2160p is acceptable
// but not preferred — a large download for little gain at this source quality.
// Anything below 1080p is rejected outright rather than merely penalised.
var ResolutionPenalty = map[string]int{
	"1080p": 0,
	"1440p": 0,
	"2160p": 10,
	"4k":    10,
	"720p":  90,
	"480p":  95,
}

// ResolutionRejected reports whether a resolution is below the floor.
//
// A resolution the parser could not read is ALSO rejected: after the learned
// vocabulary has had its chance to fill the gap, an empty resolution is a
// title that does not carry its quality in a readable form, and a release
// that cannot be quality-checked does not get the benefit of the doubt.
// Callers that merely DISPLAY a parse (the training panel) should use the
// parse directly instead, so the user can teach the missing token.
func ResolutionRejected(res string) bool {
	if res == "" {
		return true
	}
	rank, ok := ResolutionPenalty[strings.ToLower(res)]
	if !ok {
		return false
	}
	return rank >= 90
}

// CodecRank orders codecs by how close they sit to the original encode.
//
// The principle is generational loss, not the literal strings. Anime releases
// in x265 or AV1 are almost always re-encodes of an existing x264 WEB-DL — a
// lossy re-compression of something already compressed. The smaller file is
// worse, not better. So the ranking runs from closest-to-source downwards, and
// a future codec slots in by the same rule rather than needing a new case.
//
// An unrecognised codec is neutral: not punished for being unknown, just not
// rewarded.
var CodecRank = map[string]int{
	"x264":  0,
	"h.264": 0,
	"h264":  0,
	"avc":   0,
	"x265":  20,
	"h.265": 20,
	"h265":  20,
	"hevc":  20,
	"av1":   30,
}

// CodecRankOf returns the rank for a codec, and whether it is known.
func CodecRankOf(codec string) (int, bool) {
	if codec == "" {
		return 0, false
	}
	rank, ok := CodecRank[strings.ToLower(codec)]
	return rank, ok
}

// Penalties applied to a release's rank. Lower total wins.
//
// These are deliberately not hard filters (except batch). A dub or an x265
// release is still watchable, so it is demoted rather than excluded — it should
// lose to a better release, but still win if nothing better exists.
const (
	PenaltyDub        = 15 // dub present: demote, never exclude
	PenaltyUncensored = -5 // uncensored present: prefer
)

// IsDub reports whether a title indicates a dubbed release.
//
// Distinct from dual-audio, which carries both tracks and is fine. A dub-only
// release replaces the original vocal performance, which is what makes it
// unwanted.
func IsDub(title string) bool {
	t := strings.ToLower(title)
	if strings.Contains(t, "dual") || strings.Contains(t, "multi") {
		return false // both tracks present; not a dub-only release
	}
	return reDubWord.MatchString(t)
}

var reDubWord = regexp.MustCompile(`(?i)(?:^|[^a-z])(dub|dubbed)(?:[^a-z]|$)`)

// RuleRank scores a release against the global rules. Lower is better.
//
// This is the primary ordering input. Group preference is applied separately
// and takes precedence, because which group posted a release says more about
// its quality than any of these attributes do.
func RuleRank(r *Release) int {
	sum := 0

	if rank, ok := CodecRankOf(r.Codec); ok {
		sum += rank
	}
	if rank, ok := ResolutionPenalty[strings.ToLower(r.Resolution)]; ok {
		sum += rank
	}
	if IsDub(r.Title) {
		sum += PenaltyDub
	}
	if r.IsUncensored {
		sum += PenaltyUncensored
	}
	return sum
}

// RuleReject reports whether a release is excluded by a global rule, and why.
//
// Only three things are hard rejects: a batch (out of scope entirely), a
// resolution below the floor, and a resolution the parser could not read.
// Everything else is a demotion.
func RuleReject(r *Release) (bool, string) {
	if r.IsBatch {
		return true, "batch"
	}
	if ResolutionRejected(r.Resolution) {
		if r.Resolution == "" {
			return true, "no readable resolution"
		}
		return true, "resolution " + r.Resolution + " below floor"
	}
	return false, ""
}

// DefaultGroupOrder is the release-group preference, best first. It is global
// and set in advance: which group posted a release says more about its
// quality than any attribute of the file does. Unlisted groups are still
// eligible — this is a ranking, not an allowlist.
//
// Overridable at startup by the caller (e.g. the -prefer flag); never written
// by training.
var DefaultGroupOrder = []string{"VARYG", "Erai-Raws", "SubsPlease", "ToonsHub"}

// GroupRank returns where a release's group sits in DefaultGroupOrder.
// Unlisted groups sort last (RankUnlisted) but are not excluded.
const RankUnlisted = 1000

func GroupRank(group string) int {
	want := NormaliseGroup(group)
	if want != "" {
		for i, g := range DefaultGroupOrder {
			if NormaliseGroup(g) == want {
				return i
			}
		}
	}
	return RankUnlisted
}
