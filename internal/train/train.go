// Package train implements the training loop.
//
// The tool searches Nyaa using the show's aliases and offers the results for
// grading, most informative first. Each grade refits the model: episode offsets
// per release group, hard filters, and soft preferences.
//
// Candidates are ordered by NOVELTY — how much of a title the model has not
// seen before — not by the model's own uncertainty. Novelty is a measurable
// property of the data; uncertainty would ask the model to rate itself. Every
// grade therefore teaches something new, and an empty list means there is
// genuinely nothing left to learn.
//
// Nothing is filtered out for looking confidently resolved. A confidently wrong
// model is the worst failure mode, and hiding those is how it goes unnoticed.
package train

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

var (
	regexpGroup   = regexp.MustCompile(`^\s*\[[^\]]+\]\s*`)
	regexpParen   = regexp.MustCompile(`\(([^)]*)\)`)
	regexpBracket = regexp.MustCompile(`\[[^\]]*\]`)
	regexpSub     = regexp.MustCompile(`(?i)sub|dub|audio|multi|weekly`)
	regexpQuality = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|web-?dl|webrip|web|bd|blu-?ray|remux|avc|hevc|h\.?264|h\.?265|x264|x265|av1|aac|flac|opus|e-?ac-?3|ac3|ddp|10bit|hi10|dsnp|cr|amzn|nf|adn|iqiyi|dual|multi|subs?|dub|dubbed|raw|batch|complete|v2|v3|repack|proper|weekly)\b`)
)

// Reason is why the user rejected a candidate. It determines whether the
// matcher is updated or only the filters/preferences.
type Reason string

const (
	ReasonWrongEpisode Reason = "wrong_episode" // matcher problem
	ReasonWrongShow    Reason = "wrong_show"    // matcher problem
	ReasonBatch        Reason = "batch"         // filter: exclude batches
	ReasonDub          Reason = "dub"           // filter: exclude dubs
	ReasonCodec        Reason = "codec"         // preference: ranking only
	ReasonQuality      Reason = "quality"       // filter or preference
	ReasonOther        Reason = "other"
)

// UpdatesMatcher reports whether this reason means the matching model is wrong.
// Only these two should change aliases or offsets; everything else is a
// quality preference and must not corrupt the matcher.
func (r Reason) UpdatesMatcher() bool {
	return r == ReasonWrongEpisode || r == ReasonWrongShow
}

// Candidate is a release the tool is asking about.
type Candidate struct {
	Item        nyaa.Item
	Episode     int // 0 when unreadable — the most informative case
	Uncertainty float64
	Why         string
	// Novelty is how much of this title the model has NOT seen before, 0..1.
	// Used to order the list so every grade teaches something new.
	Novelty float64
	// Unseen lists what is new about it, for display.
	Unseen []string
}

// Session is one training run for one show.
type Session struct {
	st   *store.Store
	show *store.Show
	m    *match.MemShow // working model, refit as answers arrive

	TargetEp int
	Asked    map[string]bool
	Accepted int
	Rejected int

	// pending holds filters/preferences learned this session, written on Commit
	// so that cancelling discards them.
	pending []pendingWrite
	// retracted are rules this session contradicted, deleted on Commit.
	retracted []store.Filter
}

// NewSession starts training for a show at a given episode.
func NewSession(st *store.Store, sh *store.Show, targetEp int) (*Session, error) {
	offsets, err := st.GroupOffsets(sh.ID)
	if err != nil {
		return nil, err
	}
	known := distinct(offsets)

	return &Session{
		st:       st,
		show:     sh,
		m:        &match.MemShow{Name: sh.CanonicalName, Alias: sh.Aliases, Max: sh.MaxEpisode, Offsets: offsets, Defaults: known},
		TargetEp: targetEp,
		Asked:    map[string]bool{},
	}, nil
}

// Seed records the initial example and derives the first offset from it.
func (s *Session) Seed(title string) error {
	r := release.Parse(title)
	raw := r.RawEpisode()
	if raw == 0 {
		return fmt.Errorf("cannot read an episode number from %q", title)
	}
	group := r.Group
	if group == "" {
		group = "(none)"
	}
	off := raw - s.TargetEp

	s.m.Offsets[group] = off
	s.m.Defaults = distinct(s.m.Offsets)

	// The seed also teaches us aliases.
	for _, a := range extractAliases(title) {
		s.m.Alias = append(s.m.Alias, a)
	}
	s.Accepted++
	return nil
}

// Teach records a known-good example supplied by the user: this release title
// is this episode. It is the same learning step as Seed, but available at any
// point in the session rather than only at the start.
//
// The user often knows a correct release that the feed ranking buried, or wants
// to correct a wrong offset directly instead of answering questions until the
// model happens to converge.
func (s *Session) Teach(title string, ep int) error {
	r := release.Parse(title)
	raw := r.RawEpisode()
	if raw == 0 {
		return fmt.Errorf("cannot read an episode number from %q", title)
	}
	if ep < 1 {
		return fmt.Errorf("episode must be >= 1, got %d", ep)
	}
	group := r.Group
	if group == "" {
		group = "(none)"
	}
	s.m.Offsets[group] = raw - ep
	s.m.Defaults = distinct(s.m.Offsets)
	s.addAliases(title)
	s.Accepted++
	return nil
}

// noveltyWeights say how much a newly-seen value is worth learning.
//
// Weighted by consequence, not by count. An unseen release group matters most
// because the group drives the episode offset — the thing that decides whether
// the right episode gets downloaded at all. An unseen codec string is trivia by
// comparison. Without this weighting a title with three novel quality tags
// would outrank one with a brand-new group, which is backwards.
const (
	wUnseenGroup     = 1.0
	wUnseenEpisode   = 0.6
	wUnseenQuality   = 0.2
	wUnseenStructure = 0.3
)

// novelty scores how much of a title the model has not seen before, 0..1, and
// lists what is new about it.
//
// This replaces ordering by the model's own uncertainty. Uncertainty asks the
// model to rate itself, which is a judgement we would have to trust blindly.
// Novelty is a measurable property of the data: either this group is in the
// known set or it is not. Every grade then teaches something, and when the
// list goes quiet there is genuinely nothing left to learn — which is a
// stopping signal the user can see rather than infer.
func (s *Session) novelty(title string) (float64, []string) {
	r := release.Parse(title)
	score := 0.0
	var unseen []string

	group := r.Group
	if group == "" {
		group = "(none)"
	}
	if _, known := s.m.Offsets[group]; !known {
		score += wUnseenGroup
		unseen = append(unseen, "group "+group)
	}

	// An episode number outside the range already confirmed for this show.
	if raw := r.RawEpisode(); raw > 0 {
		novel := true
		for _, off := range s.m.KnownOffsets() {
			if ep := raw - off; ep >= 1 && (s.m.Max <= 0 || ep <= s.m.Max) {
				novel = false
				break
			}
		}
		if novel {
			score += wUnseenEpisode
			unseen = append(unseen, "episode "+strconv.Itoa(raw))
		}
	}

	// Quality tokens the model has no rule about yet.
	for _, tok := range []string{r.Resolution, r.Codec, r.Source, r.Service, r.Audio} {
		if tok == "" {
			continue
		}
		if !s.seenValue(tok) {
			score += wUnseenQuality
			unseen = append(unseen, tok)
			break // one is enough to make the title worth showing
		}
	}

	// A title shape not matching anything already graded: different word order
	// or punctuation usually means a different group's convention.
	if len(s.m.Alias) > 0 && release.TitleScore(s.m.Aliases(), title) < match.Threshold {
		score += wUnseenStructure
		unseen = append(unseen, "unfamiliar title")
	}

	if score > 1 {
		score = 1
	}
	return score, unseen
}

// seenValue reports whether a quality token appears in any rule or preference
// already learned for this show.
func (s *Session) seenValue(v string) bool {
	for _, p := range s.pending {
		if strings.EqualFold(p.v, v) {
			return true
		}
	}
	for _, f := range s.retracted {
		if strings.EqualFold(f.Value, v) {
			return true
		}
	}
	return false
}

// Propose returns the candidates worth grading, most informative first.
//
// Nothing is filtered out for being confidently resolved. A model that is
// confidently wrong is the worst failure mode, and hiding those is exactly how
// it goes unnoticed. Everything the alias search matched is offered; ordering
// decides what gets attention first.
//
// Ordering is by NOVELTY — how much of the title the model has not seen — not
// by the model's own uncertainty. Uncertainty asks the model to rate itself;
// novelty is a measurable property of the data. See novelty() for why that
// distinction matters.
func (s *Session) Propose(items []nyaa.Item, n int) []Candidate {
	var out []Candidate
	for _, it := range items {
		if it.InfoHash != "" && s.Asked[it.InfoHash] {
			continue
		}
		if ok, _ := match.AliasGate(s.m.Aliases(), it.Title); !ok {
			continue
		}

		res := match.Match(s.m, it.Title)
		if !res.Matched {
			continue
		}

		c := Candidate{Item: it, Episode: res.Episode, Why: res.Reason}
		c.Uncertainty = 1.0 - res.Confidence
		if res.Episode == 0 {
			// No number at all: nothing to be confident about.
			c.Uncertainty = 1.0
		}
		c.Novelty, c.Unseen = s.novelty(it.Title)
		out = append(out, c)
	}

	// Most novel first. Seeders break ties so that, between two equally
	// informative titles, the healthier one is offered — it is more likely to
	// still be there if the user wants to check it.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Novelty != out[j].Novelty {
			return out[i].Novelty > out[j].Novelty
		}
		return out[i].Item.Seeders > out[j].Item.Seeders
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Resolved returns releases the model matched on its own from a known group
// offset. These are deliberately excluded from Propose; they are surfaced
// separately so the user can see the model applying what it has learned.
func (s *Session) Resolved(items []nyaa.Item, n int) []Candidate {
	var out []Candidate
	for _, it := range items {
		if ok, _ := match.AliasGate(s.m.Aliases(), it.Title); !ok {
			continue
		}
		res := match.Match(s.m, it.Title)
		if !res.Matched || !res.Confident() {
			continue
		}
		out = append(out, Candidate{Item: it, Episode: res.Episode, Why: res.Reason})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Item.Seeders > out[j].Item.Seeders
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Accept records a positive answer and refits.
func (s *Session) Accept(c Candidate) error {
	s.Asked[c.Item.InfoHash] = true
	s.Accepted++

	r := release.Parse(c.Item.Title)
	raw := r.RawEpisode()
	if raw == 0 {
		// Nothing numeric to learn, but the aliases still help.
		s.addAliases(c.Item.Title)
		return nil
	}
	group := r.Group
	if group == "" {
		group = "(none)"
	}
	s.m.Offsets[group] = raw - s.TargetEp
	s.m.Defaults = distinct(s.m.Offsets)
	s.addAliases(c.Item.Title)
	return nil
}

// Reject records a negative answer. Only matcher-related reasons change the
// model; quality reasons are recorded as filters/preferences instead.
func (s *Session) Reject(c Candidate, reason Reason) error {
	s.Asked[c.Item.InfoHash] = true
	s.Rejected++

	if err := s.st.AddRejected(s.show.ID, c.Item.Title, string(reason)); err != nil {
		return err
	}

	switch reason {
	case ReasonBatch:
		return s.st.AddFilter(s.show.ID, store.Filter{Kind: "batch", Op: "exclude", Value: "true"})
	case ReasonDub:
		return s.st.AddFilter(s.show.ID, store.Filter{Kind: "source", Op: "exclude", Value: "dub"})
	case ReasonCodec:
		// Ranking only: never accept/reject on codec.
		r := release.Parse(c.Item.Title)
		if r.Codec != "" {
			return s.st.AddPreference(s.show.ID, store.Preference{Kind: "codec", Value: r.Codec, Rank: 99})
		}
		return nil
	case ReasonWrongShow, ReasonWrongEpisode:
		// The matcher got this wrong. Do not add the title's tokens as aliases,
		// since they led us astray.
		return nil
	}
	return nil
}

// Show exposes the working model so callers can match against it mid-session.
func (s *Session) Show() *match.MemShow { return s.m }

// confidenceFor is the model's confidence in a title, using everything learned
// so far. Exposed so tests can assert that confidence rises with training.
func (s *Session) confidenceFor(title string) float64 {
	return match.Match(s.m, title).Confidence
}

// MarkAsked records a release as seen, so it is not proposed again.
func (s *Session) MarkAsked(title string) {
	if title != "" {
		s.Asked[title] = true
	}
}

// Offsets returns the current per-group offsets, for display during training.
func (s *Session) Offsets() map[string]int {
	out := make(map[string]int, len(s.m.Offsets))
	for k, v := range s.m.Offsets {
		out[k] = v
	}
	return out
}

// Commit persists what the session learned.
func (s *Session) Commit() error {
	for g, off := range s.m.Offsets {
		if err := s.st.SetGroupOffset(s.show.ID, g, off, "training"); err != nil {
			return err
		}
	}
	for _, a := range s.m.Alias {
		if err := s.st.AddAlias(s.show.ID, a); err != nil {
			return err
		}
	}
	if err := s.flushRetracted(); err != nil {
		return err
	}
	return s.flushPending()
}

func (s *Session) addAliases(title string) {
	for _, a := range extractAliases(title) {
		if !containsFold(s.m.Alias, a) {
			s.m.Alias = append(s.m.Alias, a)
		}
	}
}

// extractAliases pulls plausible show titles out of a release name: the text
// before the episode marker, plus any parenthesised romaji/official title.
func extractAliases(title string) []string {
	s := title
	s = strings.TrimSpace(regexpGroup.ReplaceAllString(s, ""))

	var paren []string
	for _, pm := range regexpParen.FindAllStringSubmatch(s, -1) {
		for _, part := range strings.Split(pm[1], ",") {
			part = strings.TrimSpace(part)
			if part != "" && !regexpSub.MatchString(part) {
				paren = append(paren, part)
			}
		}
	}
	s = regexpParen.ReplaceAllString(s, " ")

	if m := release.ReSxE.FindStringIndex(s); m != nil {
		s = s[:m[0]]
	} else if m := release.ReBare.FindStringIndex(s); m != nil {
		s = s[:m[0]]
	}
	s = regexpBracket.ReplaceAllString(s, " ")
	s = regexpQuality.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	var out []string
	if len(s) > 2 {
		out = append(out, s)
	}
	return append(out, paren...)
}

func distinct(m map[string]int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range m {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []int{0}
	}
	sort.Ints(out)
	return out
}

func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}
