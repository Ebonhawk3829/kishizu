// Package match decides which show and which episode a release belongs to.
package match

import (
	"fmt"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/release"
)

// Show is the minimum the matcher needs. The store's Show will satisfy this;
// keeping it as an interface lets the matcher be tested without a database.
type Show interface {
	// CanonicalName is the user's own name for the show.
	CanonicalName() string
	// Aliases are every other title the show is known by, including the
	// schedule's romaji title and any alternate-language names.
	Aliases() []string
	// MaxEpisode bounds plausible episode numbers. 0 means unknown, in which
	// case no upper bound is applied.
	MaxEpisode() int
	// GroupOffset returns the offset for a release group and whether one is known.
	GroupOffset(group string) (int, bool)
	// KnownOffsets returns every offset this show has exhibited, so an unseen
	// group can be tried against all of them.
	KnownOffsets() []int
	// GroupOffsets returns the offset for every known group. Agreement needs
	// the per-group map: KnownOffsets is deduplicated, so it cannot express
	// "three groups all agree" versus "one group happens to use this offset".
	GroupOffsets() map[string]int
}

// Result is the outcome of matching one release against one show.
type Result struct {
	Matched bool
	Episode int // local episode number; only meaningful when Matched
	// Confidence is the model's own estimate that this match is right, 0..1.
	// It is built from the evidence available rather than being a constant, so
	// it sharpens as the model learns more about the show.
	Confidence float64
	Reason     string
}

// Confident reports whether the model is sure enough to act without asking.
// Callers must not ask the user about confident results: the model already has
// the answer, so asking wastes the only scarce resource here — their attention.
func (r Result) Confident() bool { return r.Confidence >= ConfidentThreshold }

// Threshold is the minimum title score for a match. Measured gap on real data:
// accepted releases 1.00, best non-match 0.33 — so this sits in empty space.
const Threshold = 0.6

// ConfidentThreshold is the confidence at which the model stops asking.
const ConfidentThreshold = 0.75

// Evidence weights. Deliberately hand-tuned rather than learned: with a handful
// of examples per show, fitted weights overfit immediately. The structure is
// fixed but the inputs accumulate, so the estimate sharpens with context.
const (
	wAlias      = 0.5 // how well the title matches a known alias
	wGroupKnown = 0.3 // do we have this exact group's offset
	wAgreement  = 0.2 // do the known offsets agree with each other
)

// offsetAgreement is how strongly the known offsets concur, 0..1.
//
// It is what makes confidence improve with context: as more groups are learned
// and they agree, an unseen group is a safer guess. When they disagree, an
// unseen group is close to a coin flip and confidence says so.
//
// It counts GROUPS per offset, not distinct offsets. KnownOffsets returns
// distinct values, so three groups agreeing on 0 collapse to a single value and
// would otherwise look exactly as strong as one group on its own.
func offsetAgreement(s Show) float64 {
	groups := s.GroupOffsets()
	if len(groups) == 0 {
		return 0
	}
	counts := map[int]int{}
	for _, off := range groups {
		counts[off]++
	}
	total := len(groups)
	best := 0
	for _, n := range counts {
		if n > best {
			best = n
		}
	}
	return float64(best) / float64(total)
}

// confidence combines the available evidence into a 0..1 estimate.
func confidence(aliasScore float64, groupKnown bool, agreement float64) float64 {
	c := wAlias * aliasScore
	if groupKnown {
		c += wGroupKnown
	}
	c += wAgreement * agreement
	if c > 1 {
		c = 1
	}
	return c
}

// Match decides whether a release belongs to a show, and which episode it is.
//
// The offset is applied PER GROUP, not per show. This is the single most
// important finding from the prototype: within one show, groups disagree about
// numbering. BLEACH TYBW episode 7 is "07" to Erai-raws and "47" to SubsPlease,
// ToonsHub and VARYG. A single show-level offset cannot satisfy both.
func Match(s Show, title string) Result {
	score := release.TitleScore(s.Aliases(), title)
	if score < Threshold {
		return Result{Reason: fmt.Sprintf("title score %.2f below %.2f", score, Threshold)}
	}

	r := release.Parse(title)
	raw := r.RawEpisode()
	if raw == 0 {
		// Matches the show but the episode number is unreadable. This is the
		// maximally informative case for training: it needs a human.
		return Result{
			Matched:    true,
			Episode:    0,
			Confidence: wAlias * score,
			Reason:     "matches show, episode unreadable",
		}
	}

	group := r.Group
	if group == "" {
		group = "(none)"
	}
	agreement := offsetAgreement(s)

	if off, ok := s.GroupOffset(group); ok {
		ep := raw - off
		if !plausible(ep, s) {
			return Result{Reason: fmt.Sprintf("episode %d out of range (group %q, offset %d)", ep, group, off)}
		}
		return Result{
			Matched:    true,
			Episode:    ep,
			Confidence: confidence(score, true, agreement),
			Reason:     fmt.Sprintf("group %q known offset %d", group, off),
		}
	}

	// Unseen group: try every offset this show has exhibited and keep the
	// plausible results. Prefer the smallest, which is the most conservative
	// reading of an ambiguous number.
	best := 0
	for _, off := range s.KnownOffsets() {
		ep := raw - off
		if !plausible(ep, s) {
			continue
		}
		if best == 0 || ep < best {
			best = ep
		}
	}
	if best == 0 {
		return Result{Reason: fmt.Sprintf("unseen group %q, no plausible offset", group)}
	}
	// No group evidence, so confidence rests on the alias and on how much the
	// known offsets agree. This is the case that improves with context.
	return Result{
		Matched:    true,
		Episode:    best,
		Confidence: confidence(score, false, agreement),
		Reason:     fmt.Sprintf("group %q unseen, inferred episode", group),
	}
}

func plausible(ep int, s Show) bool {
	if ep < 1 {
		return false
	}
	if m := s.MaxEpisode(); m > 0 && ep > m {
		return false
	}
	return true
}

// MemShow is an in-memory Show, used for tests and for the first slice before
// the store exists.
type MemShow struct {
	Name     string
	Alias    []string
	Max      int
	Offsets  map[string]int
	Defaults []int
}

func (m *MemShow) CanonicalName() string { return m.Name }

func (m *MemShow) Aliases() []string {
	all := append([]string{m.Name}, m.Alias...)
	return all
}

func (m *MemShow) MaxEpisode() int { return m.Max }

func (m *MemShow) GroupOffset(group string) (int, bool) {
	if m.Offsets == nil {
		return 0, false
	}
	want := normaliseGroup(group)
	if want == "" {
		return 0, false
	}
	// Exact match first. Substring matching is a fallback for "[SubsPlease]"
	// versus "SubsPlease", but it must not let an unrelated group inherit
	// another's offset: "BrandNewGroup" contains "A", and silently borrowing
	// group A's offset would both mis-resolve the episode and inflate
	// confidence, since the offset would look known.
	if v, ok := m.Offsets[group]; ok {
		return v, true
	}
	for k, v := range m.Offsets {
		if normaliseGroup(k) == want {
			return v, true
		}
	}
	// Substring fallback, but only for reasonably long names: a one or two
	// character group would match almost anything.
	if len(want) >= 4 {
		for k, v := range m.Offsets {
			have := normaliseGroup(k)
			if len(have) >= 4 && (strings.Contains(want, have) || strings.Contains(have, want)) {
				return v, true
			}
		}
	}
	return 0, false
}

// normaliseGroup lowercases and strips the punctuation groups use
// interchangeably, so "Erai-raws" and "Erai_raws" are the same group.
func normaliseGroup(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(s)
	return s
}

func (m *MemShow) GroupOffsets() map[string]int { return m.Offsets }

func (m *MemShow) KnownOffsets() []int {
	if len(m.Defaults) > 0 {
		return m.Defaults
	}
	seen := map[int]bool{}
	var out []int
	for _, v := range m.Offsets {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []int{0}
	}
	return out
}
