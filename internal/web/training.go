package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
)

// ---------- training ----------

// session is the in-flight training state. One at a time is fine: training is
// a human-driven, single-user activity.
var session struct {
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
// results of an alias search and the user picks one to grade; the episode comes
// from the release they choose.
//
// No seeding, deliberately. Seeding from a "known good" release teaches only
// what to accept — it can never show what to reject, and rejection is most of
// what the matcher does. A raw alias search is an unbiased sample: some right,
// some wrong, some unreadable. Grading across that gives both signals.
//
// There is also no paste-a-link path. If a release does not appear in the
// alias-derived results, the alias set does not match it — and if it does not
// match during training it will not match during hunting either. The fix is to
// correct the alias, not to route around it.
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
	// the user actually grades.
	ep := nextUnwatched(s.st, sh)

	sess, err := train.NewSession(s.st, sh, ep)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	session.active = true
	session.sess = sess
	session.show = sh
	session.items = items
	session.ep = ep

	writeJSON(w, s.trainState())
}

type trainStateJSON struct {
	Active     bool            `json:"active"`
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
}

func (s *Server) trainState() trainStateJSON {
	st := trainStateJSON{
		Active:   session.active,
		Episode:  session.ep,
		Accepted: session.sess.Accepted,
		Rejected: session.sess.Rejected,
		Offsets:  session.sess.Offsets(),
	}
	if session.show != nil {
		st.Show = session.show.CanonicalName
	}

	// A generous list: nothing is filtered for looking confident, so the user
	// can work down it as far as they like.
	for i, c := range session.sess.Propose(session.items, 25) {
		st.Candidates = append(st.Candidates, s.candidateJSON(i, c))
	}
	for i, c := range session.sess.Resolved(session.items, 5) {
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
	}
}

func (s *Server) handleTrainState(w http.ResponseWriter, r *http.Request) {
	if !session.active {
		writeJSON(w, trainStateJSON{Active: false})
		return
	}
	writeJSON(w, s.trainState())
}

type answerRequest struct {
	Index  int    `json:"index"`
	Accept bool   `json:"accept"`
	Reason string `json:"reason"`
}

func (s *Server) handleTrainAnswer(w http.ResponseWriter, r *http.Request) {
	if !session.active {
		writeErr(w, http.StatusConflict, fmt.Errorf("no active training session"))
		return
	}

	var req answerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	cands := session.sess.Propose(session.items, 3)
	if req.Index < 0 || req.Index >= len(cands) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("index %d out of range", req.Index))
		return
	}
	c := cands[req.Index]

	if req.Accept {
		if err := session.sess.Accept(c); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	} else {
		reason := train.Reason(req.Reason)
		if reason == "" {
			reason = train.ReasonOther
		}
		if err := session.sess.Reject(c, reason); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, s.trainState())
}

func (s *Server) handleTrainCommit(w http.ResponseWriter, r *http.Request) {
	if !session.active {
		writeErr(w, http.StatusConflict, fmt.Errorf("no active training session"))
		return
	}
	if err := session.sess.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Verify the learned model against the live feed, so the user can see
	// whether training actually worked before moving on.
	verified := s.verify(session.show, session.items)

	session.active = false
	writeJSON(w, map[string]any{
		"committed": true,
		"accepted":  session.sess.Accepted,
		"rejected":  session.sess.Rejected,
		"verified":  verified,
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
	if session.active && session.show != nil && session.show.ID == req.ShowID {
		session.active = false
	}
	writeJSON(w, map[string]any{"reset": true, "show_id": req.ShowID})
}

// handleTrainTeach records a known-good example the user typed in directly.
func (s *Server) handleTrainTeach(w http.ResponseWriter, r *http.Request) {
	if !session.active {
		writeErr(w, http.StatusConflict, fmt.Errorf("no active training session"))
		return
	}
	var req struct {
		Title   string `json:"title"`
		Episode int    `json:"episode"`
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
	if req.Episode == 0 {
		req.Episode = session.ep
	}
	if err := session.sess.Teach(req.Title, req.Episode); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, s.trainState())
}

// handleTrainInspect resolves a pasted link or title into gradable attributes.
func (s *Server) handleTrainInspect(w http.ResponseWriter, r *http.Request) {
	if !session.active {
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
		ep = session.ep
	}
	res := match.Match(session.sess.Show(), title)
	resolved := 0
	if res.Matched {
		resolved = res.Episode
	}

	writeJSON(w, map[string]any{
		"release": train.InspectWithConfidence(title, resolved, res.Confidence),
		"episode": ep,
		"matched": res.Matched,
		"why":     res.Reason,
	})
}

// handleTrainGrade applies per-attribute verdicts.
func (s *Server) handleTrainGrade(w http.ResponseWriter, r *http.Request) {
	if !session.active {
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
		ep = session.ep
	}

	res := match.Match(session.sess.Show(), req.Title)
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

	notes, err := session.sess.ApplyGrades(g, grades, ep)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if grades[train.AttrEpisode] == train.GradeGood {
		session.sess.MarkAsked(req.Title)
	}

	writeJSON(w, map[string]any{
		"notes": notes,
		"state": s.trainState(),
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
	if !session.active {
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

	res := match.Match(session.sess.Show(), req.Title)
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

	notes, err := session.sess.ApplyGrades(g, grades, resolved)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	session.sess.MarkAsked(req.Title)

	writeJSON(w, map[string]any{
		"notes": notes,
		"state": s.trainState(),
	})
}

type verifiedJSON struct {
	Title   string `json:"title"`
	Episode int    `json:"episode"`
	Seeders int    `json:"seeders"`
}

// releaseSummaries describes feed items so the UI can ask the user to pick a
// seed when we cannot determine one safely.
func releaseSummaries(items []nyaa.Item, limit int) []candidateJSON {
	var out []candidateJSON
	for i, it := range items {
		if i >= limit {
			break
		}
		r := release.Parse(it.Title)
		out = append(out, candidateJSON{
			Index:      i,
			Title:      it.Title,
			Episode:    r.RawEpisode(),
			Seeders:    it.Seeders,
			Size:       it.Size,
			Resolution: r.Resolution,
			Codec:      r.Codec,
			Group:      r.Group,
		})
	}
	return out
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
		if res.Matched && res.Episode == session.ep {
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

// humanSize is a small helper for templates.
func humanSize(s string) string { return s }

var _ = strconv.Itoa
var _ = strings.TrimSpace
var _ = time.Now
var _ = humanSize
