// Package web serves the single-page UI and the JSON API behind it.
//
// Deliberately standard-library only: net/http plus html/template, the same
// shape as dig. No framework, no build step, no npm.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/art"
	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/naming"
	"github.com/Ebonhawk3829/kishizu/internal/notify"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/version"
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
	notifier notify.Notifier
	// art caches cover art on disk so the UI does not depend on the
	// schedule's CDN at page-load time. Optional; nil means no art.
	art *art.Cache
	// vocab holds the learned title vocabulary, so a release written in an
	// unexpected spelling still resolves. Never nil after New.
	vocab *release.Vocabulary
	// session is the in-flight training state. Single-flight by design; the
	// mutex makes that real under concurrent requests.
	session trainSession
	// adopt holds what an adoption needs that the store does not have: where
	// the downloader should put the download, where the library is, and how
	// to reach the downloader. Set by SetAdopt; adoption is disabled until
	// then.
	adopt adoptConfig
	// naming is the library layout. It must be the same scheme the
	// reconciler writes with, or the watch signal cannot recognise kishizu's
	// own filenames. Nil means the default layout.
	naming *naming.Scheme
	// timetable caches the seasonal schedule for the browse list. Nil means
	// browsing is unavailable.
	timetable *schedule.Cache
	// configPath is the configuration file, for the settings UI. Empty means
	// configuration cannot be read or written.
	configPath string
}

// adoptConfig is the deployment-specific half of an adoption.
type adoptConfig struct {
	staging    string
	library    string
	downloader download.Downloader
}

// SetAdopt enables adopting finished seasons from the web UI.
//
// Without it the endpoints return an error rather than half-working: an
// adoption that cannot reach the downloader or does not know the library
// root would create episode rows that never resolve.
func (s *Server) SetAdopt(staging, library string, dl download.Downloader) {
	s.adopt = adoptConfig{staging: staging, library: library, downloader: dl}
}

// New builds the server and parses the embedded templates.
func New(st *store.Store) (*Server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	srv := &Server{st: st, tmpl: tmpl, vocab: release.NewVocabulary()}
	// A naming scheme is always present. Nil would mean the watch signal
	// could not recognise kishizu's own filenames, which silently breaks
	// deletion — so the default is installed here rather than left to the
	// caller.
	if sc, err := naming.Resolve(naming.PresetKishizu, "", nil); err == nil {
		srv.naming = sc
	}
	// Seed the vocabulary from the database. A failure here is not fatal: the
	// tool still works, it just reads fewer titles until it is taught again.
	if entries, err := st.Vocabulary(); err == nil {
		srv.vocab.Load(entries)
	} else {
		log.Printf("vocabulary: load: %v", err)
	}
	return srv, nil
}

// SetWatch attaches the watch handler. Optional: without it, /api/watched
// still records state but cannot sweep files.
func (s *Server) SetWatch(h *watch.Handler) { s.watch = h }

// SetNotifier attaches the notifier for user-visible alerts.
func (s *Server) SetNotifier(c notify.Notifier) { s.notifier = c }

// SetArt attaches the cover-art cache. Without it the UI falls back to the
// remote URLs, which works but keeps the CDN dependency.
func (s *Server) SetArt(c *art.Cache) { s.art = c }

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
		// A failed logo write is a broken image in the browser; there is no
		// handler-level recovery worth adding.
		_, _ = w.Write(logoData)
	})
	// Cached cover art, served from disk so the browser never reaches the
	// schedule's CDN.
	if s.art != nil {
		mux.Handle("GET /art/", http.StripPrefix("/art/",
			http.FileServer(http.Dir(s.art.Dir()))))
	}
	mux.HandleFunc("GET /shows", s.handleListShows)
	// Adding a show from the UI. Seeding from kishizu.yaml still works and
	// updates aliases for existing shows, so the two paths coexist.
	mux.HandleFunc("POST /api/shows", s.handleAddShow)
	// Removing a show, for when a season ends or was added by mistake.
	mux.HandleFunc("DELETE /api/shows", s.handleDeleteShow)
	mux.HandleFunc("POST /api/train/start", s.handleTrainStart)
	mux.HandleFunc("GET /api/train/state", s.handleTrainState)
	mux.HandleFunc("POST /api/train/commit", s.handleTrainCommit)
	mux.HandleFunc("POST /api/train/reset", s.handleTrainReset)
	mux.HandleFunc("POST /api/train/vocab", s.handleTrainVocab)
	mux.HandleFunc("POST /api/train/inspect", s.handleTrainInspect)
	mux.HandleFunc("POST /api/train/grade", s.handleTrainGrade)
	mux.HandleFunc("POST /api/train/accept-all", s.handleTrainAcceptAll)

	// Watch signal from the mpv script, and manual marking from the UI.
	mux.HandleFunc("POST /api/watched", s.handleWatched)
	mux.HandleFunc("POST /api/watched-up-to", s.handleWatchedUpTo)
	// Manual state override, for episodes obtained outside kishizu.
	// Adopting a finished season from releases.moe. Preview returns the
	// proposed plan; adopt performs a confirmed one.
	mux.HandleFunc("POST /api/adopt/preview", s.handleAdoptPreview)
	mux.HandleFunc("POST /api/adopt", s.handleAdopt)

	mux.HandleFunc("POST /api/set-state", s.handleSetState)
	// Deliberate re-download: the only way out of a terminal state.
	mux.HandleFunc("POST /api/unlatch", s.handleUnlatch)

	// Stats for a homepage widget, and debug for troubleshooting.
	mux.HandleFunc("GET /api/stats", s.handleStats)
	// Dashboard summary: status plus the next air time. Smaller than /stats
	// and shaped for a widget.
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/debug", s.handleDebug)
	// Runtime debug toggle, so verbose logging can be switched on during a
	// live run without a restart.
	mux.HandleFunc("POST /api/debug", s.handleDebugToggle)

	// Browsing the seasonal timetable, so a show can be picked from a list
	// rather than only added by pasting a URL.
	mux.HandleFunc("GET /api/timetable", s.handleTimetable)

	// Configuration: read the effective settings, and write them back.
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleSaveConfig)

	// Liveness for container orchestration. Separate from /api/summary
	// because a healthcheck must not depend on the database or the network:
	// a slow Nyaa poll should never mark the container unhealthy.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	// Build identity, for bug reports and for checking what a pinned image
	// tag actually contains.
	mux.HandleFunc("GET /api/version", s.handleVersion)

	return mux
}

// handleHealth reports that the process is up.
//
// Deliberately shallow: it checks nothing external. A healthcheck that
// failed when Nyaa was unreachable would restart a container that was
// working correctly, and the whole point of the design is that kishizu keeps
// running when an upstream is down.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// No caching: a healthcheck that reads a stale 200 is worse than useless.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"status": "ok", "version": version.String()})
}

// handleVersion reports the build identity.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"version": version.String(),
		"go":      version.GoVersion(),
	})
}

// handleTimetable serves the seasonal timetable for the browse list.
//
// ?q= filters by substring, ?refresh=1 forces a re-fetch. Filtering happens
// server-side so the same list works from the command line, and so the client
// does not have to hold the whole season to search it.
//
// A show already being tracked is marked, so the browse list does not offer
// something the user is already watching.
func (s *Server) handleTimetable(w http.ResponseWriter, r *http.Request) {
	if s.timetable == nil {
		writeErr(w, http.StatusNotImplemented,
			fmt.Errorf("browsing is not configured on this server"))
		return
	}
	force := r.URL.Query().Get("refresh") == "1"

	var tt *schedule.Timetable
	var err error
	if force {
		tt, err = s.timetable.Refresh(nil)
	} else {
		tt, err = s.timetable.Get(nil)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	// Which of these are already tracked, by slug.
	tracked := map[string]bool{}
	if shows, serr := s.st.ListShows(); serr == nil {
		for _, sh := range shows {
			if sh.Slug != "" {
				tracked[sh.Slug] = true
			}
		}
	}

	entries := tt.Search(r.URL.Query().Get("q"))
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"slug":    e.Slug,
			"title":   e.Title,
			"image":   e.ImageURL,
			"airs_at": airsAtString(e.AirsAt),
			"tracked": tracked[e.Slug],
		})
	}
	writeJSON(w, map[string]any{
		"fetched": tt.Fetched.Format(time.RFC3339),
		"count":   len(out),
		"entries": out,
	})
}

// airsAtString renders an air time for JSON, or "" when there is none.
func airsAtString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// ---------- configuration ----------

// configPath is where the configuration file lives, set by SetConfigPath.
// Empty means configuration cannot be read or written from the UI.
func (s *Server) SetConfigPath(p string) { s.configPath = p }

// handleGetConfig reports the effective configuration.
//
// Secrets are masked. The UI never needs to display a credential, and sending
// one to the browser puts it in the page, in memory, and in any screenshot.
// A masked value that is left untouched is preserved on save.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		writeErr(w, http.StatusNotImplemented,
			fmt.Errorf("no configuration file is configured on this server"))
		return
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, configView(cfg.Server))
}

// handleSaveConfig writes the configuration back to the file.
//
// Secrets are preserved rather than overwritten when the submitted value is
// the mask or empty: the UI cannot know a credential it never displayed, so
// blank must mean "leave it alone" and not "clear it".
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		writeErr(w, http.StatusNotImplemented,
			fmt.Errorf("no configuration file is configured on this server"))
		return
	}
	var req config.Server
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Load the current file so untouched values — including secrets — survive.
	cur, err := config.Load(s.configPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if isMasked(req.Downloader.QBittorrentPass) {
		req.Downloader.QBittorrentPass = cur.Server.Downloader.QBittorrentPass
	}
	if isMasked(req.Notifier.GotifyToken) {
		req.Notifier.GotifyToken = cur.Server.Notifier.GotifyToken
	}
	// A credential supplied by environment variable must not be written into
	// the file. Saving it would defeat the reason for using one.
	qbitFromEnv, gotifyFromEnv := secretFromEnv(cur.Server)
	if qbitFromEnv {
		req.Downloader.QBittorrentPass = ""
	}
	if gotifyFromEnv {
		req.Notifier.GotifyToken = ""
	}

	// Validate before writing: a config that cannot be loaded is worse than
	// one that was never changed, because kishizu would refuse to start.
	if err := validateServer(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := config.Save(s.configPath, cur, &req); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("config: saved %s (restart required for some settings)", s.configPath)
	writeJSON(w, map[string]any{
		"saved":            true,
		"restart_required": true,
	})
}

// SecretMask is what the UI sends back for a credential it did not display.
const SecretMask = "••••••••"

// isMasked reports whether a submitted value is the mask, meaning "unchanged".
func isMasked(s string) bool {
	return s == SecretMask || s == ""
}

// secretFromEnv reports whether a credential came from the environment rather
// than the configuration file.
//
// Such a value must never be written back: the whole point of supplying it by
// environment variable is to keep it out of the file, and saving it would
// silently undo that. It is also never sent to the browser, for the same
// reason any other secret is masked.
func secretFromEnv(s *config.Server) (qbitPass, gotifyToken bool) {
	if s == nil {
		return false, false
	}
	qbitPass = strings.TrimSpace(os.Getenv(config.EnvQBittorrentPass)) != ""
	gotifyToken = strings.TrimSpace(os.Getenv(config.EnvGotifyToken)) != ""
	return qbitPass, gotifyToken
}

// validateServer rejects a configuration kishizu could not run with.
//
// Checked before writing, because a config file that cannot be loaded is
// worse than one that was never edited: kishizu would refuse to start, and
// the user would have to fix it by hand.
func validateServer(s *config.Server) error {
	if s.Library == "" {
		return fmt.Errorf("library is required")
	}
	if s.Staging == "" {
		return fmt.Errorf("staging is required")
	}
	if s.Keep != nil && *s.Keep < 0 {
		return fmt.Errorf("keep must be >= 0")
	}
	if s.Interval != "" {
		if _, err := time.ParseDuration(s.Interval); err != nil {
			return fmt.Errorf("interval %q: %w", s.Interval, err)
		}
	}
	if _, err := download.ParseKind(s.Downloader.Kind); err != nil {
		return err
	}
	if _, err := notify.ParseKind(s.Notifier.Kind); err != nil {
		return err
	}
	if s.Indexer.MinInterval != "" {
		if _, err := time.ParseDuration(s.Indexer.MinInterval); err != nil {
			return fmt.Errorf("indexer.min_interval %q: %w", s.Indexer.MinInterval, err)
		}
	}
	if _, err := naming.ParsePreset(s.Naming.Preset); err != nil {
		return err
	}
	if s.Naming.Preset == string(naming.PresetCustom) {
		if err := naming.ValidatePattern(s.Naming.Pattern); err != nil {
			return err
		}
	}
	return nil
}

// configView is the configuration as the UI sees it: secrets masked, and the
// choices offered so the form can render them without a second request.
func configView(s *config.Server) map[string]any {
	qbitFromEnv, gotifyFromEnv := secretFromEnv(s)
	view := map[string]any{
		"library":  s.Library,
		"staging":  s.Staging,
		"interval": s.Interval,
		"dry_run":  boolVal(s.DryRun),
		"keep":     intVal(s.Keep),
		"downloader": map[string]any{
			"kind":             s.Downloader.Kind,
			"transmission_rpc": s.Downloader.TransmissionRPC,
			"qbittorrent_url":  s.Downloader.QBittorrentURL,
			"qbittorrent_user": s.Downloader.QBittorrentUser,
			// Masked: the UI never needs to show a credential.
			"qbittorrent_pass": mask(s.Downloader.QBittorrentPass),
			// Set by environment variable, so the field is read-only and the
			// value is never written back to the file.
			"qbittorrent_pass_env": qbitFromEnv,
		},
		"notifier": map[string]any{
			"kind":             s.Notifier.Kind,
			"ntfy_topic":       s.Notifier.NtfyTopic,
			"gotify_url":       s.Notifier.GotifyURL,
			"gotify_token":     mask(s.Notifier.GotifyToken),
			"gotify_token_env": gotifyFromEnv,
		},
		"indexer": map[string]any{
			"base":         s.Indexer.Base,
			"category":     s.Indexer.Category,
			"user_agent":   s.Indexer.UserAgent,
			"min_interval": s.Indexer.MinInterval,
		},
		"quality": map[string]any{
			"resolution_floor": s.Quality.ResolutionFloor,
			"group_order":      s.Quality.GroupOrder,
		},
		"naming": map[string]any{
			"preset":        s.Naming.Preset,
			"pattern":       s.Naming.Pattern,
			"season_folder": boolVal(s.Naming.SeasonFolder),
		},
		"choices": map[string]any{
			"downloaders": download.Kinds,
			"notifiers":   notify.Kinds,
			"presets":     naming.Presets,
		},
	}
	return view
}

func mask(s string) string {
	if s == "" {
		return ""
	}
	return SecretMask
}

func boolVal(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

func intVal(i *int) int {
	if i == nil {
		return 0
	}
	return *i
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
		// Force overrides the in-flight guard, for a download that got stuck
		// and will never complete. The UI sets this when the user confirms
		// they want to mark a downloading episode watched anyway.
		Force bool `json:"force"`
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
	marked, err := s.st.MarkWatchedUpTo(sh.ID, req.UpTo, req.Force)
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

// handleSummary answers the two questions worth putting on a dashboard:
// is there anything to watch, and when is the next episode due?
//
// The per-state counts are still available at /api/stats, but they are not
// useful at a glance — "64 watched" says nothing about whether there is
// anything to do. This reduces the show list to a status and a next air
// time, which is what a widget has room for.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	shows, err := s.st.ListShows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	now := time.Now()
	ready, downloading, hunting, missing := 0, 0, 0, 0
	var next struct {
		Name   string
		Ep     int
		AirsAt time.Time
	}

	for _, sh := range shows {
		eps, err := s.st.EpisodesForShow(sh.ID)
		if err != nil {
			continue
		}
		for _, ep := range eps {
			switch cycle.StateOf(ep, now) {
			case cycle.ReadyToWatch:
				ready++
			case cycle.Downloading:
				downloading++
			case cycle.Hunting:
				hunting++
			case cycle.Missing, cycle.NoReleaseFound:
				missing++
			}
		}

		// The soonest future air time across all shows wins.
		n, at, err := s.st.NextEpisode(sh.ID)
		if err != nil || at == nil || n <= 0 {
			continue
		}
		if !at.After(now) {
			continue
		}
		if next.Name == "" || at.Before(next.AirsAt) {
			next.Name, next.Ep, next.AirsAt = sh.CanonicalName, n, *at
		}
	}

	status := "All up to date"
	if missing > 0 {
		status = fmt.Sprintf("%d need attention", missing)
	} else if ready > 0 {
		status = fmt.Sprintf("%d to watch", ready)
	} else if downloading > 0 {
		status = fmt.Sprintf("%d downloading", downloading)
	} else if hunting > 0 {
		status = "Hunting for releases"
	}

	out := map[string]any{
		"status":      status,
		"ready":       ready,
		"downloading": downloading,
		"hunting":     hunting,
		"missing":     missing,
		"upToDate":    ready == 0 && downloading == 0 && hunting == 0 && missing == 0,
		// Reported here as well as on /healthz so a widget can show which
		// build it is talking to without a second request.
		"version": version.String(),
	}
	if next.Name != "" {
		out["next"] = map[string]any{
			"show":    next.Name,
			"episode": next.Ep,
			"airs_at": next.AirsAt.Format(time.RFC3339),
		}
	}
	writeJSON(w, out)
}

// handleStats summarises episode state for a homepage widget.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	shows, err := s.st.ListShows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type perShow struct {
		Name        string `json:"name"`
		Next        int    `json:"next"`
		Downloading int    `json:"downloading"`
		Downloaded  int    `json:"downloaded"`
		Watched     int    `json:"watched"`
		Deleted     int    `json:"deleted"`
	}
	out := struct {
		Shows       int       `json:"shows"`
		Downloading int       `json:"downloading"`
		Downloaded  int       `json:"downloaded"`
		Watched     int       `json:"watched"`
		Deleted     int       `json:"deleted"`
		PerShow     []perShow `json:"per_show"`
	}{PerShow: []perShow{}}

	out.Shows = len(shows)

	for _, sh := range shows {
		eps, err := s.st.EpisodesForShow(sh.ID)
		if err != nil {
			continue
		}
		p := perShow{Name: sh.CanonicalName, Next: s.st.NextUnwatched(sh.ID)}
		for _, ep := range eps {
			switch episode.ParseState(string(ep.State)) {
			case episode.Downloading:
				out.Downloading++
				p.Downloading++
			case episode.Downloaded:
				out.Downloaded++
				p.Downloaded++
			case episode.Watched:
				out.Watched++
				p.Watched++
			case episode.Deleted:
				out.Deleted++
				p.Deleted++
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
		si := showInfo{Name: sh.CanonicalName, NextEp: s.st.NextUnwatched(sh.ID)}
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
				// alert() is not used here because the server has no
				// package-level helper; a failed send is logged by the
				// notifier itself, and the episode stays flagged in the UI.
				if err := s.notifier.Send("kishizu: file missing",
					fmt.Sprintf("%d episode(s) vanished before the watch signal", len(missing)),
					notify.PriorityHigh); err != nil {
					log.Printf("notify: %v", err)
				}
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

// matchFile resolves a filename to a show and episode.
//
// The primary path is exact: kishizu named this file itself when the download
// completed and stored the path, so if the name matches a stored episode, that
// is the answer. No scoring, no inference — the question is already answered.
//
// Only if that fails do we fall back to parsing the name, for files that came
// from outside kishizu. That path stays gated on confidence, because a wrong
// guess here deletes a file the user may still want.
func (s *Server) matchFile(base string) (int64, int, error) {
	if ep, err := s.st.FindByFileName(base); err == nil && ep != nil {
		return ep.ShowID, ep.Number, nil
	}

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
		// A filename in the configured library form is inherently certain: it
		// was written by this tool on completion, so it needs no confidence
		// gate. Requiring one here blocked every watch signal, since a library
		// name has no group and therefore scores only 0.5 — below the
		// threshold.
		//
		// Recognised by the naming scheme rather than a fixed regex, so a
		// custom layout is trusted just as much as the default.
		if s.isLibraryForm(base) {
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
// isLibraryForm reports whether a filename was written by kishizu.
//
// Delegates to the naming scheme, which is the same one that wrote the file.
// A scheme that cannot recognise its own output would leave every watch
// signal to the fuzzy path, where a library name scores too low to pass the
// confidence gate and nothing is ever marked watched.
func (s *Server) isLibraryForm(name string) bool {
	if s.naming == nil {
		return false
	}
	return s.naming.EpisodeFrom(name) > 0
}

// SetNaming attaches the naming scheme, so the watch signal recognises the
// layout the reconciler writes.
func (s *Server) SetNaming(sc *naming.Scheme) { s.naming = sc }

// SetTimetable attaches the seasonal timetable cache, which backs the browse
// list. Without it the browse endpoints report that browsing is unavailable
// rather than returning an empty list that looks like "nothing is airing".
func (s *Server) SetTimetable(c *schedule.Cache) { s.timetable = c }

// ListenAndServe starts the server and blocks until ctx is cancelled.
//
// Timeouts are set explicitly: without them a slow or stuck client holds a
// connection open indefinitely, and the goroutine with it.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("kishizu %s listening on %s", version.String(), addr)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
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
//
// The name field accepts EITHER a plain name or an animeschedule.net URL. A
// URL is the better input: the slug is an exact identity for the show, and
// the page it points at carries the title, the season length and every name
// the season is known by. Those are otherwise typed by hand and go stale.
//
// When the input is a URL, the show's NAME comes from the fetched page — the
// URL itself is never stored as a name. A bare slug is accepted too and
// treated the same way.
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

	// A URL (or bare slug) is the identity-bearing input. Resolve it FIRST so
	// the show is created under its real title, and so enrichment fills in
	// what the user would otherwise have to type.
	slug := schedule.SlugFromURL(req.Name)
	if slug != "" {
		sh, err := s.createShowFromSlug(slug, req.Aliases, req.MaxEpisode)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		log.Printf("add-show: %s (max %d, %d aliases, slug %q)",
			sh.CanonicalName, sh.MaxEpisode, len(sh.Aliases), sh.Slug)
		writeJSON(w, map[string]any{
			"id": sh.ID, "name": sh.CanonicalName, "slug": sh.Slug, "max_episode": sh.MaxEpisode,
		})
		return
	}

	sh, err := s.st.CreateShow(req.Name, req.Aliases, req.MaxEpisode)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("add-show: %s (max %d, %d aliases)",
		sh.CanonicalName, sh.MaxEpisode, len(sh.Aliases))
	writeJSON(w, map[string]any{
		"id": sh.ID, "name": sh.CanonicalName, "max_episode": sh.MaxEpisode,
	})
}

// createShowFromSlug adds a show by its animeschedule identity. The page's
// own title becomes the canonical name; the slug is recorded so the daily
// refresh matches exactly; enrichment fills in season length, aliases and
// art. Best-effort throughout — a partial record is still a usable show.
func (s *Server) createShowFromSlug(slug string, aliases []string, maxEpisode int) (*store.Show, error) {
	info, err := schedule.FetchShow(nil, slug)
	if err != nil {
		return nil, fmt.Errorf("could not read that show's page: %w", err)
	}
	name := info.Title
	if name == "" {
		name = slug
	}
	sh, err := s.st.CreateShow(name, aliases, maxEpisode)
	if err != nil {
		return nil, err
	}
	if err := s.st.SetSlug(sh.ID, slug); err != nil {
		log.Printf("add-show: set slug %s: %v", slug, err)
	} else {
		sh.Slug = slug
	}
	s.enrichFromSchedule(sh)
	return sh, nil
}

// enrichFromSchedule fills a show in from its animeschedule page: the season
// length, every alternative name, and the cover art.
//
// Best-effort throughout. A failure here leaves the show exactly as the user
// typed it, which is still a usable show — the schedule is an enrichment, never
// a dependency. That is the same posture the daily refresh takes.
func (s *Server) enrichFromSchedule(sh *store.Show) {
	if sh.Slug == "" {
		return
	}
	info, err := schedule.FetchShow(nil, sh.Slug)
	if err != nil {
		log.Printf("schedule: fetch %s: %v", sh.Slug, err)
		return
	}

	// The season length is the plausibility bound the matcher uses and the
	// signal that a season has finished. The site knows it; the user usually
	// does not, so most shows sat at 0 (unknown) before this.
	//
	// SeasonLength, not Episodes: a film reports "1", and capping a season at
	// one episode would mark it complete after a single download.
	if n := info.SeasonLength(); n > 0 && n != sh.MaxEpisode {
		if err := s.st.SetMaxEpisode(sh.ID, n); err != nil {
			log.Printf("schedule: set max %s: %v", sh.Slug, err)
		} else {
			sh.MaxEpisode = n
		}
	}

	// Alternative names, tagged with their provenance. Abbreviations are
	// stored too but marked separately: too short to match on, still useful as
	// a Nyaa feed query.
	for _, a := range info.Aliases() {
		if err := s.st.AddAliasFrom(sh.ID, a, "schedule"); err != nil {
			log.Printf("schedule: add alias %q: %v", a, err)
		}
	}

	// The page's Release Time is episode 1's air slot. For a show that has not
	// premiered it is the only air information that exists, so record it as
	// the first episode's air time rather than leaving the show dateless.
	if !info.AirsAt.IsZero() {
		if err := s.st.SetNextEpisode(sh.ID, 1, info.AirsAt); err != nil {
			log.Printf("schedule: set air time %s: %v", sh.Slug, err)
		} else if err := s.st.ProjectAirDates(sh.ID); err != nil {
			log.Printf("schedule: project %s: %v", sh.Slug, err)
		}
	}

	if info.ImageURL != "" {
		if err := s.st.SetImageURL(sh.ID, info.ImageURL); err != nil {
			log.Printf("schedule: set image %s: %v", sh.Slug, err)
		} else {
			sh.ImageURL = info.ImageURL
			if s.art != nil {
				if _, err := s.art.Ensure(info.ImageURL); err != nil {
					log.Printf("art: cache %s: %v", sh.CanonicalName, err)
				}
			}
		}
	}

	// Reload so the response carries the aliases we just added.
	if fresh, err := s.st.GetShow(sh.ID); err == nil && fresh != nil {
		*sh = *fresh
	}
	log.Printf("schedule: enriched %s (max %d, %d aliases)",
		sh.CanonicalName, sh.MaxEpisode, len(sh.Aliases))
}

// handleDeleteShow removes a show and everything learned about it.
//
// Deleting is the only way to drop a season once it has finished, and the
// only undo for one added by mistake. It does not touch files on disk —
// removing a show stops kishizu tracking it, it does not delete episodes.
func (s *Server) handleDeleteShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sh, err := s.st.GetShow(req.ID)
	if err != nil || sh == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("show %d not found", req.ID))
		return
	}
	if err := s.st.DeleteShow(sh.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("delete-show: %s", sh.CanonicalName)
	writeJSON(w, map[string]any{"deleted": sh.CanonicalName})
}

type showJSON struct {
	ID      int64          `json:"id"`
	Name    string         `json:"name"`
	Next    int            `json:"next"`
	Max     int            `json:"max"`
	Aliases []string       `json:"aliases"`
	Offsets map[string]int `json:"offsets"`
	Trained bool           `json:"trained"`
	// Adopted is true for a finished season taken from releases.moe. Such a
	// show is never trained and never polled, so the UI must not offer
	// training or claim it is untrained — both would be noise about a
	// question that does not apply.
	Adopted bool `json:"adopted"`
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
		next := s.st.NextUnwatched(sh.ID)
		offsets, _ := s.st.GroupOffsets(sh.ID)
		j := showJSON{
			ID:       sh.ID,
			Name:     sh.CanonicalName,
			Next:     next,
			Max:      sh.MaxEpisode,
			Aliases:  sh.Aliases,
			Offsets:  offsets,
			Trained:  len(offsets) > 0,
			Adopted:  sh.Source == store.SourceSeaDex,
			Cadence:  sh.CadenceWeekday,
			ImageURL: s.imageFor(sh),
		}
		if eps, err := s.st.EpisodesForShow(sh.ID); err == nil {
			var states []cycle.State
			var nextAirs *time.Time
			var firstAirs *time.Time
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
				if ep.Number == 1 && ep.AirsAt != nil {
					t := *ep.AirsAt
					firstAirs = &t
				}
			}
			// Aired means episode 1 has happened. Until then there is nothing
			// to train on and nothing to hunt for, so the show is simply
			// waiting. An unknown air time counts as not yet aired: the site
			// lists announced-but-unscheduled shows, and we cannot claim an
			// episode exists when we do not know when it would.
			//
			// Episode 1 specifically — not the next unwatched one. Mid-way
			// through a season the next episode is always in the future, which
			// made every airing show read as unaired.
			aired := firstAirs != nil && !firstAirs.After(time.Now())
			j.State, j.NeedsAttention = showState(states, j.Trained, aired, sh.Source == store.SourceSeaDex)
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

// imageFor resolves a show's cover art to a locally cached file when the
// cache has it, so the browser never reaches the schedule's CDN. Falls back
// to the remote URL when the art is not cached yet.
func (s *Server) imageFor(sh *store.Show) string {
	if sh.ImageURL == "" || s.art == nil {
		return sh.ImageURL
	}
	name, err := s.art.Ensure(sh.ImageURL)
	if err != nil || name == "" {
		return sh.ImageURL
	}
	return "/art/" + name
}

// NeedsTraining is the state of a show that has never been trained.
//
// It is not a cycle state — the cycle is about episodes, and this is about the
// show. It is surfaced in the same field because the card has one status line,
// and "up to date" would be a lie: an untrained show is not up to date, it is
// inert. Nothing will ever be downloaded for it until it is trained.
const NeedsTraining = "needs training"

// Upcoming is the state of a show whose first episode has not aired yet.
//
// Training is impossible before there is anything to train on — there are no
// releases for an episode that does not exist. So an unaired show is not
// "needs training", it is simply waiting. The air time may also be unknown
// (the site lists announced-but-unscheduled shows), which is why this reads
// "upcoming" rather than counting down to a date.
const Upcoming = "upcoming"

// Complete is the state of a season adopted from SeaDex: finished, on disk or
// on its way, and never hunted.
//
// Distinct from the airing states because the questions that produce those do
// not apply. An adopted season has no air dates, so "has it aired" is
// meaningless, and it is never trained, so "does it need training" is wrong.
// It gets its own section rather than being filed under Airing, which would
// claim kishizu is watching it week by week.
const Complete = "complete"

// The most demanding episode wins: hunting beats ready-to-watch beats
// up-to-date. Any no-release-found episode sets NeedsAttention, since that is
// the state asking the user to look at it.
//
// trained is passed in rather than derived here because it is a property of the
// show, not of any episode, and the caller already has it. aired reports
// whether episode 1 has happened yet; until it has, there is nothing to train
// on and nothing to hunt for.
// adopted reports that the show came from a finished-season adoption
// rather than the airing schedule. Such a show has no air dates and is never
// trained, so the usual "has it aired yet" and "is it trained" questions do
// not apply: it is already on disk or on its way.
func showState(states []cycle.State, trained, aired, adopted bool) (string, bool) {
	attention := false
	for _, s := range states {
		if s == cycle.NoReleaseFound {
			attention = true
		}
	}
	// Nothing to train on until the first episode exists. Saying "needs
	// training" about a show premiering in four months is asking for something
	// that cannot be done.
	// An adopted season has no air dates and is never trained, so both of the
	// usual gates would mislabel it: "upcoming" claims it has not started, and
	// "needs training" claims it cannot be downloaded. Neither is true — the
	// release was chosen by hand and is already in flight or on disk.
	if adopted {
		for _, s := range states {
			if s == cycle.Missing {
				return string(cycle.Missing), true
			}
		}
		for _, s := range states {
			if s == cycle.Downloading {
				return string(cycle.Downloading), false
			}
		}
		for _, s := range states {
			if s == cycle.ReadyToWatch {
				return string(cycle.ReadyToWatch), false
			}
		}
		// Nothing outstanding: the season is on disk and watched, or was
		// adopted and has already been consumed. Either way it is complete
		// rather than "up to date", which implies a next episode is coming.
		return Complete, false
	}

	if !aired {
		return Upcoming, false
	}
	// An untrained show cannot match a release, so nothing else on the card
	// means anything yet. Say so plainly instead of claiming it is up to date.
	if !trained {
		return NeedsTraining, true
	}
	// A missing file is the most urgent thing on the card: it needs a decision
	// (re-grab or mark watched) before anything else makes sense.
	for _, s := range states {
		if s == cycle.Missing {
			return string(cycle.Missing), true
		}
	}
	// Downloading outranks hunting: the episode is already in flight, so
	// saying "hunting" would claim the listener is still looking for it.
	for _, s := range states {
		if s == cycle.Downloading {
			return string(cycle.Downloading), attention
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
