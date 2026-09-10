// Package train implements the propose-and-confirm training loop.
//
// The user gives one seed example ("this release is episode N"), then the tool
// searches Nyaa, ranks candidates by UNCERTAINTY rather than confidence, and
// proposes the ones it is least sure about. Each answer refits the model.
//
// Ranking by uncertainty is the whole trick: a release that matches the show but
// whose episode number cannot be read is the most informative thing to ask
// about, because it is exactly the case a human resolves instantly.
package train

import (
	"fmt"
	"regexp"
	"sort"
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

// Propose returns the candidates the tool is least certain about.
//
// Candidates the model can already resolve confidently are NOT proposed. Asking
// "is this ep 7?" about a release the model reads as ep 9 is asking the user to
// confirm something the tool claims to know is false, which is both annoying and
// a waste of the only scarce resource here: the user's attention.
func (s *Session) Propose(items []nyaa.Item, n int) []Candidate {
	var out []Candidate
	for _, it := range items {
		if it.InfoHash != "" && s.Asked[it.InfoHash] {
			continue
		}
		score := release.TitleScore(s.m.Aliases(), it.Title)
		if score < match.Threshold {
			continue
		}

		res := match.Match(s.m, it.Title)
		if !res.Matched {
			continue
		}

		// Already resolved confidently: apply it silently.
		if res.Confident() {
			continue
		}

		// Uncertainty is the complement of the model's own confidence, so
		// ranking follows the evidence rather than a fixed bucket. A release
		// from a known group with a strong alias match is asked about last;
		// one from an unseen group when the known offsets disagree is asked
		// first. Previously these were constants, so the tool was exactly as
		// uncertain after fifty examples as after one.
		c := Candidate{Item: it, Episode: res.Episode, Why: res.Reason}
		c.Uncertainty = 1.0 - res.Confidence
		if res.Episode == 0 {
			// No number at all: nothing to be confident about.
			c.Uncertainty = 1.0
		}
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Uncertainty != out[j].Uncertainty {
			return out[i].Uncertainty > out[j].Uncertainty
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
		score := release.TitleScore(s.m.Aliases(), it.Title)
		if score < match.Threshold {
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
