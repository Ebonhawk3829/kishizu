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
}

// Result is the outcome of matching one release against one show.
type Result struct {
	Matched bool
	Episode int // local episode number; only meaningful when Matched
	Reason  string
}

// Threshold is the minimum title score for a match. Measured gap on real data:
// accepted releases 1.00, best non-match 0.33 — so this sits in empty space.
const Threshold = 0.6

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
		return Result{Matched: true, Episode: 0, Reason: "matches show, episode unreadable"}
	}

	group := r.Group
	if group == "" {
		group = "(none)"
	}

	if off, ok := s.GroupOffset(group); ok {
		ep := raw - off
		if !plausible(ep, s) {
			return Result{Reason: fmt.Sprintf("episode %d out of range (group %q, offset %d)", ep, group, off)}
		}
		return Result{Matched: true, Episode: ep, Reason: fmt.Sprintf("group %q known offset %d", group, off)}
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
	return Result{Matched: true, Episode: best, Reason: fmt.Sprintf("group %q unseen, inferred episode", group)}
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
	// Case-insensitive, substring-tolerant lookup: "[SubsPlease]" vs "SubsPlease".
	want := strings.ToLower(strings.TrimSpace(group))
	for k, v := range m.Offsets {
		have := strings.ToLower(strings.TrimSpace(k))
		if have == want || strings.Contains(want, have) || strings.Contains(have, want) {
			return v, true
		}
	}
	return 0, false
}

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
