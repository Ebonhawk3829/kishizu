// Package config loads kishizu's configuration file.
//
// One file holds everything: the server settings and the show list. They are
// together because a deployment is one thing — the paths, the client and the
// shows belong to the same setup, and splitting them means two files to mount
// and two things to get out of sync.
//
// The show list is parsed by hand rather than by the YAML library, for two
// reasons: the shape is flat and fixed, and a hand-written parser can report
// the line number of a mistake. The server section is nested and open-ended,
// so it uses the YAML library — hand-parsing that would be a second parser to
// maintain, and the UI writes it back.
package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Show is one entry in the show list.
type Show struct {
	Name    string
	Aliases []string
	Watched int
	Max     int
}

// File is the whole configuration file.
type File struct {
	// Server is the deployment configuration. Never nil after Load.
	Server *Server
	// Shows is the seed list. Empty is fine: shows can be added from the UI.
	Shows []Show
}

// Server is the deployment half of the configuration.
//
// Every field has a zero value that means "not configured", and the defaults
// fill those in. That is what lets a minimal file work: the user writes what
// they care about and nothing else.
type Server struct {
	// Library is where finished episodes are filed, as kishizu sees it.
	Library string `yaml:"library" json:"library"`
	// Staging is where the downloader puts completed files, as kishizu sees
	// it. Must be on the same filesystem as Library: the final move is a
	// rename, and a rename across mount points fails.
	Staging string `yaml:"staging" json:"staging"`
	// Keep is how many recently watched episodes to leave on disk.
	Keep *int `yaml:"keep" json:"keep"`
	// Delete controls when watched episodes are removed from disk.
	// "immediate" (the default) deletes as soon as a watch signal lands;
	// "after" waits until the episode has been watched for DeleteAfter;
	// "off" never deletes. Anything else fails at load.
	Delete string `yaml:"delete" json:"delete"`
	// DeleteAfter is how long a watched episode stays on disk before the
	// "after" mode may remove it. Accepts a number followed by h for hours
	// or d for days: "48h", "7d", "30d". Minutes and seconds are also
	// valid ("90m", "7200s") but rarely useful. Required when delete is
	// "after"; ignored otherwise.
	DeleteAfter string `yaml:"delete_after" json:"delete_after"`
	// PruneUnselected deletes staged files that were not selected for
	// tracking, once a pack has finished. Off by default: it deletes data.
	PruneUnselected *bool `yaml:"prune_unselected" json:"prune_unselected"`
	// Interval is how often to poll for releases.
	Interval string `yaml:"interval" json:"interval"`
	// DryRun decides but does not download.
	DryRun *bool `yaml:"dry_run" json:"dry_run"`

	// Downloader is which torrent client to use.
	Downloader DownloaderConfig `yaml:"downloader" json:"downloader"`
	// Notifier is which notification backend to use.
	Notifier NotifierConfig `yaml:"notifier" json:"notifier"`
	// Indexer is where releases are searched for.
	Indexer IndexerConfig `yaml:"indexer" json:"indexer"`
	// Quality is the release-quality policy.
	Quality QualityConfig `yaml:"quality" json:"quality"`
	// Naming is how files are named in the library.
	Naming NamingConfig `yaml:"naming" json:"naming"`
	// Browse is how the seasonal browse list is presented.
	Browse BrowseConfig `yaml:"browse" json:"browse"`
}

// DownloaderConfig selects and configures the torrent client.
type DownloaderConfig struct {
	// Kind is "transmission" or "qbittorrent".
	Kind string `yaml:"kind" json:"kind"`
	// TransmissionRPC is the Transmission RPC endpoint.
	TransmissionRPC string `yaml:"transmission_rpc" json:"transmission_rpc"`
	// QBittorrentURL is the qBittorrent WebUI root URL.
	QBittorrentURL string `yaml:"qbittorrent_url" json:"qbittorrent_url"`
	// QBittorrentUser and QBittorrentPass are the WebUI credentials.
	// Optional when the WebUI bypasses auth for localhost.
	QBittorrentUser string `yaml:"qbittorrent_user" json:"qbittorrent_user"`
	QBittorrentPass string `yaml:"qbittorrent_pass" json:"qbittorrent_pass"`
}

// NotifierConfig selects and configures the notification backend.
type NotifierConfig struct {
	// Kind is "ntfy", "gotify" or "none".
	Kind string `yaml:"kind" json:"kind"`
	// NtfyTopic is the full ntfy topic URL.
	NtfyTopic string `yaml:"ntfy_topic" json:"ntfy_topic"`
	// GotifyURL is the Gotify server root.
	GotifyURL string `yaml:"gotify_url" json:"gotify_url"`
	// GotifyToken is a Gotify app token. A secret: never written back to
	// the file by the UI, and never logged.
	GotifyToken string `yaml:"gotify_token" json:"gotify_token"`
}

// IndexerConfig is where releases are searched for.
type IndexerConfig struct {
	// Base is the indexer root, e.g. https://nyaa.si
	Base string `yaml:"base" json:"base"`
	// Category is the indexer's category filter, e.g. 1_2 for
	// anime-english-translated on Nyaa.
	Category string `yaml:"category" json:"category"`
	// UserAgent is sent on every request. Some indexers reject the default
	// Go user agent outright, and a descriptive one lets an admin see who
	// is polling them.
	UserAgent string `yaml:"user_agent" json:"user_agent"`
	// MinInterval is the shortest time between requests to the indexer.
	// Politeness: a public indexer should not be hammered, and a burst that
	// looks like a scraper gets the caller blocked.
	MinInterval string `yaml:"min_interval" json:"min_interval"`
}

// QualityConfig is the release-quality policy.
type QualityConfig struct {
	// ResolutionFloor is the lowest acceptable resolution, e.g. 1080p.
	ResolutionFloor string `yaml:"resolution_floor" json:"resolution_floor"`
	// GroupOrder is the preferred release groups, best first.
	GroupOrder []string `yaml:"group_order" json:"group_order"`
	// CodecRank maps a codec to its rank. Lower is better.
	CodecRank map[string]int `yaml:"codec_rank" json:"codec_rank"`
	// ResolutionPenalty maps a resolution to a rank penalty. Lower is better.
	ResolutionPenalty map[string]int `yaml:"resolution_penalty" json:"resolution_penalty"`
	// PenaltyDub is added when a release is a dub-only encode.
	PenaltyDub *int `yaml:"penalty_dub" json:"penalty_dub"`
	// PenaltyUncensored is added when a release is uncensored. Negative to
	// prefer it.
	PenaltyUncensored *int `yaml:"penalty_uncensored" json:"penalty_uncensored"`
	// RejectBatch excludes batches and season packs.
	RejectBatch *bool `yaml:"reject_batch" json:"reject_batch"`
}

// BrowseConfig is how the seasonal browse list is presented.
type BrowseConfig struct {
	// Title is which name the browse list shows: "romaji" or "english".
	//
	// Display only. The filter always matches both, so switching this never
	// hides a show the user could have found — it changes which of the two
	// names is on screen, not what is searchable.
	Title string `yaml:"title" json:"title"`
}

// NamingConfig is how files are named in the library.
type NamingConfig struct {
	// Preset is a named layout: "kishizu", "sonarr", "plex" or "custom".
	Preset string `yaml:"preset" json:"preset"`
	// Pattern is a custom layout, used when Preset is "custom".
	//
	// Placeholders: {show} {season} {episode} {episode:2} {ext}
	Pattern string `yaml:"pattern" json:"pattern"`
	// SeasonFolder, when true, files into <library>/<Show>/Season 01/.
	// Implied by the sonarr and plex presets.
	SeasonFolder *bool `yaml:"season_folder" json:"season_folder"`
}

// Load reads and parses the configuration file, then applies any secret
// environment variables.
//
// A missing file is not an error: it yields defaults, so kishizu starts with
// no configuration at all. A file that exists but cannot be parsed IS an
// error — silently ignoring a broken config would run with settings the user
// never asked for.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			f := &File{Server: DefaultServer()}
			applySecretEnv(f.Server)
			return f, nil
		}
		return nil, err
	}
	return Parse(b)
}

// Secret environment variables, as an alternative to putting credentials in
// the configuration file.
const (
	// EnvQBittorrentPass overrides downloader.qbittorrent_pass.
	EnvQBittorrentPass = "KISHIZU_QBITTORRENT_PASS"
	// EnvGotifyToken overrides notifier.gotify_token.
	EnvGotifyToken = "KISHIZU_GOTIFY_TOKEN"
)

// applySecretEnv lets an environment variable supply a credential.
//
// The file is still the primary source; an env var only wins when it is set
// and non-empty. That ordering matters: someone who has carefully put a
// secret in the file should not have it silently overridden by a stray
// variable in the environment.
//
// This exists because the configuration file is plain text on disk, and is
// the one file a user is most likely to back up, copy around, or commit by
// mistake. An environment variable keeps the credential out of it entirely.
func applySecretEnv(s *Server) {
	if v := strings.TrimSpace(os.Getenv(EnvQBittorrentPass)); v != "" {
		s.Downloader.QBittorrentPass = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvGotifyToken)); v != "" {
		s.Notifier.GotifyToken = v
	}
}

// Parse reads a configuration file from bytes.
func Parse(b []byte) (*File, error) {
	f := &File{Server: DefaultServer()}

	// The server section is YAML. Unmarshalling into an already-populated
	// struct is what makes partial config work: only the keys present are
	// overwritten, everything else keeps its default.
	//
	// Only the server block is handed to the YAML library, never the whole
	// file. The show list breaks YAML: a show name may contain a colon
	// ("BLEACH: Thousand-Year Blood War"), which YAML reads as a key/value
	// separator. The hand-written parser tolerates that, the library does
	// not — so parsing the whole file would reject every kishizu.yaml that
	// was ever written.
	if block, ok := serverBlock(string(b)); ok {
		var doc struct {
			Server *Server `yaml:"server" json:"server"`
		}
		if err := yaml.Unmarshal([]byte(block), &doc); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
		if doc.Server != nil {
			mergeServer(f.Server, doc.Server)
		}
	}

	shows, err := parseShows(string(b))
	if err != nil {
		return nil, err
	}
	f.Shows = shows
	applySecretEnv(f.Server)
	return f, nil
}

// mergeServer overwrites only the fields that were explicitly set.
//
// Pointers distinguish "set to false" from "not mentioned", which is why the
// optional booleans and ints are pointers. Without that, a file that omitted
// dry_run would turn dry-run off.
func mergeServer(dst, src *Server) {
	if src.Library != "" {
		dst.Library = src.Library
	}
	if src.Staging != "" {
		dst.Staging = src.Staging
	}
	if src.Keep != nil {
		dst.Keep = src.Keep
	}
	if src.Delete != "" {
		dst.Delete = src.Delete
	}
	if src.DeleteAfter != "" {
		dst.DeleteAfter = src.DeleteAfter
	}
	if src.PruneUnselected != nil {
		dst.PruneUnselected = src.PruneUnselected
	}
	if src.Interval != "" {
		dst.Interval = src.Interval
	}
	if src.DryRun != nil {
		dst.DryRun = src.DryRun
	}
	if src.Downloader.Kind != "" {
		dst.Downloader.Kind = src.Downloader.Kind
	}
	if src.Downloader.TransmissionRPC != "" {
		dst.Downloader.TransmissionRPC = src.Downloader.TransmissionRPC
	}
	if src.Downloader.QBittorrentURL != "" {
		dst.Downloader.QBittorrentURL = src.Downloader.QBittorrentURL
	}
	if src.Downloader.QBittorrentUser != "" {
		dst.Downloader.QBittorrentUser = src.Downloader.QBittorrentUser
	}
	if src.Downloader.QBittorrentPass != "" {
		dst.Downloader.QBittorrentPass = src.Downloader.QBittorrentPass
	}
	if src.Notifier.Kind != "" {
		dst.Notifier.Kind = src.Notifier.Kind
	}
	if src.Notifier.NtfyTopic != "" {
		dst.Notifier.NtfyTopic = src.Notifier.NtfyTopic
	}
	if src.Notifier.GotifyURL != "" {
		dst.Notifier.GotifyURL = src.Notifier.GotifyURL
	}
	if src.Notifier.GotifyToken != "" {
		dst.Notifier.GotifyToken = src.Notifier.GotifyToken
	}
	if src.Indexer.Base != "" {
		dst.Indexer.Base = src.Indexer.Base
	}
	if src.Indexer.Category != "" {
		dst.Indexer.Category = src.Indexer.Category
	}
	if src.Indexer.UserAgent != "" {
		dst.Indexer.UserAgent = src.Indexer.UserAgent
	}
	if src.Indexer.MinInterval != "" {
		dst.Indexer.MinInterval = src.Indexer.MinInterval
	}
	if src.Quality.ResolutionFloor != "" {
		dst.Quality.ResolutionFloor = src.Quality.ResolutionFloor
	}
	if len(src.Quality.GroupOrder) > 0 {
		dst.Quality.GroupOrder = src.Quality.GroupOrder
	}
	if len(src.Quality.CodecRank) > 0 {
		dst.Quality.CodecRank = src.Quality.CodecRank
	}
	if len(src.Quality.ResolutionPenalty) > 0 {
		dst.Quality.ResolutionPenalty = src.Quality.ResolutionPenalty
	}
	if src.Quality.PenaltyDub != nil {
		dst.Quality.PenaltyDub = src.Quality.PenaltyDub
	}
	if src.Quality.PenaltyUncensored != nil {
		dst.Quality.PenaltyUncensored = src.Quality.PenaltyUncensored
	}
	if src.Quality.RejectBatch != nil {
		dst.Quality.RejectBatch = src.Quality.RejectBatch
	}
	if src.Naming.Preset != "" {
		dst.Naming.Preset = src.Naming.Preset
	}
	if src.Naming.Pattern != "" {
		dst.Naming.Pattern = src.Naming.Pattern
	}
	if src.Naming.SeasonFolder != nil {
		dst.Naming.SeasonFolder = src.Naming.SeasonFolder
	}
	if src.Browse.Title != "" {
		dst.Browse.Title = src.Browse.Title
	}
}

// DeleteAfterDuration parses the delete_after setting. It accepts a number
// followed by h (hours) or d (days) — "48h", "7d", "30d" — plus Go's own
// suffixes (m for minutes, s for seconds) for anyone who wants them. Days
// need their own suffix because time.ParseDuration has none: a month of
// "720h" is unreadable.
//
// An empty setting is 0. A malformed value is an error, not a silent default:
// a typo in a deletion delay is exactly the kind of thing that must fail
// loudly at startup.
func (s *Server) DeleteAfterDuration() (time.Duration, error) {
	v := strings.TrimSpace(s.DeleteAfter)
	if v == "" {
		return 0, nil
	}
	// Days: convert to hours and let ParseDuration do the rest.
	if d := strings.TrimSuffix(v, "d"); d != v && d != "" {
		n, err := strconv.ParseFloat(d, 64)
		if err != nil {
			return 0, fmt.Errorf("delete_after: bad number of days %q", v)
		}
		dur := time.Duration(n) * 24 * time.Hour
		if dur <= 0 {
			return 0, fmt.Errorf("delete_after: must be positive, got %q", v)
		}
		return dur, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("delete_after: %q is not a duration like \"48h\" or \"7d\"", v)
	}
	if d <= 0 {
		return 0, fmt.Errorf("delete_after: must be positive, got %q", v)
	}
	return d, nil
}

// DefaultServer is the configuration kishizu runs with when nothing is set.
func DefaultServer() *Server {
	keep := 2
	dryRun := true
	prune := false
	penaltyDub := 15
	penaltyUncensored := -5
	rejectBatch := true
	seasonFolder := false
	return &Server{
		Library:         "/media/anime",
		Staging:         "/downloads/anime",
		Keep:            &keep,
		Delete:          "immediate",
		Interval:        "5m",
		DryRun:          &dryRun,
		PruneUnselected: &prune,
		Downloader: DownloaderConfig{
			Kind: "transmission",
		},
		Notifier: NotifierConfig{
			Kind: "none",
		},
		Indexer: IndexerConfig{
			Base:        "https://nyaa.si",
			Category:    "1_2",
			UserAgent:   "kishizu",
			MinInterval: "1s",
		},
		Quality: QualityConfig{
			ResolutionFloor:   "1080p",
			GroupOrder:        []string{"VARYG", "Erai-Raws", "SubsPlease", "ToonsHub"},
			PenaltyDub:        &penaltyDub,
			PenaltyUncensored: &penaltyUncensored,
			RejectBatch:       &rejectBatch,
		},
		Naming: NamingConfig{
			Preset:       "kishizu",
			SeasonFolder: &seasonFolder,
		},
		Browse: BrowseConfig{
			Title: "romaji",
		},
	}
}

// Save writes the server section back to the configuration file, preserving
// the show list.
//
// The show list is preserved verbatim rather than re-serialised, for two
// reasons: the hand-written parser accepts shapes a YAML round-trip might
// reformat, and a user's comments in that section are worth keeping.
//
// The write is atomic — temp file, then rename — so a crash mid-write cannot
// leave a config that fails to parse on next start. That would stop kishizu
// starting at all, which is the worst possible outcome of an edit.
func Save(path string, cur *File, next *Server) error {
	if cur == nil {
		cur = &File{}
	}
	if next == nil {
		return fmt.Errorf("nothing to save")
	}
	shows := renderShows(cur.Shows)

	var b bytes.Buffer
	b.WriteString("# kishizu configuration.\n#\n")
	b.WriteString("# This file is rewritten by the web UI. Comments in the server\n")
	b.WriteString("# section are not preserved; comments in the shows list are.\n\n")

	// Encode the server section on its own, then indent it under "server:".
	//
	// The encoder writes top-level keys at column 0, so encoding the struct
	// directly after a "server:" line produces invalid YAML — the keys would
	// be siblings of "server:", not children of it. Indenting the whole
	// block is what makes the nesting real.
	var inner bytes.Buffer
	enc := yaml.NewEncoder(&inner)
	enc.SetIndent(2)
	if err := enc.Encode(next); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	b.WriteString("server:\n")
	for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}

	if shows != "" {
		b.WriteString("\n")
		b.WriteString(shows)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// renderShows renders the show list back to YAML text.
func renderShows(shows []Show) string {
	if len(shows) == 0 {
		return ""
	}
	var b bytes.Buffer
	b.WriteString("shows:\n")
	for _, sh := range shows {
		b.WriteString("  - name: " + sh.Name + "\n")
		if len(sh.Aliases) > 0 {
			b.WriteString("    aliases:\n")
			for _, a := range sh.Aliases {
				b.WriteString("      - " + a + "\n")
			}
		}
		b.WriteString("    watched: " + strconv.Itoa(sh.Watched) + "\n")
		b.WriteString("    max: " + strconv.Itoa(sh.Max) + "\n")
	}
	return b.String()
}

// serverBlock extracts the "server:" block from the file, verbatim.
//
// Returns false when there is no server block, which is fine: a shows-only
// file is still valid.
//
// The block ends at the next top-level key. Indentation is preserved exactly
// as written — "server:" is a top-level key, so its children are already
// correctly nested relative to it, and re-indenting them would flatten the
// structure and make every key a sibling of "server:" instead of a child.
func serverBlock(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	var out []string
	inBlock := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if inBlock {
				out = append(out, "")
			}
			continue
		}
		// A top-level key starts at column 0.
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			if trimmed == "server:" {
				inBlock = true
				out = append(out, l)
				continue
			}
			if inBlock {
				break // next top-level key ends the block
			}
			continue
		}
		if inBlock {
			out = append(out, l)
		}
	}
	if !inBlock {
		return "", false
	}
	return strings.Join(out, "\n") + "\n", true
}

// ---------- show list ----------
//
// Parsed by hand so a mistake can be reported with its line number. The
// shape is one flat list of flat maps, which is all this needs.

// parseShows reads the `shows:` list out of the file.
func parseShows(s string) ([]Show, error) {
	var out []Show
	var cur *Show
	inAliases := false
	// inServer is true while walking the server block, whose lines this
	// parser must skip without understanding.
	inServer := false

	for i, raw := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineNo := i + 1

		// A top-level key starts at column 0. "server:" opens the block the
		// YAML library already validated; "shows:" opens the list parsed
		// here. Anything else at column 0 ends the server block.
		if raw != "" && !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
			inServer = trimmed == "server:"
			if inServer || trimmed == "shows:" {
				continue
			}
		}
		if inServer {
			continue
		}

		switch {
		case inAliases && strings.HasPrefix(trimmed, "- "):
			// An alias item. Checked before the new-show case because both
			// start with "- "; the aliases flag disambiguates.
			if cur == nil {
				return nil, fmt.Errorf("line %d: alias outside a show", lineNo)
			}
			cur.Aliases = append(cur.Aliases, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))

		case strings.HasPrefix(trimmed, "- "):
			// New show entry.
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &Show{}
			inAliases = false
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")); strings.HasPrefix(rest, "name:") {
				cur.Name = strings.TrimSpace(strings.TrimPrefix(rest, "name:"))
			} else if rest != "" {
				return nil, fmt.Errorf("line %d: expected name after '- ', got %q", lineNo, trimmed)
			}

		case strings.HasPrefix(trimmed, "name:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: %q outside a show", lineNo, trimmed)
			}
			cur.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			inAliases = false

		case strings.HasPrefix(trimmed, "aliases:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: %q outside a show", lineNo, trimmed)
			}
			inAliases = true

		case strings.HasPrefix(trimmed, "watched:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: watched outside a show", lineNo)
			}
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "watched:")))
			if err != nil {
				return nil, fmt.Errorf("line %d: bad watched value: %w", lineNo, err)
			}
			cur.Watched = n
			inAliases = false

		case strings.HasPrefix(trimmed, "max:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: max outside a show", lineNo)
			}
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "max:")))
			if err != nil {
				return nil, fmt.Errorf("line %d: bad max", lineNo)
			}
			cur.Max = n
			inAliases = false

		default:
			return nil, fmt.Errorf("line %d: unexpected %q", lineNo, trimmed)
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}

	for _, sh := range out {
		if sh.Name == "" {
			return nil, fmt.Errorf("a show is missing its name")
		}
	}
	return out, nil
}
