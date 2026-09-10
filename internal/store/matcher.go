package store

import (
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/match"
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

// Aliases returns every stored alias. The canonical name is stored as an alias
// too, so this is the complete set.
func (m *Matcher) Aliases() []string {
	if len(m.sh.Aliases) > 0 {
		return m.sh.Aliases
	}
	return []string{m.sh.CanonicalName}
}

func (m *Matcher) MaxEpisode() int { return m.sh.MaxEpisode }

// GroupOffsets returns the offset for every known group. The matcher needs the
// per-group map to weigh agreement: three groups concurring is stronger
// evidence than one group on its own.
func (m *Matcher) GroupOffsets() map[string]int { return m.off }

// GroupOffset looks up a group case-insensitively and tolerates substring
// differences, because "[SubsPlease]" in a title and "SubsPlease" in the store
// should be the same group.
func (m *Matcher) GroupOffset(group string) (int, bool) {
	if group == "" {
		group = "(none)"
	}
	want := strings.ToLower(strings.TrimSpace(group))
	for k, v := range m.off {
		have := strings.ToLower(strings.TrimSpace(k))
		if have == want || strings.Contains(want, have) || strings.Contains(have, want) {
			return v, true
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
