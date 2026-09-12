package store

import (
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/release"
)

// Matcher adapts a stored Show to the interface the matcher needs.
//
// The matcher deliberately depends on a narrow interface rather than the store,
// so it stays testable without a database. This is the adapter that joins them.
type Matcher struct {
	st  *Store
	sh  *Show
	off map[string]int
}

// NewMatcher loads a show's aliases and offsets for matching.
func (s *Store) NewMatcher(sh *Show) (*Matcher, error) {
	off, err := s.GroupOffsets(sh.ID)
	if err != nil {
		return nil, err
	}
	return &Matcher{st: s, sh: sh, off: off}, nil
}

func (m *Matcher) CanonicalName() string { return m.sh.CanonicalName }

// Aliases returns every stored alias, plus a sanitised variant of each.
//
// The sanitised variants exist because kishizu writes files with
// Windows-forbidden characters stripped — "Re:ZERO" lands on disk as
// "ReZERO". Normalise turns punctuation into spaces, so the two forms
// tokenise differently ("re zero" vs "rezero") and score zero against each
// other. A long canonical name survives that on its other tokens; a short
// alias does not. Comparing against both forms makes the round trip work
// regardless of which one the filename carries.
func (m *Matcher) Aliases() []string {
	raw := m.sh.Aliases
	if len(raw) == 0 {
		raw = []string{m.sh.CanonicalName}
	}
	out := make([]string, 0, len(raw)*2)
	seen := map[string]bool{}
	for _, a := range raw {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
		if s := release.Sanitise(a); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (m *Matcher) MaxEpisode() int { return m.sh.MaxEpisode }

// GroupOffsets returns the offset for every known group. The matcher needs the
// per-group map to weigh agreement: three groups concurring is stronger
// evidence than one group on its own.
func (m *Matcher) GroupOffsets() map[string]int { return m.off }

// GroupOffset looks up a group's offset.
//
// Exact match first, then a punctuation-normalised comparison. Substring
// matching is deliberately restricted to names of four characters or more: a
// group named "A" would otherwise match almost anything, and an unrelated group
// silently borrowing another's offset both mis-resolves the episode and
// inflates confidence, since the offset would look known.
func (m *Matcher) GroupOffset(group string) (int, bool) {
	if group == "" {
		group = "(none)"
	}
	if v, ok := m.off[group]; ok {
		return v, true
	}
	want := release.NormaliseGroup(group)
	if want == "" {
		return 0, false
	}
	for k, v := range m.off {
		if release.NormaliseGroup(k) == want {
			return v, true
		}
	}
	if len(want) >= 4 {
		for k, v := range m.off {
			have := release.NormaliseGroup(k)
			if len(have) >= 4 && (strings.Contains(want, have) || strings.Contains(have, want)) {
				return v, true
			}
		}
	}
	return 0, false
}

// KnownOffsets returns the distinct offsets this show has exhibited, so an
// unseen group can be tried against all of them.
func (m *Matcher) KnownOffsets() []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range m.off {
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

// Compile-time check that the adapter satisfies the matcher's contract.
var _ match.Show = (*Matcher)(nil)
