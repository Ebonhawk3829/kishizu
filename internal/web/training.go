package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
)

// ---------- training ----------

// trainSession is the in-flight training state. One at a time is a feature:
// training is a human-driven, single-user activity, and the UI has a single
// modal. The mutex makes that single-flight guarantee real under concurrent
// requests (a double-click on Train, two tabs) rather than trusting the
// client to be polite.
type trainSession struct {
	mu     sync.Mutex
	active bool
	sess   *train.Session
	show   *store.Show
	items  []nyaa.Item
	ep     int
}

type startRequest struct {
	ShowID int64 `json:"show_id"`
}

// handleTrainStart opens a training session scoped to a show.
//
// There is no seed example and no episode picker. The session opens on the raw
// results of an alias search and the user confirms the parse of one; the
// episode comes from the release they choose.
//
// No seeding, deliberately. Seeding from a "known good" release teaches only
// what to accept — it can never show what to reject, and rejection is most of
// what the matcher does. A raw alias search is an unbiased sample: some right,
// some wrong, some unreadable. Confirming across that gives both signals.
//
// There is also no paste-a-link path. If a release does not appear in the
// alias-derived results, the alias set does not match it — and if it does not
// match during training it will not match during hunting either. The fix is to
// correct the alias, not to route around it.
//
// Candidates are filtered to releases published after this season's first
// episode aired (minus a week's slack). A group that numbers this season with
// a carry-over offset has "episode 1" uploads from months ago that parse as
// perfectly plausible episodes of the NEW season; without the time filter a
// training run can anchor an offset to last season's file.
func (s *Server) handleTrainStart(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	sh, err := s.st.GetShow(req.ShowID)
	if err != nil || sh == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("show %d not found", req.ShowID))
		return
	}

	// Query on every alias, not just the canonical name. Nyaa's search is a
	// plain substring match, so a long specific name misses groups that write
	// the title differently — and training is exactly where you need to see
	// those groups, since learning their offsets is the point.
	items, err := nyaa.FetchAll(nil, nyaa.FeedURLsFor(sh.CanonicalName, sh.Aliases))
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("fetch feed: %w", err))
		return
	}
	items = filterToSeason(s.st, items, sh)
	if len(items) == 0 {
		// Not an error to paper over: it means the alias set matches nothing,
		// which is a real defect worth saying out loud.
		writeErr(w, http.StatusNotFound, fmt.Errorf(
			"no releases found for %q — its aliases match nothing on Nyaa, so it could never be downloaded either",
			sh.CanonicalName))
		return
	}

	// The episode is not chosen up front. NewSession wants a target, so use the
	// next unwatched as a starting hint; it is overridden by whatever release
	// the user actually confirms.
	ep := s.st.NextUnwatched(sh.ID)

	sess, err := train.NewSession(s.st, sh, ep)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	s.session.active = true
	s.session.sess = sess
	s.session.show = sh
	s.session.items = items
	s.session.ep = ep

	writeJSON(w, s.trainStateLocked())
}

// filterToSeason drops releases published before this season could have had
// any episodes. The anchor is episode 1's projected air date minus a week's
// slack for early uploads and timezone slop. Shows with no air date keep
// everything: there is nothing to anchor to, and inventing a bound would be
// guessing.
func filterToSeason(st *store.Store, items []nyaa.Item, sh *store.Show) []nyaa.Item {
	eps, err := st.EpisodesForShow(sh.ID)
	if err != nil {
		return items
	}
	var first *time.Time
	for _, ep := range eps {
		if ep.Number == 1 && ep.AirsAt != nil {
			first = ep.AirsAt
			break
		}
	}
	if first == nil {
		return items
	}
	cutoff := first.AddDate(0, 0, -7)
	var out []nyaa.Item
	for _, it := range items {
		if it.PubDate.IsZero() || !it.PubDate.Before(cutoff) {
			out = append(out, it)
		}
	}
	return out
}

type trainStateJSON struct {
	Active     bool            `json:"active"`
	ShowID     int64           `json:"show_id"`
	Show       string          `json:"show"`
	Episode    int             `json:"episode"`
	Accepted   int             `json:"accepted"`
	Rejected   int             `json:"rejected"`
	Offsets    map[string]int  `json:"offsets"`
	Candidates []candidateJSON `json:"candidates"`
	// Resolved are releases the model matched on its own from a known group
	// offset. Shown so the user can see the model is applying what it learned,
	// instead of wondering why some releases never get asked about.
	Resolved []candidateJSON `json:"resolved"`
}

type candidateJSON struct {
	Index       int      `json:"index"`
	Title       string   `json:"title"`
	Episode     int      `json:"episode"`
	Seeders     int      `json:"seeders"`
	Size        string   `json:"size"`
	Resolution  string   `json:"resolution"`
	Codec       string   `json:"codec"`
	Group       string   `json:"group"`
	Why         string   `json:"why"`
	Uncertainty float64  `json:"uncertainty"`
	// Novelty is how much of this title the model has not seen, 0..1. Drives
	// ordering: the most informative candidate is offered first.
	Novelty     float64  `json:"novelty"`
	// Unseen names what is new about it, so the user can see why it is at the
	// top rather than taking the ordering on faith.
	Unseen      []string `json:"unseen"`
	// Attrs is the full parse — every attribute the parser reads, present or
	// not. Quick accept confirms all of these, so all of them must be shown:
	// asking the user to confirm a source and service they were never shown is
	// not confirmation.
	Attrs       []train.AttrValue `json:"attrs"`
}

func (s *Server) trainStateLocked() trainStateJSON {
	st := trainStateJSON{
		Active:   s.session.active,
		Episode:  s.session.ep,
		Accepted: s.session.sess.Accepted,
		Rejected: s.session.sess.Rejected,
		Offsets:  s.session.sess.Offsets(),
	}
	if s.session.show != nil {
		st.Show = s.session.show.CanonicalName
		st.ShowID = s.session.show.ID
	}

	// A generous list: nothing is filtered for looking confident, so the user
	// can work down it as far as they like.
	for i, c := range s.session.sess.Propose(s.session.items, 25) {
		st.Candidates = append(st.Candidates, s.candidateJSON(i, c))
	}
	for i, c := range s.session.sess.Resolved(s.session.items, 5) {
		st.Resolved = append(st.Resolved, s.candidateJSON(i, c))
	}
	return st
}

func (s *Server) candidateJSON(i int, c train.Candidate) candidateJSON {
	r := release.Parse(c.Item.Title)
	return candidateJSON{
		Index:       i,
		Title:       c.Item.Title,
		Episode:     c.Episode,
		Seeders:     c.Item.Seeders,
		Size:        c.Item.Size,
		Resolution:  r.Resolution,
		Codec:       r.Codec,
		Group:       r.Group,
		Why:         c.Why,
		Uncertainty: c.Uncertainty,
		Novelty:     c.Novelty,
		Unseen:      c.Unseen,
		Attrs:       train.Inspect(c.Item.Title, c.Episode).Attrs,
	}
}

func (s *Server) handleTrainState(w http.ResponseWriter, r *http.Request) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if !s.session.active {
		writeJSON(w, trainStateJSON{Active: false})
		return
	}
	writeJSON(w, s.trainStateLocked())
}

func (s *Server) handleTrainCommit(w http.ResponseWriter, r *http.Request) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if !s.session.active {
		writeErr(w, http.StatusConflict, fmt.Errorf("no active training session"))
		return
	}
	if err := s.session.sess.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Verify the learned model against the live feed, so the user can see
	// whether training actually worked before moving on.
	verified := s.verify(s.session.show, s.session.items)

	s.session.active = false
	writeJSON(w, map[string]any{
		"committed": true,
		"accepted":   s.session.sess.Accepted,
		"rejected":   s.session.sess.Rejected,
		"verified":   verified,
	})
}

// handleTrainReset clears a show's learned offsets so training can start over.
func (s *Server) handleTrainReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowID int64 `json:"show_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.ClearGroupOffsets(req.ShowID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Also drop any in-flight session for this show, or its stale in-memory
	// offsets would be committed later and undo the reset.
	s.session.mu.Lock()
	if s.session.active && s.session.show != nil && s.session.show.ID == req.ShowID {
		s.session.active = false
	}
	s.session.mu.Unlock()
	writeJSON(w, map[string]any{"reset": true, "show_id": req.ShowID})
}

// handleTrainInspect resolves a pasted link or title into gradable attributes.
func (s *Server) handleTrainInspect(w http.ResponseWriter, r *http.Request) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if !s.session.active {
		writeErr(w, http.StatusConflict, fmt.Errorf("no active training session"))
		return
	}
	var req struct {
		Input   string `json:"input"`
		Episode int    `json:"episode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	input := strings.TrimSpace(req.Input)
	if input == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("paste a Nyaa link or release title"))
		return
	}

	title := input
	if nyaa.IsLink(input) {
		var err error
		title, err = nyaa.ResolveLink(nil, input)
		if err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Errorf("could not read that link: %w", err))
			return
		}
	}

	ep := req.Episode
	if ep == 0 {
		ep = s.session.ep
	}
	res := match.Match(s.session.sess.Show(), title)
	resolved := 0
	if res.Matched {
		resolved = res.Episode
	}

	// Fill any field the parser missed using the learned vocabulary, so the
	// panel shows what kishizu will actually see once taught — not what the
	// dumb parser sees in isolation.
	parsed := release.Parse(title)
	s.vocab.ApplyVocabulary(&parsed)

	writeJSON(w, map[string]any{
		"release": train.InspectWithConfidence(title, resolved, res.Confidence),
		"parsed":  parsed,
		"episode": ep,
		"matched": res.Matched,
		"why":     res.Reason,
	})
}

// handleTrainVocab records that a title token means a canonical value.
//
// This is the point of training. Offsets are per-group and saturate after one
// example; vocabulary compounds — one correction makes every future release
// using that spelling readable, for every group and every show.
func (s *Server) handleTrainVocab(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind      string `json:"kind"`      // resolution | codec | source | service | audio
		Token     string `json:"token"`     // as written in the title
		Canonical string `json:"canonical"` // what kishizu calls it
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Token = strings.TrimSpace(req.Token)
	req.Canonical = strings.TrimSpace(req.Canonical)
	if req.Kind == "" || req.Token == "" || req.Canonical == "" {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("kind, token and canonical are all required"))
		return
	}
	if err := s.st.LearnVocabulary(req.Kind, req.Token, req.Canonical); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.vocab.Learn(req.Kind, req.Token, req.Canonical)
	log.Printf("vocabulary: %s %q -> %q", req.Kind, req.Token, req.Canonical)
	writeJSON(w, map[string]any{"learned": req.Token + " -> " + req.Canonical})
}

// handleTrainGrade applies per-attribute verdicts.
func (s *Server) handleTrainGrade(w http.ResponseWriter, r *http.Request) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if !s.session.active {
		writeErr(w, http.StatusConflict, fmt.Errorf("no active training session"))
		return
	}
	var req struct {
		Title   string               `json:"title"`
		Episode int                  `json:"episode"`
		Grades  map[string]string    `json:"grades"`
		Release *train.GradedRelease `json:"release"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ep := req.Episode
	if ep == 0 {
		ep = s.session.ep
	}

	res := match.Match(s.session.sess.Show(), req.Title)
	resolved := 0
	if res.Matched {
		resolved = res.Episode
	}
	g := train.InspectWithConfidence(req.Title, resolved, res.Confidence)
	if req.Release != nil {
		g = *req.Release
	}

	grades := make(map[train.Attribute]train.Grade, len(req.Grades))
	for k, v := range req.Grades {
		gr, err := train.ParseGrade(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		grades[train.Attribute(k)] = gr
	}

	notes, err := s.session.sess.ApplyGrades(g, grades, ep)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if grades[train.AttrEpisode] == train.GradeGood {
		s.session.sess.MarkAsked(req.Title)
	}

	writeJSON(w, map[string]any{
		"notes": notes,
		"state": s.trainStateLocked(),
	})
}

// handleTrainAcceptAll grades every present attribute of a release in one
// request, so an obviously-correct release costs one click instead of one per
// attribute.
//
// Two modes, because "the parse is right" and "I want this" are different
// claims and must not be collapsed:
//
//	parse      — mark every present attribute good. Confirms extraction only.
//	preferred  — the same, plus mark the quality attributes preferred.
//
// The second is the true one-click for a release that is both correctly parsed
// and wanted. The first exists so confirming a parse never silently records a
// preference the user did not choose.
//
// Absent attributes are skipped either way: there is nothing to confirm about a
// value the title does not contain.
func (s *Server) handleTrainAcceptAll(w http.ResponseWriter, r *http.Request) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if !s.session.active {
		writeErr(w, http.StatusConflict, fmt.Errorf("no active training session"))
		return
	}
	var req struct {
		Title  string `json:"title"`
		Mode   string `json:"mode"` // "parse" (default) or "preferred"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("title is required"))
		return
	}

	res := match.Match(s.session.sess.Show(), req.Title)
	resolved := 0
	if res.Matched {
		resolved = res.Episode
	}

	// Refuse when no episode could be read. A quick accept without one teaches
	// nothing: the offset comes from the episode attribute, so "good" on a
	// release with no number is a no-op that reports success. Those belong in
	// the detail view, where the user supplies the number themselves.
	if resolved <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf(
			"no episode number readable from this title, so there is nothing to match — grade it in detail instead"))
		return
	}
	g := train.InspectWithConfidence(req.Title, resolved, res.Confidence)

	prefer := strings.EqualFold(req.Mode, "preferred")
	grades := make(map[train.Attribute]train.Grade, len(g.Attrs))
	for _, a := range g.Attrs {
		if !a.Present {
			continue
		}
		switch a.Key {
		case train.AttrEpisode, train.AttrGroup:
			// The episode teaches the offset; the group is what the offset is
			// keyed on. Both are always "good" here — a quick accept is a
			// statement that the parse is right.
			grades[a.Key] = train.GradeGood
		default:
			if prefer {
				grades[a.Key] = train.GradeGood
			} else {
				// Acceptable rather than good: the parse is confirmed, but no
				// preference is claimed.
				grades[a.Key] = train.GradeAcceptable
			}
		}
	}

	notes, err := s.session.sess.ApplyGrades(g, grades, resolved)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.session.sess.MarkAsked(req.Title)

	writeJSON(w, map[string]any{
		"notes": notes,
		"state": s.trainStateLocked(),
	})
}

type verifiedJSON struct {
	Title   string `json:"title"`
	Episode int    `json:"episode"`
	Seeders int    `json:"seeders"`
}

// verify re-matches the feed with the committed model and returns what would
// now be grabbed for the trained episode.
func (s *Server) verify(sh *store.Show, items []nyaa.Item) []verifiedJSON {
	m, err := s.st.NewMatcher(sh)
	if err != nil {
		return nil
	}
	var out []verifiedJSON
	for _, it := range items {
		res := match.Match(m, it.Title)
		if res.Matched && res.Episode == s.session.ep {
			out = append(out, verifiedJSON{Title: it.Title, Episode: res.Episode, Seeders: it.Seeders})
		}
	}
	return out
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

