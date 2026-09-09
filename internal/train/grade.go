package train

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Grade is the user's verdict on ONE attribute of a release.
//
// Grading per attribute rather than per release is the whole point: one release
// yields several independent signals. A release can be the right episode at a
// good resolution but from a group you dislike, and a single accept/reject
// cannot express that.
type Grade string

const (
	GradeGood       Grade = "good"       // actively wanted: prefer it
	GradeAcceptable Grade = "acceptable" // fine, but not preferred
	GradeWrong      Grade = "wrong"      // exclude it
	GradeUnknown    Grade = "unknown"    // not graded; no signal
)

// Attribute is one gradable facet of a release.
type Attribute string

const (
	AttrEpisode    Attribute = "episode"
	AttrGroup      Attribute = "group"
	AttrResolution Attribute = "resolution"
	AttrCodec      Attribute = "codec"
	AttrSource     Attribute = "source"
	AttrBatch      Attribute = "batch"
	AttrUncensored Attribute = "uncensored"
)

// AttrValue is one parsed attribute presented to the user for grading.
type AttrValue struct {
	Key   Attribute `json:"key"`
	Label string    `json:"label"` // human-readable name
	Value string    `json:"value"` // parsed value, "" when absent
	// Hint is read-only context shown beside the value. Used for the episode,
	// where the number in the title and the number in the user's list differ.
	Hint string `json:"hint"`
	// Editable marks a value the user can correct before grading. Only the
	// episode is editable: it is the one attribute the parser can get right by
	// its own rules and still be wrong for the user's list.
	Editable bool `json:"editable"`
	// Present is false when the title did not contain this attribute at all.
	// Absent attributes are still shown, because "this release has no codec
	// tag" is itself worth grading.
	Present bool `json:"present"`
}

// GradedRelease is a release broken into gradable attributes.
type GradedRelease struct {
	Title      string      `json:"title"`
	Episode    int         `json:"episode"` // resolved local episode, 0 if unknown
	RawEpisode int         `json:"raw_episode"`
	Attrs      []AttrValue `json:"attrs"`
}

// resolutionRank orders resolutions so a floor can be compared numerically.
var resolutionRank = map[string]int{
	"480p": 1, "720p": 2, "1080p": 3, "2160p": 4, "4k": 4,
}

// Inspect breaks a release title into the attributes the user can grade.
func Inspect(title string, resolvedEp int) GradedRelease {
	r := release.Parse(title)
	g := GradedRelease{
		Title:      title,
		Episode:    resolvedEp,
		RawEpisode: r.RawEpisode(),
	}
	// Every attribute is editable. The parser is deliberately dumb, so it will
	// always meet titles it reads wrongly — a group at the end instead of in
	// brackets, a codec written unusually. Letting the user correct the value
	// is cheaper and more reliable than widening the regexes forever.
	g.Attrs = []AttrValue{
		{
			Key:      AttrEpisode,
			Label:    "Episode",
			Value:    episodeValue(resolvedEp, r.RawEpisode()),
			Hint:     episodeHint(resolvedEp, r.RawEpisode()),
			Editable: true,
			Present:  r.RawEpisode() != 0,
		},
		{Key: AttrGroup, Label: "Group", Value: r.Group, Editable: true, Present: r.Group != ""},
		{Key: AttrResolution, Label: "Resolution", Value: r.Resolution, Editable: true, Present: r.Resolution != ""},
		{Key: AttrCodec, Label: "Codec", Value: r.Codec, Editable: true, Present: r.Codec != ""},
		{Key: AttrSource, Label: "Source", Value: r.Source, Editable: true, Present: r.Source != ""},
		{Key: AttrBatch, Label: "Batch", Value: strconv.FormatBool(r.IsBatch), Editable: true, Present: r.IsBatch},
		{Key: AttrUncensored, Label: "Uncensored", Value: strconv.FormatBool(r.IsUncensored), Editable: true, Present: r.IsUncensored},
	}
	return g
}

// episodeValue is the episode number in the USER's list, which is what a grade
// refers to. It is editable because the parser cannot know it: a title reading
// 47 may be episode 7 of this season.
func episodeValue(resolved, raw int) string {
	if resolved > 0 {
		return strconv.Itoa(resolved)
	}
	if raw > 0 {
		return strconv.Itoa(raw)
	}
	return ""
}

// episodeHint states what the title literally says, so the two numbers are
// never confused. "47 (unresolved)" left it ambiguous whether the question was
// about the group's number or the user's own numbering.
func episodeHint(resolved, raw int) string {
	if raw == 0 {
		return "no number in title"
	}
	if resolved == 0 {
		return fmt.Sprintf("title says %d — set the episode it really is", raw)
	}
	if resolved == raw {
		return fmt.Sprintf("title says %d", raw)
	}
	return fmt.Sprintf("title says %d, offset applied", raw)
}

// ApplyGrades turns per-attribute verdicts into matcher updates, filters and
// preferences. It is the learning step: each grade moves exactly one part of
// the model, so a wrong resolution never corrupts the episode offsets.
//
// Returns a summary of what changed, for display.
func (s *Session) ApplyGrades(g GradedRelease, grades map[Attribute]Grade, ep int) ([]string, error) {
	var notes []string

	// The episode attribute is editable, so a corrected value there wins over
	// the caller's default. This is the BLEACH case: the title says 47, the user
	// corrects it to 7, and the offset must be derived from 7.
	for _, a := range g.Attrs {
		if a.Key != AttrEpisode {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil && n > 0 {
			ep = n
		}
		break
	}

	// A corrected group must survive Teach, which re-parses the title and would
	// otherwise recover the parser's (wrong) group. This is the VARYG case: the
	// group sits at the end after a hyphen, and the user has to supply it.
	correctedGroup := ""
	for _, a := range g.Attrs {
		if a.Key == AttrGroup {
			correctedGroup = strings.TrimSpace(a.Value)
			break
		}
	}

	for _, a := range g.Attrs {
		grade, ok := grades[a.Key]
		if !ok || grade == GradeUnknown {
			continue
		}
		switch a.Key {
		case AttrEpisode:
			// Only a "good" episode grade teaches the offset. "Wrong" here means
			// the number is not this episode, which we cannot learn an offset
			// from — the user should supply the right one instead.
			if grade == GradeGood && g.RawEpisode != 0 {
				// Teach derives the group by parsing. When the user corrected it,
				// apply theirs instead: otherwise the offset lands on the
				// parser's group (often "(none)"), which would then match every
				// release with no group at all.
				if correctedGroup != "" {
					s.m.Offsets[correctedGroup] = g.RawEpisode - ep
					s.m.Defaults = distinct(s.m.Offsets)
					s.addAliases(g.Title)
					s.Accepted++
				} else if err := s.Teach(g.Title, ep); err != nil {
					return notes, err
				}
				notes = append(notes, fmt.Sprintf("learned offset for %q", effectiveGroup(g.Title, correctedGroup)))
			}
		case AttrGroup:
			if a.Value == "" {
				continue
			}
			switch grade {
			case GradeGood:
				s.pending = append(s.pending, pendingWrite{
					kind: "preference", k: "group", v: a.Value, rank: 0,
				})
				notes = append(notes, fmt.Sprintf("prefer group %q", a.Value))
			case GradeWrong:
				s.pending = append(s.pending, pendingWrite{
					kind: "filter", k: "group", op: "exclude", v: a.Value,
				})
				notes = append(notes, fmt.Sprintf("exclude group %q", a.Value))
			}
		case AttrResolution:
			if a.Value == "" {
				continue
			}
			switch grade {
			case GradeGood, GradeAcceptable:
				// An acceptable resolution sets the FLOOR: anything below this
				// is not wanted. Resolution is a floor, not a ladder.
				if rank, ok := resolutionRank[a.Value]; ok {
					// Accepting a resolution retracts any earlier exclusion of
					// it. Otherwise grading 2160p wrong and later acceptable
					// leaves both "exclude 2160p" and "min 2160p" standing,
					// which contradict each other.
					s.retract("filter", "resolution", "exclude", a.Value)
					s.pending = append(s.pending, pendingWrite{
						kind: "filter", k: "resolution", op: "min", v: a.Value, rank: rank,
					})
					notes = append(notes, fmt.Sprintf("resolution floor %s", a.Value))
				}
			case GradeWrong:
				// Excluding a resolution retracts a floor at or below it, since
				// "min 2160p" and "exclude 2160p" cannot both hold.
				if rank, ok := resolutionRank[a.Value]; ok {
					for v, r := range resolutionRank {
						if r <= rank {
							s.retract("filter", "resolution", "min", v)
						}
					}
				}
				s.pending = append(s.pending, pendingWrite{
					kind: "filter", k: "resolution", op: "exclude", v: a.Value,
				})
				notes = append(notes, fmt.Sprintf("exclude resolution %s", a.Value))
			}
		case AttrCodec:
			if a.Value == "" {
				continue
			}
			switch grade {
			case GradeGood:
				s.pending = append(s.pending, pendingWrite{
					kind: "preference", k: "codec", v: a.Value, rank: 0,
				})
				notes = append(notes, fmt.Sprintf("prefer codec %s", a.Value))
			case GradeAcceptable:
				s.pending = append(s.pending, pendingWrite{
					kind: "preference", k: "codec", v: a.Value, rank: 50,
				})
				notes = append(notes, fmt.Sprintf("accept codec %s", a.Value))
			case GradeWrong:
				// Codec is a preference, never a hard filter: a wrong codec is
				// still watchable, so demote rather than exclude.
				s.pending = append(s.pending, pendingWrite{
					kind: "preference", k: "codec", v: a.Value, rank: 99,
				})
				notes = append(notes, fmt.Sprintf("demote codec %s", a.Value))
			}
		case AttrSource:
			if a.Value == "" {
				continue
			}
			switch grade {
			case GradeGood:
				s.pending = append(s.pending, pendingWrite{
					kind: "preference", k: "source", v: a.Value, rank: 0,
				})
				notes = append(notes, fmt.Sprintf("prefer source %s", a.Value))
			case GradeWrong:
				s.pending = append(s.pending, pendingWrite{
					kind: "filter", k: "source", op: "exclude", v: a.Value,
				})
				notes = append(notes, fmt.Sprintf("exclude source %s", a.Value))
			}
		case AttrBatch:
			if grade == GradeWrong {
				s.pending = append(s.pending, pendingWrite{
					kind: "filter", k: "batch", op: "exclude", v: "true",
				})
				notes = append(notes, "exclude batches")
			}
		case AttrUncensored:
			if a.Value == "" {
				continue
			}
			switch grade {
			case GradeGood:
				s.pending = append(s.pending, pendingWrite{
					kind: "preference", k: "uncensored", v: a.Value, rank: 0,
				})
			case GradeWrong:
				s.pending = append(s.pending, pendingWrite{
					kind: "preference", k: "uncensored", v: a.Value, rank: 99,
				})
			}
			notes = append(notes, fmt.Sprintf("uncensored=%s %s", a.Value, grade))
		}
	}

	// A "wrong" episode grade is a rejection: record it so the same release is
	// not proposed again.
	if grades[AttrEpisode] == GradeWrong {
		if err := s.st.AddRejected(s.show.ID, g.Title, "wrong_episode"); err != nil {
			return notes, err
		}
		s.Rejected++
	}
	return notes, nil
}

// pendingWrite is a filter/preference to persist on Commit.
//
// Writes are deferred so that cancelling a session discards them. Previously
// rejections wrote straight to the database while offsets waited for Commit,
// so cancelling kept half the session's learning.
type pendingWrite struct {
	kind string // filter | preference
	k    string
	op   string
	v    string
	rank int
}

// retract drops a pending write, and deletes any matching row already in the
// database. Both are needed: within one session the rule may still be pending,
// but across sessions it will already have been committed.
func (s *Session) retract(kind, k, op, v string) {
	kept := s.pending[:0]
	for _, p := range s.pending {
		if p.kind == kind && p.k == k && p.op == op && p.v == v {
			continue
		}
		kept = append(kept, p)
	}
	s.pending = kept

	if kind == "filter" {
		s.retracted = append(s.retracted, store.Filter{Kind: k, Op: op, Value: v})
	}
}

func groupOf(title string) string {
	g := release.Parse(title).Group
	if g == "" {
		return "(none)"
	}
	return g
}

// effectiveGroup prefers a user-corrected group over the parsed one.
func effectiveGroup(title, corrected string) string {
	if corrected != "" {
		return corrected
	}
	return groupOf(title)
}

// flushPending persists the deferred filters and preferences.
func (s *Session) flushPending() error {
	// Deduplicate: later grades on the same key/value win.
	type key struct{ kind, k, v string }
	seen := map[key]pendingWrite{}
	var order []key
	for _, p := range s.pending {
		k := key{p.kind, p.k, p.v}
		if _, ok := seen[k]; !ok {
			order = append(order, k)
		}
		seen[k] = p
	}
	sort.SliceStable(order, func(i, j int) bool {
		return order[i].kind < order[j].kind
	})

	for _, k := range order {
		p := seen[k]
		if p.kind == "filter" {
			if err := s.st.AddFilter(s.show.ID, store.Filter{Kind: p.k, Op: p.op, Value: p.v}); err != nil {
				return err
			}
			continue
		}
		if err := s.st.AddPreference(s.show.ID, store.Preference{Kind: p.k, Value: p.v, Rank: p.rank}); err != nil {
			return err
		}
	}
	s.pending = nil
	return nil
}

// flushRetracted deletes rules this session contradicted.
func (s *Session) flushRetracted() error {
	for _, f := range s.retracted {
		if err := s.st.DeleteFilter(s.show.ID, f); err != nil {
			return err
		}
	}
	s.retracted = nil
	return nil
}

// ParseGrade maps user input to a Grade, tolerating the obvious spellings.
func ParseGrade(s string) (Grade, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "good", "g", "yes", "y", "want", "prefer":
		return GradeGood, nil
	case "acceptable", "ok", "a", "fine", "meh":
		return GradeAcceptable, nil
	case "wrong", "w", "no", "n", "bad", "exclude":
		return GradeWrong, nil
	case "", "unknown", "?", "skip":
		return GradeUnknown, nil
	}
	return GradeUnknown, fmt.Errorf("unknown grade %q", s)
}
