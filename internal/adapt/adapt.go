// Package adapt joins the persistence layer to the domain logic that consumes
// it.
//
// It exists to keep the dependency direction honest. The matcher defines a
// narrow interface (match.Show) so it can be tested without a database — but
// the type that satisfies it needs a database to answer, so it cannot live in
// match. It used to live in store, which made the persistence layer import the
// domain logic it was supposed to be decoupled from: touching the matching
// model recompiled the store.
//
// Putting the adapter here leaves both sides independent. store knows nothing
// about matching, match knows nothing about SQLite, and this package is the
// only place that knows about both.
package adapt

import (
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Show adapts a stored show to the interface the matcher needs.
type Show struct {
	sh    *store.Show
	off   map[string]int
	vocab *release.Vocabulary
}

// Compile-time check that the adapter satisfies the matcher's contract.
var _ match.Show = (*Show)(nil)

// NewShow builds a matcher view of a stored show.
//
// offsets and vocab are supplied by the caller rather than loaded here, so
// this package does not need the store and the caller can share one
// vocabulary across many shows. Loading the whole vocabulary per show per
// poll was a full table scan on every tick.
func NewShow(sh *store.Show, offsets map[string]int, vocab *release.Vocabulary) *Show {
	if vocab == nil {
		vocab = release.NewVocabulary()
	}
	return &Show{sh: sh, off: offsets, vocab: vocab}
}

// Parse parses a title with the learned vocabulary applied. All enforcement
// paths (listener, ranker, grab) go through this so a learned token is
// honoured everywhere.
func (s *Show) Parse(title string) release.Release {
	r := release.Parse(title)
	s.vocab.ApplyVocabulary(&r)
	return r
}

func (s *Show) CanonicalName() string { return s.sh.CanonicalName }

// Aliases returns every stored alias. The canonical name is stored as an alias
// too, so this is the complete set.
//
// These are used to match Nyaa release titles, where fuzzy matching is the
// whole point: release names are written by strangers and never match a
// canonical name exactly. They are NOT used to resolve the watch signal —
// that resolves by exact filename, because kishizu wrote the file itself.
func (s *Show) Aliases() []string {
	if len(s.sh.Aliases) > 0 {
		return s.sh.Aliases
	}
	return []string{s.sh.CanonicalName}
}

func (s *Show) MaxEpisode() int { return s.sh.MaxEpisode }

// GroupOffsets returns the offset for every known group. The matcher needs the
// per-group map to weigh agreement: three groups concurring is stronger
// evidence than one group on its own.
func (s *Show) GroupOffsets() map[string]int { return s.off }

// GroupOffset looks up a group's offset.
//
// Exact match first, then a punctuation-normalised comparison. Substring
// matching is deliberately restricted to names of four characters or more: a
// group named "A" would otherwise match almost anything, and an unrelated group
// silently borrowing another's offset both mis-resolves the episode and
// inflates confidence, since the offset would look known.
func (s *Show) GroupOffset(group string) (int, bool) {
	if group == "" {
		group = "(none)"
	}
	if v, ok := s.off[group]; ok {
		return v, true
	}
	want := release.NormaliseGroup(group)
	if want == "" {
		return 0, false
	}
	for k, v := range s.off {
		if release.NormaliseGroup(k) == want {
			return v, true
		}
	}
	if len(want) >= 4 {
		for k, v := range s.off {
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
func (s *Show) KnownOffsets() []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range s.off {
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
