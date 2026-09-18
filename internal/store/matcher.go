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
	st    *Store
	sh    *Show
	off   map[string]int
	vocab *release.Vocabulary
}

// NewMatcher loads a show's aliases, offsets and the learned vocabulary for
// matching.
//
// The vocabulary is applied before every rule evaluation, so a release written
// in a spelling the parser does not know still reads correctly at the point
// of enforcement — the whole reason training teaches tokens.
func (s *Store) NewMatcher(sh *Show) (*Matcher, error) {
	off, err := s.GroupOffsets(sh.ID)
	if err != nil {
		return nil, err
	}
	var vocab *release.Vocabulary
	if entries, err := s.Vocabulary(); err == nil {
		vocab = release.NewVocabulary()
		vocab.Load(entries)
	} else {
		// Fail open: matching still works, the parser just reads fewer titles.
		vocab = release.NewVocabulary()
	}
	return &Matcher{st: s, sh: sh, off: off, vocab: vocab}, nil
}

// Parse parses a title with the learned vocabulary applied. All enforcement
// paths (listener, ranker, grab) go through this so a learned token is
// honoured everywhere.
func (m *Matcher) Parse(title string) release.Release {
	r := release.Parse(title)
	m.vocab.ApplyVocabulary(&r)
	return r
}

func (m *Matcher) CanonicalName() string { return m.sh.CanonicalName }

// Aliases returns every stored alias. The canonical name is stored as an alias
// too, so this is the complete set.
//
// These are used to match Nyaa release titles, where fuzzy matching is the
// whole point: release names are written by strangers and never match a
// canonical name exactly. They are NOT used to resolve the watch signal —
// that resolves by exact filename, because kishizu wrote the file itself.
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
