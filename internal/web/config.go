package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/groups"
	"github.com/Ebonhawk3829/kishizu/internal/naming"
	"github.com/Ebonhawk3829/kishizu/internal/notify"
	"github.com/Ebonhawk3829/kishizu/internal/quality"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
)

// The settings endpoints: reading the deployment configuration, writing it
// back, and the one user preference that is not deployment configuration.
//
// Split out of server.go because this group is self-contained: it touches
// configPath and the store, and nothing else on Server. The rest of
// server.go's handlers share the store with several other dependencies, so
// there is no comparable seam to cut — moving them would just relocate the
// same coupling to a file with a different name.

// SetConfigPath sets where the configuration file lives. Empty means
// configuration cannot be read or written from the UI.
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
	writeJSON(w, s.configView(cfg.Server))
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

	// The UI edits a curated subset of the configuration; the rest is yaml-only
	// by design (indexer internals, quality tuning). The form does not render
	// those fields, so the browser sends them empty — and an empty value here
	// means "not in this request", not "clear it". Restoring from the current
	// file keeps a hand-edited yaml value alive across UI saves; without this,
	// the first Save after the UI stopped rendering a field would silently
	// blank it.
	if req.Indexer == (config.IndexerConfig{}) {
		req.Indexer = cur.Server.Indexer
	}
	// Quality contains slices, so it cannot be compared with ==. "Untouched"
	// means every field at its zero value: the UI sends the whole object, but
	// omits these keys entirely once they leave the form.
	if req.Quality.ResolutionFloor == "" && len(req.Quality.GroupOrder) == 0 &&
		len(req.Quality.RejectGroups) == 0 &&
		len(req.Quality.CodecRank) == 0 && len(req.Quality.ResolutionPenalty) == 0 &&
		req.Quality.PenaltyDub == nil && req.Quality.PenaltyUncensored == nil &&
		req.Quality.RejectBatch == nil {
		req.Quality = cur.Server.Quality
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

// handleBrowseTitle saves just the browse display preference.
//
// A separate endpoint rather than reusing the full settings save, because the
// toggle is a click-anywhere control: persisting it should be one small write,
// not a round-trip of every setting with the secrets masked and unmasked.
//
// It writes to the database, not the config file. A display preference is
// not deployment configuration: it is per-user, the UI owns it, and the
// database is already there. The config file is the wrong home — the UI
// would have to read-modify-write a file the operator also edits by hand,
// with no locking, and the server re-reading the file on every list request
// would re-assert stale values and reset the control.
func (s *Server) handleBrowseTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pref, err := schedule.ParseBrowseTitle(req.Title)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.SetPreference(prefKeyBrowseTitle, string(pref)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"saved": true, "title": string(pref)})
}

// prefKeyBrowseTitle is the preference key for the browse list's display name.
const prefKeyBrowseTitle = "browse.title"

// browseTitle reads the browse display preference, falling back to the
// configured default and then to romaji.
//
// The config file is still consulted, but only as a default for a preference
// the user has never set. That keeps an existing deployment's configured
// choice working without the file being the runtime state.
func (s *Server) browseTitle() schedule.BrowseTitle {
	if v, found, err := s.st.Preference(prefKeyBrowseTitle); err == nil && found {
		if p, perr := schedule.ParseBrowseTitle(v); perr == nil {
			return p
		}
	}
	if s.configPath != "" {
		if cfg, cerr := config.Load(s.configPath); cerr == nil {
			if p, perr := schedule.ParseBrowseTitle(cfg.Server.Browse.Title); perr == nil {
				return p
			}
		}
	}
	return schedule.BrowseRomaji
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
	if _, err := schedule.ParseBrowseTitle(s.Browse.Title); err != nil {
		return err
	}
	return nil
}

// configView is the configuration as the UI sees it: secrets masked, and the
// choices offered so the form can render them without a second request.
//
// browse.title is the effective preference rather than the file's value: the
// file is only the default now, and showing it would render a control that
// disagrees with what the list is actually displaying.
func (s *Server) configView(cfg *config.Server) map[string]any {
	qbitFromEnv, gotifyFromEnv := secretFromEnv(cfg)
	view := map[string]any{
		"library":      cfg.Library,
		"staging":      cfg.Staging,
		"interval":     cfg.Interval,
		"dry_run":      boolVal(cfg.DryRun),
		"keep":         intVal(cfg.Keep),
		"delete":       cfg.Delete,
		"delete_after": cfg.DeleteAfter,
		"downloader": map[string]any{
			"kind":             cfg.Downloader.Kind,
			"transmission_rpc": cfg.Downloader.TransmissionRPC,
			"qbittorrent_url":  cfg.Downloader.QBittorrentURL,
			"qbittorrent_user": cfg.Downloader.QBittorrentUser,
			// Masked: the UI never needs to show a credential.
			"qbittorrent_pass": mask(cfg.Downloader.QBittorrentPass),
			// Set by environment variable, so the field is read-only and the
			// value is never written back to the file.
			"qbittorrent_pass_env": qbitFromEnv,
		},
		"notifier": map[string]any{
			"kind":             cfg.Notifier.Kind,
			"ntfy_topic":       cfg.Notifier.NtfyTopic,
			"gotify_url":       cfg.Notifier.GotifyURL,
			"gotify_token":     mask(cfg.Notifier.GotifyToken),
			"gotify_token_env": gotifyFromEnv,
		},
		"indexer": map[string]any{
			"base":         cfg.Indexer.Base,
			"category":     cfg.Indexer.Category,
			"user_agent":   cfg.Indexer.UserAgent,
			"min_interval": cfg.Indexer.MinInterval,
		},
		"quality": map[string]any{
			"resolution_floor": cfg.Quality.ResolutionFloor,
			"group_order":      cfg.Quality.GroupOrder,
			"reject_groups":    cfg.Quality.RejectGroups,
		},
		"naming": map[string]any{
			"preset":        cfg.Naming.Preset,
			"pattern":       cfg.Naming.Pattern,
			"season_folder": boolVal(cfg.Naming.SeasonFolder),
		},
		"browse": map[string]any{
			"title": string(s.browseTitle()),
		},
		"choices": map[string]any{
			"downloaders":   download.Kinds,
			"notifiers":     notify.Kinds,
			"presets":       naming.Presets,
			"browse_titles": schedule.BrowseTitles,
			"resolutions":   quality.Resolutions,
			// The researched baseline for the group-order editor: the
			// user's own list replaces it once they have one.
			"group_baseline": groups.Baseline,
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
