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
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/ntfy"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed img/kishizu_logo_512.png
var logoFS embed.FS

// logoData is the raw PNG, read once at init.
var logoData = func() []byte {
	b, err := logoFS.ReadFile("img/kishizu_logo_512.png")
	if err != nil {
		return nil
	}
	return b
}()

// Server holds the dependencies the handlers need.
type Server struct {
	st    *store.Store
	tmpl  *template.Template
	watch *watch.Handler
	// notifier is optional; nil disables notifications.
	notifier *ntfy.Client
}

// New builds a Server and parses templates.
func New(st *store.Store) (*Server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Server{st: st, tmpl: tmpl}, nil
}

// SetWatch attaches the watch handler. Optional: without it, /api/watched
// still records state but cannot sweep files.
func (s *Server) SetWatch(h *watch.Handler) { s.watch = h }

// SetNotifier attaches the ntfy client for user-visible alerts.
func (s *Server) SetNotifier(c *ntfy.Client) { s.notifier = c }

// Handler returns the routed mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.handleIndex)
	// The logo, embedded so the binary stays self-contained.
	mux.HandleFunc("GET /static/logo.png", func(w http.ResponseWriter, r *http.Request) {
		if len(logoData) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Content-Type", "image/png")
		w.Write(logoData)
	})
	mux.HandleFunc("GET /shows", s.handleListShows)
	// Adding a show from the UI. Seeding from shows.yaml still works and
	// updates aliases for existing shows, so the two paths coexist.
	mux.HandleFunc("POST /api/shows", s.handleAddShow)
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

	// Watch signal from the mpv script, and manual marking from the UI.
	mux.HandleFunc("POST /api/watched", s.handleWatched)
	mux.HandleFunc("POST /api/watched-up-to", s.handleWatchedUpTo)
	// Manual state override, for episodes obtained outside kishizu.
	mux.HandleFunc("POST /api/set-state", s.handleSetState)
	// Deliberate re-download: the only way out of a terminal state.
	mux.HandleFunc("POST /api/unlatch", s.handleUnlatch)

	// Stats for a homepage widget, and debug for troubleshooting.
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/debug", s.handleDebug)
	// Runtime debug toggle, so verbose logging can be switched on during a
	// live run without a restart.
	mux.HandleFunc("POST /api/debug", s.handleDebugToggle)

	return mux
}

// handleSetState forces an episode into a state, bypassing the latch.
//
// For episodes obtained outside kishizu: the user already has files on their
// PC that the tool never downloaded, so those episodes sit in "wanted" and
// would be hunted again. Marking them "downloaded" tells the truth — they are
// on disk, waiting to be watched — and stops the pointless hunting.
//
// The latch is bypassed deliberately: this is a correction, not a lifecycle
// event. But it still refuses to move a terminal episode (watched/deleted)
// backwards, since that would resurrect something already consumed.
func (s *Server) handleSetState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowID  int64  `json:"show_id"`
		Episode int    `json:"episode"`
		State   string `json:"state"`
		Path    string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	want := episode.ParseState(req.State)
	switch want {
	case episode.Wanted, episode.Downloading, episode.Downloaded, episode.Blocked:
	default:
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("state %q cannot be set manually; use /api/watched for watched", req.State))
		return
	}
	if req.ShowID == 0 || req.Episode == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("show_id and episode are required"))
		return
	}

	// Refuse to rewind a terminal episode: that would resurrect something the
	// user already finished.
	if cur, err := s.st.GetEpisode(req.ShowID, req.Episode); err == nil && cur != nil {
		if episode.ParseState(string(cur.State)).Terminal() {
			writeErr(w, http.StatusConflict,
				fmt.Errorf("episode %d is %s (terminal); not overriding", req.Episode, cur.State))
			return
		}
	}

	if err := s.st.SetEpisodeState(req.ShowID, req.Episode, want); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if req.Path != "" {
		if err := s.st.SetFilePath(req.ShowID, req.Episode, req.Path); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	log.Printf("set-state: show %d ep %d -> %s", req.ShowID, req.Episode, want)
	writeJSON(w, map[string]any{
		"show_id": req.ShowID, "episode": req.Episode, "state": string(want),
	})
}

// handleUnlatch resets an episode to wanted so it can be grabbed again.
//
// The "accident, redownload" path: the user deleted a file early, or wants
// something back they already watched. Deliberately user-initiated — no
// automatic path may call this, or the resurrection guard is void.
func (s *Server) handleUnlatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowID  int64 `json:"show_id"`
		Episode int   `json:"episode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.ShowID == 0 || req.Episode == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("show_id and episode are required"))
		return
	}
	if err := s.st.Unlatch(req.ShowID, req.Episode); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("unlatch: show %d ep %d -> wanted", req.ShowID, req.Episode)
	writeJSON(w, map[string]any{
		"show_id": req.ShowID, "episode": req.Episode, "state": "wanted",
	})
}

// handleWatchedUpTo latches episodes 1..n as watched, for first runs of a
// newly added show. Without it the listener would grab everything from
// episode 1.
func (s *Server) handleWatchedUpTo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowID int64 `json:"show_id"`
		UpTo   int   `json:"up_to"`
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
	if req.UpTo < 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("up_to must be >= 0"))
		return
	}
	marked, err := s.st.MarkWatchedUpTo(sh.ID, req.UpTo)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Same contract as the single-episode path: watching is progress, so
	// the schedule pointer must move with it. Best-effort for the same
	// reason - the daily refresh is the backstop.
	if err := s.st.AdvanceSchedule(sh.ID, req.UpTo); err != nil {
		log.Printf("watched-up-to: advance schedule: %v", err)
	}
	if err := s.st.ProjectAirDates(sh.ID); err != nil {
		log.Printf("watched-up-to: project air dates: %v", err)
	}
	log.Printf("watched-up-to: %s episodes 1..%d (%d newly latched)", sh.CanonicalName, req.UpTo, marked)
	writeJSON(w, map[string]any{
		"show": sh.CanonicalName, "up_to": req.UpTo, "newly_marked": marked,
	})
}

// handleStats summarises episode state for a homepage widget.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	shows, err := s.st.ListShows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type perShow struct {
		Name       string `json:"name"`
		Next       int    `json:"next"`
		Downloaded int    `json:"downloaded"`
		Watched    int    `json:"watched"`
		Deleted    int    `json:"deleted"`
	}
	out := struct {
		Shows       int       `json:"shows"`
		Downloading int       `json:"downloading"`
		Downloaded  int       `json:"downloaded"`
		Watched     int       `json:"watched"`
		Deleted     int       `json:"deleted"`
		PerShow     []perShow `json:"per_show"`
	}{PerShow: []perShow{}}

	for _, sh := range shows {
		eps, err := s.st.EpisodesForShow(sh.ID)
		if err != nil {
			continue
		}
		p := perShow{Name: sh.CanonicalName, Next: nextUnwatched(s.st, sh)}
		for _, ep := range eps {
			switch episode.ParseState(string(ep.State)) {
			case episode.Downloading:
				out.Downloading++
			case episode.Downloaded:
				out.Downloaded++
			case episode.Watched:
				out.Watched++
			case episode.Deleted:
				out.Deleted++
			}
		}
		out.PerShow = append(out.PerShow, p)
	}
	writeJSON(w, out)
}

// handleDebugToggle switches verbose logging on or off at runtime.
func (s *Server) handleDebugToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	debug.Set(req.On)
	log.Printf("debug mode: %v", req.On)
	writeJSON(w, map[string]any{"debug": req.On})
}
func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	shows, err := s.st.ListShows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()

	type epInfo struct {
		Number int     `json:"number"`
		State  string  `json:"state"`
		AirsAt *string `json:"airs_at"`
		Path   string  `json:"path"`
	}
	type showInfo struct {
		Name       string   `json:"name"`
		NextEp     int      `json:"next_ep"`
		NextAirsAt *string  `json:"next_airs_at"`
		Trained    bool     `json:"trained"`
		Due        bool     `json:"due"`
		Interval   string   `json:"poll_interval"`
		Episodes   []epInfo `json:"episodes"`
	}

	out := make([]showInfo, 0, len(shows))
	for _, sh := range shows {
		si := showInfo{Name: sh.CanonicalName, NextEp: nextUnwatched(s.st, sh)}
		if n, at, _ := s.st.NextEpisode(sh.ID); at != nil && n > 0 {
			si.NextEp = n
			formatted := at.Format(time.RFC3339)
			si.NextAirsAt = &formatted
		}
		offsets, _ := s.st.GroupOffsets(sh.ID)
		si.Trained = len(offsets) > 0

		eps, err := s.st.EpisodesForShow(sh.ID)
		if err == nil {
			var states []cycle.State
			for _, ep := range eps {
				states = append(states, cycle.StateOf(ep, now))
				ei := epInfo{Number: ep.Number, State: string(ep.State), Path: ep.FilePath}
				if ep.AirsAt != nil {
					f := ep.AirsAt.Format(time.RFC3339)
					ei.AirsAt = &f
				}
				si.Episodes = append(si.Episodes, ei)
			}
			if d, ok := cycle.PollInterval(states); ok {
				si.Due = true
				si.Interval = d.String()
			}
		}
		out = append(out, si)
	}

	writeJSON(w, map[string]any{
		"debug":  debug.Enabled(),
		"now":    now.Format(time.RFC3339),
		"window": cycle.Window.String(),
		"shows":  out,
		"hint":   "POST /api/debug {\"on\":true} to switch verbose logging on",
	})
}

// handleWatched records a watch signal from the mpv script or the UI.
//
// The filename is matched server-side: the PC sends the raw path, and the
// server — which has the aliases, the per-group offsets and the confidence
// model — decides which show and episode it was. A client-side parse would
// duplicate all of that and drift.
//
// Uncertainty refuses: if the file does not match a tracked show, or the
// episode is ambiguous, nothing is marked. Failure never deletes.
func (s *Server) handleWatched(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Path is the full file path as mpv saw it. Only the base name is used
		// for matching, so the PC's directory layout does not matter.
		Path string `json:"path"`
		// ShowID and Episode are the manual path from the UI. When both are
		// set they take precedence over matching the path.
		ShowID  int64 `json:"show_id"`
		Episode int   `json:"episode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	var showID int64
	var epNum int
	var source string

	if req.ShowID != 0 && req.Episode != 0 {
		// Manual marking: the user said so, no matching needed.
		sh, err := s.st.GetShow(req.ShowID)
		if err != nil || sh == nil {
			writeErr(w, http.StatusNotFound, fmt.Errorf("show %d not found", req.ShowID))
			return
		}
		showID, epNum = sh.ID, req.Episode
		source = "manual"
	} else {
		// mpv path: match the filename against tracked shows.
		base := baseName(req.Path)
		matched, ep, err := s.matchFile(base)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		showID, epNum = matched, ep
		source = "mpv"
	}

	// A file that vanished before the watch signal is worth knowing about:
	// either the deletion was an accident, or the signal is late. Checked
	// before marking, so the episode is still in "downloaded" and detectable.
	if s.watch != nil {
		if missing, err := s.watch.CheckMissing(); err != nil {
			log.Printf("watch: check missing: %v", err)
		} else if len(missing) > 0 {
			log.Printf("watch: %d episode(s) missing from disk", len(missing))
			if s.notifier != nil {
				s.notifier.Send("kishizu: file missing",
					fmt.Sprintf("%d episode(s) vanished before the watch signal", len(missing)),
					ntfy.PriorityHigh)
			}
		}
	}

	if err := s.st.UpsertEpisode(showID, epNum, episode.Watched, "", ""); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Watching is progress just as a download is: if the watch point has
	// reached the schedule's pointer, the pointer must move, or the UI
	// keeps announcing an air date that is already in the past. Best-effort:
	// a failed advance leaves the pointer where it was, and the daily
	// schedule refresh corrects it anyway.
	if err := s.st.AdvanceSchedule(showID, epNum); err != nil {
		log.Printf("watched: advance schedule: %v", err)
	}
	if err := s.st.ProjectAirDates(showID); err != nil {
		log.Printf("watched: project air dates: %v", err)
	}
	if s.watch != nil {
		if deleted, kept, err := s.watch.Sweep(); err != nil {
			log.Printf("watch sweep: %v", err)
		} else if len(deleted) > 0 {
			log.Printf("watch: %d deleted, %d kept", len(deleted), len(kept))
		}
	}

	writeJSON(w, map[string]any{
		"show_id": showID, "episode": epNum, "source": source,
	})
}

// baseName extracts the filename from a path that may come from any OS.
//
// filepath.Base is not enough: the server runs on Linux, and mpv on the user's
// PC sends Windows paths whose separator is backslash. Splitting on both
// separators keeps the matching independent of where mpv ran.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// matchFile matches a filename to a tracked show and episode.
//
// The episode latch makes this idempotent: a duplicate signal cannot rewind a
// watched episode.
func (s *Server) matchFile(base string) (int64, int, error) {
	shows, err := s.st.ListShows()
	if err != nil {
		return 0, 0, err
	}
	for _, sh := range shows {
		m, err := s.st.NewMatcher(sh)
		if err != nil {
			continue
		}
		res := match.Match(m, base)
		if !res.Matched || res.Episode <= 0 {
			continue
		}
		// A filename in kishizu's own library form ("<Show> - E09.mkv") is
		// inherently certain: it was written by this tool on completion, so it
		// needs no confidence gate. Requiring one here blocked every watch
		// signal, since a library name has no group and therefore scores only
		// 0.5 — below the threshold.
		if isLibraryForm(base) {
			return sh.ID, res.Episode, nil
		}
		// Otherwise only act on confident matches. A wrong guess here deletes
		// a file the user may still want, so uncertainty means do nothing.
		if !res.Confident() {
			continue
		}
		return sh.ID, res.Episode, nil
	}
	return 0, 0, fmt.Errorf("no confident match for %q", base)
}

// isLibraryForm reports whether a filename looks like one kishizu wrote:
// "<Show> - E<NN>.<ext>".
func isLibraryForm(name string) bool {
	return release.ReLibrary.MatchString(name)
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
		"release": train.InspectWithConfidence(title, resolved, res.Confidence),
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
	g := train.InspectWithConfidence(req.Title, resolved, res.Confidence)
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

// handleAddShow creates a show from the UI. The canonical name is stored as
// an alias of itself, so matching needs no special case. Max episode 0 means
// the season length is unknown; the cycle then uses a generous window.
func (s *Server) handleAddShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string   `json:"name"`
		Aliases    []string `json:"aliases"`
		MaxEpisode int      `json:"max_episode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	if req.MaxEpisode < 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("max_episode must be >= 0"))
		return
	}
	sh, err := s.st.CreateShow(req.Name, req.Aliases, req.MaxEpisode)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("add-show: %s (max %d, %d aliases)", sh.CanonicalName, req.MaxEpisode, len(req.Aliases))
	writeJSON(w, map[string]any{"id": sh.ID, "name": sh.CanonicalName})
}

type showJSON struct {
	ID      int64          `json:"id"`
	Name    string         `json:"name"`
	Next    int            `json:"next"`
	Max     int            `json:"max"`
	Aliases []string       `json:"aliases"`
	Offsets map[string]int `json:"offsets"`
	Trained bool           `json:"trained"`
	// ImageURL is the season's cover art from the schedule, empty when unknown.
	ImageURL string `json:"image_url"`
	// Cadence is the air weekday, 0 = Sunday. Nil when unknown. Shown in the
	// UI so the user can see whether a show is on air or between episodes.
	Cadence *int `json:"cadence"`
	// NextSchedule is the schedule's authoritative next-episode point: ep N
	// airs at this time. Held until a download confirms the episode.
	NextSchedule *scheduleJSON `json:"next_schedule"`
	// State is the user-facing cycle state of the show's next episode.
	State string `json:"state"`
	// NeedsAttention is true when the state requires user action: no air date,
	// or a window that closed empty.
	NeedsAttention bool `json:"needs_attention"`
	// Counts for the stats display.
	Downloaded int `json:"downloaded"`
	Watched    int `json:"watched"`
	Deleted    int `json:"deleted"`
}

type scheduleJSON struct {
	Episode int    `json:"episode"`
	AirsAt  string `json:"airs_at"`
	// Status is the derived state: "aired" when the time has passed and no
	// download has confirmed it, otherwise "upcoming".
	Status string `json:"status"`
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
		j := showJSON{
			ID:       sh.ID,
			Name:     sh.CanonicalName,
			Next:     next,
			Max:      sh.MaxEpisode,
			Aliases:  sh.Aliases,
			Offsets:  offsets,
			Trained:  len(offsets) > 0,
			Cadence:  sh.CadenceWeekday,
			ImageURL: sh.ImageURL,
		}
		if eps, err := s.st.EpisodesForShow(sh.ID); err == nil {
			var states []cycle.State
			var nextAirs *time.Time
			for _, ep := range eps {
				states = append(states, cycle.StateOf(ep, time.Now()))
				switch episode.ParseState(string(ep.State)) {
				case episode.Downloaded:
					j.Downloaded++
				case episode.Watched:
					j.Watched++
				case episode.Deleted:
					j.Deleted++
				}
				// The air line follows the user's actual position, not the
				// schedule's raw pointer. The pointer only moves on a watch or
				// download event, so it lags behind whenever progress happens
				// outside kishizu; the projected episode rows do not.
				if ep.Number == next && ep.AirsAt != nil {
					t := *ep.AirsAt
					nextAirs = &t
				}
			}
			j.State, j.NeedsAttention = showState(states)
			if nextAirs != nil {
				status := "upcoming"
				if nextAirs.Before(time.Now()) {
					status = "aired"
				}
				j.NextSchedule = &scheduleJSON{
					Episode: next,
					AirsAt:  nextAirs.Format(time.RFC3339),
					Status:  status,
				}
			}
		}
		out = append(out, j)
	}
	writeJSON(w, out)
}

// showState condenses per-episode cycle states into one show state.
//
// The most demanding episode wins: hunting beats ready-to-watch beats
// up-to-date. Any no-release-found episode sets NeedsAttention, since that is
// the state asking the user to look at it.
func showState(states []cycle.State) (string, bool) {
	attention := false
	for _, s := range states {
		if s == cycle.NoReleaseFound {
			attention = true
		}
	}
	// A missing file is the most urgent thing on the card: it needs a decision
	// (re-grab or mark watched) before anything else makes sense.
	for _, s := range states {
		if s == cycle.Missing {
			return string(cycle.Missing), true
		}
	}
	for _, s := range states {
		if s == cycle.Hunting {
			return string(cycle.Hunting), attention
		}
	}
	for _, s := range states {
		if s == cycle.ReadyToWatch {
			return string(cycle.ReadyToWatch), attention
		}
	}
	return string(cycle.UpToDate), attention
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
