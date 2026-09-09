// Package web serves the single-page UI and the JSON API behind it.
//
// Deliberately standard-library only: net/http plus html/template, the same
// shape as dig. No framework, no build step, no npm.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server holds the dependencies the handlers need.
type Server struct {
	st   *store.Store
	tmpl *template.Template
}

// New builds a Server and parses templates.
func New(st *store.Store) (*Server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Server{st: st, tmpl: tmpl}, nil
}

// Handler returns the routed mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /shows", s.handleListShows)

	// Training API.
	mux.HandleFunc("POST /api/train/start", s.handleTrainStart)
	mux.HandleFunc("GET /api/train/state", s.handleTrainState)
	mux.HandleFunc("POST /api/train/answer", s.handleTrainAnswer)
	mux.HandleFunc("POST /api/train/commit", s.handleTrainCommit)
	mux.HandleFunc("POST /api/train/reset", s.handleTrainReset)
	mux.HandleFunc("POST /api/train/teach", s.handleTrainTeach)
	mux.HandleFunc("POST /api/train/inspect", s.handleTrainInspect)
	mux.HandleFunc("POST /api/train/grade", s.handleTrainGrade)

	// Session-free grading: grade a release without starting a training run.
	mux.HandleFunc("POST /api/inspect", s.handleInspect)
	mux.HandleFunc("POST /api/grade", s.handleGrade)

	return mux
}

// resolveTitle turns a pasted link or title into a release title.
func resolveTitle(input string) (string, error) {
	if !nyaa.IsLink(input) {
		return input, nil
	}
	return nyaa.ResolveLink(nil, input)
}

// handleInspect breaks a release into gradable attributes for a given show,
// without needing an active training session.
func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input   string `json:"input"`
		ShowID  int64  `json:"show_id"`
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
	title, err := resolveTitle(input)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("could not read that link: %w", err))
		return
	}

	sh, err := s.st.GetShow(req.ShowID)
	if err != nil || sh == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("show %d not found", req.ShowID))
		return
	}

	m, err := s.st.NewMatcher(sh)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	res := match.Match(m, title)
	resolved := 0
	if res.Matched {
		resolved = res.Episode
	}
	writeJSON(w, map[string]any{
		"release": train.Inspect(title, resolved),
		"episode": req.Episode,
		"matched": res.Matched,
		"why":     res.Reason,
	})
}

// handleGrade applies per-attribute verdicts straight to the store, so a
// release can be graded without a training session in flight.
func (s *Server) handleGrade(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title   string               `json:"title"`
		ShowID  int64                `json:"show_id"`
		Episode int                  `json:"episode"`
		Grades  map[string]string    `json:"grades"`
		Release *train.GradedRelease `json:"release"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sh, err := s.st.GetShow(req.ShowID)
	if err != nil || sh == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("show %d not found", req.ShowID))
		return
	}

	grades := make(map[train.Attribute]train.Grade, len(req.Grades))
	for k, v := range req.Grades {
		g, err := train.ParseGrade(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		grades[train.Attribute(k)] = g
	}

	// A short-lived session gives us the same learning rules as the interactive
	// flow, without requiring one to be open.
	sess, err := train.NewSession(s.st, sh, req.Episode)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	m, err := s.st.NewMatcher(sh)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	res := match.Match(m, req.Title)
	resolved := 0
	if res.Matched {
		resolved = res.Episode
	}
	g := train.Inspect(req.Title, resolved)
	if req.Release != nil {
		g = *req.Release
	}

	notes, err := sess.ApplyGrades(g, grades, req.Episode)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := sess.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"notes": notes})
}

// ListenAndServe starts the server.
func (s *Server) ListenAndServe(addr string) error {
	log.Printf("kishizu listening on %s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

// ---------- pages ----------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if err := s.tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---------- shows ----------

type showJSON struct {
	ID      int64          `json:"id"`
	Name    string         `json:"name"`
	Next    int            `json:"next"`
	Max     int            `json:"max"`
	Aliases []string       `json:"aliases"`
	Offsets map[string]int `json:"offsets"`
	Trained bool           `json:"trained"`
}

func (s *Server) handleListShows(w http.ResponseWriter, r *http.Request) {
	shows, err := s.st.ListShows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out := make([]showJSON, 0, len(shows))
	for _, sh := range shows {
		next := nextUnwatched(s.st, sh)
		offsets, _ := s.st.GroupOffsets(sh.ID)
		out = append(out, showJSON{
			ID:      sh.ID,
			Name:    sh.CanonicalName,
			Next:    next,
			Max:     sh.MaxEpisode,
			Aliases: sh.Aliases,
			Offsets: offsets,
			Trained: len(offsets) > 0,
		})
	}
	writeJSON(w, out)
}

func nextUnwatched(st *store.Store, sh *store.Show) int {
	eps, err := st.EpisodesForShow(sh.ID)
	if err != nil {
		return 1
	}
	next := 1
	for _, e := range eps {
		if e.Number == next && (e.State == episode.Watched || e.State == episode.Deleted) {
			next++
		}
	}
	return next
}

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
	ShowID    int64 `json:"show_id"`
	Episode   int   `json:"episode"`
	SeedIndex int   `json:"seed_index"`
}

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

	items, err := nyaa.Fetch(nil, nyaa.FeedURL(sh.CanonicalName))
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("fetch feed: %w", err))
		return
	}
	if len(items) == 0 {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no releases found for %q", sh.CanonicalName))
		return
	}

	ep := req.Episode
	if ep == 0 {
		ep = nextUnwatched(s.st, sh)
	}

	sess, err := train.NewSession(s.st, sh, ep)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Seed from the chosen release.
	//
	// When no index is given, do NOT blindly take items[0]: the newest release
	// is often the PREVIOUS episode (the target has not aired yet), and seeding
	// from it teaches a wrong offset. Prefer a release whose raw number equals
	// the target, and fall back to asking the user.
	idx := req.SeedIndex
	if idx < 0 || idx >= len(items) {
		idx = -1
		for i, it := range items {
			if release.Parse(it.Title).RawEpisode() == ep {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		writeJSON(w, map[string]any{
			"needs_seed": true,
			"show":       sh.CanonicalName,
			"episode":    ep,
			"releases":   releaseSummaries(items, 12),
		})
		return
	}
	if err := sess.Seed(items[idx].Title); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("seed: %w", err))
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
	Index       int     `json:"index"`
	Title       string  `json:"title"`
	Episode     int     `json:"episode"`
	Seeders     int     `json:"seeders"`
	Size        string  `json:"size"`
	Resolution  string  `json:"resolution"`
	Codec       string  `json:"codec"`
	Group       string  `json:"group"`
	Why         string  `json:"why"`
	Uncertainty float64 `json:"uncertainty"`
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

	for i, c := range session.sess.Propose(session.items, 3) {
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
		"release": train.Inspect(title, resolved),
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
	g := train.Inspect(req.Title, resolved)
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
