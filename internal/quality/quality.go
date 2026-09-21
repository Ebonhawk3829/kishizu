// Package quality holds the release-quality policy: what kishizu will and will
// not grab, and how it orders the releases it could grab.
//
// It is separate from release because the two answer different questions.
// release parses a title into facts; quality decides what those facts are
// worth. Keeping them apart is what lets the policy be configured without
// touching the parser.
//
// The policy is global — it holds for every show — and is set in advance. It
// is never written by training: training calibrates the parser (per-group
// offsets and vocabulary), and a per-release answer that contradicted a
// preference nobody was unsure about would be a regression, not a refinement.
package quality

import (
	"fmt"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/release"
)

// Policy is the whole quality policy: the hard rejects and the ranking.
//
// A zero Policy is not usable; build one with Default and change what you
// need, so a field added later is never accidentally left at a zero value
// that means something surprising.
type Policy struct {
	// ResolutionFloor is the lowest acceptable resolution, by name
	// ("1080p"). A release below it is rejected outright rather than merely
	// demoted: a smaller file is not a cheaper version of the same thing, it
	// is a different thing.
	ResolutionFloor string

	// ResolutionPenalty orders resolutions by how much they cost a release's
	// rank. Lower is better, so this is a penalty rather than a quality
	// score.
	//
	// Distinct from release.ResQuality, which orders by ascending quality so
	// a floor can be compared. Same information, opposite direction, and both
	// are needed: the floor is a comparison, this is a ranking. 1080p and
	// 1440p are equally preferred — 1440p is not meaningfully better for
	// anime, and treating it as a strict upgrade would churn. 2160p is
	// acceptable but not preferred: a large download for little gain at this
	// source quality.
	ResolutionPenalty map[string]int

	// CodecRank orders codecs by how close they sit to the original encode.
	//
	// The principle is generational loss, not the literal strings. Anime
	// releases in x265 or AV1 are almost always re-encodes of an existing
	// x264 WEB-DL — a lossy re-compression of something already compressed.
	// The smaller file is worse, not better. So the ranking runs from
	// closest-to-source downwards, and a future codec slots in by the same
	// rule rather than needing a new case.
	//
	// An unrecognised codec is neutral: not punished for being unknown, just
	// not rewarded.
	CodecRank map[string]int

	// GroupOrder is the release-group preference, best first. Which group
	// posted a release says more about its quality than any attribute of the
	// file does, so this is applied before everything else.
	//
	// Unlisted groups are still eligible — this is a ranking, not an
	// allowlist.
	GroupOrder []string

	// Penalties applied to a release's rank. Lower total wins.
	//
	// These are deliberately not hard rejects. A dub or an x265 release is
	// still watchable, so it is demoted rather than excluded — it should lose
	// to a better release, but still win if nothing better exists.
	PenaltyDub        int // dub present: demote, never exclude
	PenaltyUncensored int // uncensored present: prefer, so negative

	// RejectBatch excludes batches and season packs outright. They are out of
	// scope: kishizu hunts one episode at a time while a season airs.
	RejectBatch bool
}

// Default is the policy kishizu ships with.
//
// These are the preferences that hold for every show, so they never need
// grading per release. Nobody wants a batch. Nobody prefers a dub. Nobody
// wants a re-encoded x265 over the original x264. Asking those questions once
// per release was wasted effort, and worse, it let a per-release answer
// contradict a preference that was never really in doubt.
func Default() *Policy {
	return &Policy{
		ResolutionFloor: "1080p",
		ResolutionPenalty: map[string]int{
			"1080p": 0,
			"1440p": 0,
			"2160p": 10,
			"4k":    10,
			"720p":  90,
			"480p":  95,
		},
		CodecRank: map[string]int{
			"x264":  0,
			"h.264": 0,
			"h264":  0,
			"avc":   0,
			"x265":  20,
			"h.265": 20,
			"h265":  20,
			"hevc":  20,
			"av1":   30,
		},
		GroupOrder:        []string{"VARYG", "Erai-Raws", "SubsPlease", "ToonsHub"},
		PenaltyDub:        15,
		PenaltyUncensored: -5,
		RejectBatch:       true,
	}
}

// RankUnlisted is where an unlisted release group sorts. High enough to lose
// to any listed group, but the group is still eligible.
const RankUnlisted = 1000

// GroupRank returns where a release's group sits in the group order.
// Unlisted groups sort last but are not excluded.
func (p *Policy) GroupRank(group string) int {
	want := release.NormaliseGroup(group)
	if want != "" {
		for i, g := range p.GroupOrder {
			if release.NormaliseGroup(g) == want {
				return i
			}
		}
	}
	return RankUnlisted
}

// CodecRankOf returns the rank for a codec, and whether it is known.
func (p *Policy) CodecRankOf(codec string) (int, bool) {
	if codec == "" {
		return 0, false
	}
	rank, ok := p.CodecRank[strings.ToLower(codec)]
	return rank, ok
}

// ResolutionRejected reports whether a resolution is below the floor.
//
// A resolution the parser could not read is ALSO rejected: after the learned
// vocabulary has had its chance to fill the gap, an empty resolution is a
// title that does not carry its quality in a readable form, and a release
// that cannot be quality-checked does not get the benefit of the doubt.
// Callers that merely DISPLAY a parse (the training panel) should use the
// parse directly instead, so the user can teach the missing token.
func (p *Policy) ResolutionRejected(res string) bool {
	if res == "" {
		return true
	}
	// An unrecognised resolution is not rejected. It cannot be placed
	// against the floor, and rejecting it would exclude a release for
	// failing to name its quality in a spelling we happen to know. The
	// penalty map is what demotes it, if at all.
	q := release.ResRank(res)
	if q == 0 {
		return false
	}
	return q < release.ResRank(p.ResolutionFloor)
}

// Reject reports whether a release is excluded by the policy, and why.
//
// Only a few things are hard rejects: a batch (out of scope entirely), a
// resolution below the floor, and a resolution the parser could not read.
// Everything else is a demotion, so a watchable release is never excluded
// outright.
func (p *Policy) Reject(r *release.Release) (bool, string) {
	if p.RejectBatch && r.IsBatch {
		return true, "batch"
	}
	if p.ResolutionRejected(r.Resolution) {
		if r.Resolution == "" {
			return true, "no readable resolution"
		}
		return true, fmt.Sprintf("resolution %s below floor %s", r.Resolution, p.ResolutionFloor)
	}
	return false, ""
}

// Rank scores a release against the policy. Lower is better.
//
// This is the primary ordering input. Group preference is applied separately
// and takes precedence, because which group posted a release says more about
// its quality than any of these attributes do.
func (p *Policy) Rank(r *release.Release) int {
	sum := 0

	if rank, ok := p.CodecRankOf(r.Codec); ok {
		sum += rank
	}
	if rank, ok := p.ResolutionPenalty[strings.ToLower(r.Resolution)]; ok {
		sum += rank
	}
	if release.IsDub(r.Title) {
		sum += p.PenaltyDub
	}
	if r.IsUncensored {
		sum += p.PenaltyUncensored
	}
	return sum
}
