package train

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/release"
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
	// GradeAbsent is "the title does not say this" — the parser inferred a
	// value that is not there.
	//
	// Distinct from GradeWrong, which means "the title says X and I do not
	// want X". Absent means there is nothing to want: the claim itself is
	// invented. Without this the two look identical, so a hallucinated value
	// could only be corrected, never rejected — and correcting it writes a
	// rule about something the release never contained.
	GradeAbsent Grade = "absent"
)

// Attribute is one gradable facet of a release.
type Attribute string

const (
	AttrEpisode    Attribute = "episode"
	AttrGroup      Attribute = "group"
	AttrResolution Attribute = "resolution"
	AttrCodec      Attribute = "codec"
	AttrSource     Attribute = "source"
	AttrService    Attribute = "service"
	AttrAudio      Attribute = "audio"
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
	Title      string `json:"title"`
	Episode    int    `json:"episode"` // resolved local episode, 0 if unknown
	RawEpisode int    `json:"raw_episode"`
	// Confidence is the model's own estimate that this match is right. Shown
	// so the user can see whether the model is guessing.
	Confidence float64     `json:"confidence"`
	Attrs      []AttrValue `json:"attrs"`
}

// resolutionRank is the shared ordering from the release package: the trainer
// writes the floor and the listener enforces it, so they must agree.
var resolutionRank = release.ResolutionPenalty

// Inspect breaks a release title into the attributes the user can grade.
func Inspect(title string, resolvedEp int) GradedRelease {
	return InspectWithConfidence(title, resolvedEp, 0)
}

// InspectWithConfidence is Inspect plus the model's confidence in the match.
func InspectWithConfidence(title string, resolvedEp int, conf float64) GradedRelease {
	r := release.Parse(title)
	g := GradedRelease{
		Title:      title,
		Episode:    resolvedEp,
		RawEpisode: r.RawEpisode(),
		Confidence: conf,
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
		{Key: AttrService, Label: "Service", Value: r.Service, Editable: true, Present: r.Service != ""},
		{Key: AttrAudio, Label: "Audio", Value: r.Audio, Editable: true, Present: r.Audio != ""},
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

// episodeHint states what the title literally says AND the offset that follows,
// so the two numbers are never confused and the derivation is visible.
//
// Showing the arithmetic is the "which means the offset is?" step: the model
// states its conclusion rather than computing it silently, so a wrong episode
// is caught at the moment it is entered instead of three episodes later when
// something downloads wrong.
func episodeHint(resolved, raw int) string {
	if raw == 0 {
		return "no number in title"
	}
	if resolved == 0 {
		return fmt.Sprintf("title says %d — set the episode it really is", raw)
	}
	off := raw - resolved
	if off == 0 {
		return fmt.Sprintf("title says %d, so offset is 0", raw)
	}
	return fmt.Sprintf("title says %d, so offset is %d", raw, off)
}

// ApplyGrades applies a corrected parse to the working model. Training
// calibrates the parser only: the episode and group attributes teach the
// per-group offset, and corrections to other attributes are vocabulary
// teaching, which the caller records via the vocab endpoint before calling
// this. Quality verdicts (resolution, codec, batch, dub, uncensored) are
// global rules set in advance and are never written here.
//
// Returns a summary of what changed, for display.
func (s *Session) ApplyGrades(g GradedRelease, grades map[Attribute]Grade, ep int) ([]string, error) {
	var notes []string

	// The episode attribute is editable, so a corrected value there wins over
	// the caller's default. This is the BLEACH case: the title says 47, the
	// user corrects it to 7, and the offset must be derived from 7.
	for _, a := range g.Attrs {
		if a.Key != AttrEpisode {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil && n > 0 {
			ep = n
		}
		break
	}

	// A corrected group must survive Teach, which re-parses the title and
	// would otherwise recover the parser's (wrong) group. This is the VARYG
	// case: the group sits at the end after a hyphen, and the user has to
	// supply it.
	correctedGroup := ""
	for _, a := range g.Attrs {
		if a.Key == AttrGroup {
			correctedGroup = strings.TrimSpace(a.Value)
			break
		}
	}

	// Only the episode offset is learned here, and only from a confirmed
	// parse with a readable raw number.
	if grade := grades[AttrEpisode]; grade == GradeGood && g.RawEpisode != 0 {
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
	if grade := grades[AttrEpisode]; grade == GradeWrong {
		s.Rejected++
		notes = append(notes, "not this episode; no offset learned")
	}
	return notes, nil
}

// effectiveGroup prefers a user-corrected group over the parsed one.
func effectiveGroup(title, corrected string) string {
	if corrected != "" {
		return corrected
	}
	g := release.Parse(title).Group
	if g == "" {
		return "(none)"
	}
	return g
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
	case "absent", "none", "not present", "hallucinated", "invented":
		return GradeAbsent, nil
	case "", "unknown", "?", "skip":
		return GradeUnknown, nil
	}
	return GradeUnknown, fmt.Errorf("unknown grade %q", s)
}
