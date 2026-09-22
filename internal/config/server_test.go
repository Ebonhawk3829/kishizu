package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadMissingFileYieldsDefaults: kishizu must start with no configuration
// at all. Requiring a file before first run is a barrier to trying it.
func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Server == nil {
		t.Fatal("Server is nil")
	}
	if f.Server.Library != "/media/anime" {
		t.Errorf("library = %q, want the default", f.Server.Library)
	}
	if f.Server.DryRun == nil || !*f.Server.DryRun {
		t.Error("dry_run must default to true; downloading is opt-in")
	}
}

// TestLoadBrokenFileIsAnError: a file that exists but cannot be parsed must
// fail loudly. Silently ignoring it would run with settings the user never
// asked for.
func TestLoadBrokenFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(p, []byte("server:\n  library: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Error("expected an error for malformed YAML")
	}
}

// TestPartialServerConfigKeepsDefaults: the point of merging into a populated
// struct. A user writes what they care about; everything else must keep its
// default rather than reading as zero.
func TestPartialServerConfigKeepsDefaults(t *testing.T) {
	f, err := Parse([]byte("server:\n  library: /data/anime\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Server.Library != "/data/anime" {
		t.Errorf("library = %q, want /data/anime", f.Server.Library)
	}
	// Untouched fields keep their defaults.
	if f.Server.Staging != "/downloads/anime" {
		t.Errorf("staging = %q, want the default", f.Server.Staging)
	}
	if f.Server.Indexer.Base != "https://nyaa.si" {
		t.Errorf("indexer base = %q, want the default", f.Server.Indexer.Base)
	}
	if len(f.Server.Quality.GroupOrder) == 0 {
		t.Error("group order must keep its default, not become empty")
	}
}

// TestExplicitFalseIsNotMissing: the reason the optional booleans are
// pointers. Without them, a file that omitted dry_run would read as false and
// start downloading — the one default that must never flip silently.
func TestExplicitFalseIsNotMissing(t *testing.T) {
	f, err := Parse([]byte("server:\n  dry_run: false\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Server.DryRun == nil {
		t.Fatal("dry_run is nil; an explicit false must be distinguishable")
	}
	if *f.Server.DryRun {
		t.Error("dry_run = true, want false")
	}

	// And omitting it leaves the default alone.
	g, err := Parse([]byte("server:\n  library: /x\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !*g.Server.DryRun {
		t.Error("omitted dry_run must stay true")
	}
}

// TestServerAndShowsCoexist: one file holds both. This is the shape the
// README documents, and the show parser must skip the server block rather
// than choke on it.
func TestServerAndShowsCoexist(t *testing.T) {
	src := `
server:
  library: /media/anime
  staging: /downloads/anime
  keep: 3
  dry_run: false
  downloader:
    kind: qbittorrent
    qbittorrent_url: http://localhost:8080
  notifier:
    kind: ntfy
    ntfy_topic: https://ntfy.sh/kishizu
  indexer:
    base: https://nyaa.si
    category: "1_2"
    user_agent: kishizu/1.0
    min_interval: 2s
  quality:
    resolution_floor: 720p
    group_order:
      - SubsPlease
      - Erai-Raws
  naming:
    preset: sonarr

shows:
  - name: Tomb Raider King
    aliases:
      - Dogul Wang
    watched: 9
    max: 12
`
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if f.Server.Library != "/media/anime" {
		t.Errorf("library = %q", f.Server.Library)
	}
	if f.Server.Keep == nil || *f.Server.Keep != 3 {
		t.Errorf("keep = %v, want 3", f.Server.Keep)
	}
	if f.Server.Downloader.Kind != "qbittorrent" {
		t.Errorf("downloader = %q", f.Server.Downloader.Kind)
	}
	if f.Server.Downloader.QBittorrentURL != "http://localhost:8080" {
		t.Errorf("qbit url = %q", f.Server.Downloader.QBittorrentURL)
	}
	if f.Server.Notifier.Kind != "ntfy" {
		t.Errorf("notifier = %q", f.Server.Notifier.Kind)
	}
	if f.Server.Notifier.NtfyTopic != "https://ntfy.sh/kishizu" {
		t.Errorf("ntfy topic = %q", f.Server.Notifier.NtfyTopic)
	}
	if f.Server.Indexer.UserAgent != "kishizu/1.0" {
		t.Errorf("user agent = %q", f.Server.Indexer.UserAgent)
	}
	if f.Server.Indexer.MinInterval != "2s" {
		t.Errorf("min interval = %q", f.Server.Indexer.MinInterval)
	}
	if f.Server.Quality.ResolutionFloor != "720p" {
		t.Errorf("floor = %q, want 720p", f.Server.Quality.ResolutionFloor)
	}
	if len(f.Server.Quality.GroupOrder) != 2 || f.Server.Quality.GroupOrder[0] != "SubsPlease" {
		t.Errorf("group order = %v", f.Server.Quality.GroupOrder)
	}
	if f.Server.Naming.Preset != "sonarr" {
		t.Errorf("naming preset = %q", f.Server.Naming.Preset)
	}

	// The show list is preserved alongside the server block.
	if len(f.Shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(f.Shows))
	}
	if f.Shows[0].Name != "Tomb Raider King" {
		t.Errorf("show name = %q", f.Shows[0].Name)
	}
	if len(f.Shows[0].Aliases) != 1 || f.Shows[0].Aliases[0] != "Dogul Wang" {
		t.Errorf("aliases = %v", f.Shows[0].Aliases)
	}
	if f.Shows[0].Watched != 9 || f.Shows[0].Max != 12 {
		t.Errorf("watched/max = %d/%d, want 9/12", f.Shows[0].Watched, f.Shows[0].Max)
	}
}

// TestShowsOnlyFileStillWorks: the existing shape, before any server block
// existed. Backwards compatibility for every shows.yaml already written.
func TestShowsOnlyFileStillWorks(t *testing.T) {
	src := `shows:
  - name: Show
    watched: 3
    max: 12
`
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(f.Shows))
	}
	// Defaults intact.
	if f.Server.Library != "/media/anime" {
		t.Errorf("library = %q, want the default", f.Server.Library)
	}
}

// TestServerOnlyFileHasNoShows: a config with no show list is valid — shows
// can be added from the UI instead.
func TestServerOnlyFileHasNoShows(t *testing.T) {
	f, err := Parse([]byte("server:\n  library: /x\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Shows) != 0 {
		t.Errorf("got %d shows, want 0", len(f.Shows))
	}
}

// TestEmptyFileIsDefaults: a blank file must not be an error, so a user can
// start from nothing and fill it in.
func TestEmptyFileIsDefaults(t *testing.T) {
	for _, src := range []string{"", "# just a comment\n"} {
		f, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		if f.Server == nil || f.Server.Library != "/media/anime" {
			t.Errorf("Parse(%q) lost the defaults", src)
		}
	}
}

// TestDefaultsAreUsable: every default must be a value kishizu can actually
// run with. A default that is an empty string or a nil pointer is a field
// someone forgot.
func TestDefaultsAreUsable(t *testing.T) {
	s := DefaultServer()
	if s.Library == "" || s.Staging == "" {
		t.Error("library and staging must have defaults")
	}
	if s.Keep == nil || s.Interval == "" {
		t.Error("keep and interval must have defaults")
	}
	if s.DryRun == nil {
		t.Error("dry_run must have a default")
	}
	if s.Downloader.Kind == "" {
		t.Error("downloader kind must have a default")
	}
	if s.Notifier.Kind == "" {
		t.Error("notifier kind must have a default")
	}
	if s.Indexer.Base == "" || s.Indexer.Category == "" {
		t.Error("indexer base and category must have defaults")
	}
	if s.Indexer.UserAgent == "" {
		t.Error("user agent must have a default; some indexers reject the Go default")
	}
	if s.Quality.ResolutionFloor == "" || len(s.Quality.GroupOrder) == 0 {
		t.Error("quality floor and group order must have defaults")
	}
	if s.Naming.Preset == "" {
		t.Error("naming preset must have a default")
	}
}

// TestShowNamesWithColonsParse: a show name may contain a colon ("BLEACH:
// Thousand-Year Blood War"), which YAML reads as a key/value separator.
//
// The hand-written show parser tolerates it; the YAML library does not. So
// the server block is extracted and parsed on its own, and the show list is
// never handed to the library. Parsing the whole file would reject every
// shows.yaml ever written — including the one in production.
func TestShowNamesWithColonsParse(t *testing.T) {
	src := `shows:
  - name: BLEACH: Thousand-Year Blood War - The Calamity
    aliases:
      - Bleach: Sennen Kessen Hen - Kashin Tan
      - Bleach
    watched: 7
    max: 10
`
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(f.Shows))
	}
	if f.Shows[0].Name != "BLEACH: Thousand-Year Blood War - The Calamity" {
		t.Errorf("name = %q", f.Shows[0].Name)
	}
	if len(f.Shows[0].Aliases) != 2 {
		t.Errorf("aliases = %v", f.Shows[0].Aliases)
	}
}

// TestServerBlockExtraction: the block must keep its nesting. Flattening it
// would make every key a sibling of "server:" rather than a child, and the
// whole section would silently parse as empty.
func TestServerBlockExtraction(t *testing.T) {
	src := "server:\n  library: /a\n  downloader:\n    kind: qbittorrent\n\nshows:\n  - name: X\n"
	block, ok := serverBlock(src)
	if !ok {
		t.Fatal("no server block found")
	}
	if !strings.Contains(block, "  library: /a") {
		t.Errorf("library lost its indent:\n%s", block)
	}
	if !strings.Contains(block, "    kind: qbittorrent") {
		t.Errorf("nested key lost its indent:\n%s", block)
	}
	if strings.Contains(block, "name: X") {
		t.Error("the show list leaked into the server block")
	}
}
