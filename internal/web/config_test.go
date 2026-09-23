package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

func testServerWithConfig(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "kishizu.yaml")
	srv.SetConfigPath(path)
	return srv, st, path
}

// TestGetConfigMasksSecrets: the UI never needs to display a credential, and
// sending one to the browser puts it in the page, in memory, and in any
// screenshot.
func TestGetConfigMasksSecrets(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  downloader:
    kind: qbittorrent
    qbittorrent_pass: hunter2
  notifier:
    kind: gotify
    gotify_token: secret-token
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/config", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"hunter2", "secret-token"} {
		if strings.Contains(body, secret) {
			t.Errorf("config response leaks the secret %q", secret)
		}
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	dl := got["downloader"].(map[string]any)
	if dl["qbittorrent_pass"] != SecretMask {
		t.Errorf("password = %v, want the mask", dl["qbittorrent_pass"])
	}
}

// TestSaveConfigPreservesSecrets: the UI cannot know a credential it never
// displayed, so a masked value must mean "leave it alone" and not "clear it".
func TestSaveConfigPreservesSecrets(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  staging: /downloads/anime
  downloader:
    kind: qbittorrent
    qbittorrent_pass: hunter2
  notifier:
    kind: gotify
    gotify_url: https://gotify.test
    gotify_token: secret-token
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	body := `{"library":"/new/anime","staging":"/downloads/anime","interval":"5m",
	  "keep":2,"dry_run":true,
	  "downloader":{"kind":"qbittorrent","qbittorrent_pass":"` + SecretMask + `"},
	  "notifier":{"kind":"gotify","gotify_url":"https://gotify.test","gotify_token":"` + SecretMask + `"},
	  "indexer":{"base":"https://nyaa.si","category":"1_2","user_agent":"kishizu","min_interval":"1s"},
	  "quality":{"resolution_floor":"1080p","group_order":["VARYG"]},
	  "naming":{"preset":"kishizu","season_folder":false}}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/config", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	after, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Server.Downloader.QBittorrentPass != "hunter2" {
		t.Errorf("password = %q, want it preserved", after.Server.Downloader.QBittorrentPass)
	}
	if after.Server.Notifier.GotifyToken != "secret-token" {
		t.Errorf("token = %q, want it preserved", after.Server.Notifier.GotifyToken)
	}
	if after.Server.Library != "/new/anime" {
		t.Errorf("library = %q, want the new value", after.Server.Library)
	}
}

// TestSaveConfigPreservesShows: the settings UI rewrites the file, and the
// show list must survive. Losing it would silently unseed the database.
func TestSaveConfigPreservesShows(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  staging: /downloads/anime

shows:
  - name: Tomb Raider King
    aliases:
      - Dogul Wang
    watched: 9
    max: 12
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	body := `{"library":"/new","staging":"/downloads/anime","interval":"5m","keep":2,"dry_run":true,
	  "downloader":{"kind":"transmission"},"notifier":{"kind":"none"},
	  "indexer":{"base":"https://nyaa.si","category":"1_2","user_agent":"k","min_interval":"1s"},
	  "quality":{"resolution_floor":"1080p","group_order":["VARYG"]},
	  "naming":{"preset":"kishizu","season_folder":false}}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/config", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	after, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(after.Shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(after.Shows))
	}
	if after.Shows[0].Name != "Tomb Raider King" {
		t.Errorf("show = %q", after.Shows[0].Name)
	}
	if len(after.Shows[0].Aliases) != 1 || after.Shows[0].Aliases[0] != "Dogul Wang" {
		t.Errorf("aliases = %v", after.Shows[0].Aliases)
	}
	if after.Shows[0].Watched != 9 || after.Shows[0].Max != 12 {
		t.Errorf("watched/max = %d/%d, want 9/12", after.Shows[0].Watched, after.Shows[0].Max)
	}
}

// TestSaveConfigPreservesYamlOnlySections: the UI edits a curated subset of
// the config; indexer and quality fine-tuning are yaml-only by design. The
// form no longer renders those fields, so a save that omits them must leave
// the file's values alone — the first Save after a field leaves the form
// would otherwise silently blank a hand-edited value.
func TestSaveConfigPreservesYamlOnlySections(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  staging: /downloads/anime
  indexer:
    base: https://mirror.example
    category: 9_9
    user_agent: custom-agent
    min_interval: 3s
  quality:
    resolution_floor: 720p
    group_order:
      - SubsPlease
      - Erai-raws
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	// The new UI's save body: no indexer key at all, quality carrying only
	// what the form actually renders.
	body := `{"library":"/media/anime","staging":"/downloads/anime","interval":"5m",
"keep":0,"dry_run":false,
  "downloader":{"kind":"transmission"},"notifier":{"kind":"none"},
  "quality":{"resolution_floor":"1080p","group_order":["SubsPlease"]},
  "naming":{"preset":"kishizu","season_folder":false}}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/config", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	after, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// Indexer was absent from the request: the file's values must survive.
	if after.Server.Indexer.Base != "https://mirror.example" {
		t.Errorf("indexer.base = %q, want the yaml value preserved", after.Server.Indexer.Base)
	}
	if after.Server.Indexer.Category != "9_9" {
		t.Errorf("indexer.category = %q, want preserved", after.Server.Indexer.Category)
	}
	if after.Server.Indexer.UserAgent != "custom-agent" {
		t.Errorf("indexer.user_agent = %q, want preserved", after.Server.Indexer.UserAgent)
	}
	if after.Server.Indexer.MinInterval != "3s" {
		t.Errorf("indexer.min_interval = %q, want preserved", after.Server.Indexer.MinInterval)
	}
	// Quality WAS in the request: the request's values win.
	if after.Server.Quality.ResolutionFloor != "1080p" {
		t.Errorf("resolution_floor = %q, want the submitted 1080p", after.Server.Quality.ResolutionFloor)
	}
	if len(after.Server.Quality.GroupOrder) != 1 || after.Server.Quality.GroupOrder[0] != "SubsPlease" {
		t.Errorf("group_order = %v, want the submitted list", after.Server.Quality.GroupOrder)
	}
}

// TestSaveConfigAppliesUnderscoreFields: the UI speaks snake_case JSON, and
// before the config structs carried json tags, Go's decoder silently dropped
// every underscore-named key — dry_run, transmission_rpc, ntfy_topic,
// resolution_floor and the rest decoded to zero and were written back as
// defaults. A save looked successful while discarding half the user's edits.
// This test is the regression guard: every field the form sends must land.
func TestSaveConfigAppliesUnderscoreFields(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  staging: /downloads/anime
  dry_run: false
  downloader:
    kind: transmission
    transmission_rpc: http://old:9091/transmission/rpc
  notifier:
    kind: ntfy
    ntfy_topic: http://old:8085/kishizu
  quality:
    resolution_floor: 720p
    group_order:
      - OldGroup
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"library":"/media/anime","staging":"/downloads/anime","interval":"5m","keep":0,"dry_run":false,
"downloader":{"kind":"transmission","transmission_rpc":"http://new:9091/transmission/rpc"},
"notifier":{"kind":"ntfy","ntfy_topic":"http://new:8085/kishizu"},
"quality":{"resolution_floor":"1080p","group_order":["SubsPlease"]},
"naming":{"preset":"kishizu","pattern":"","season_folder":false}}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/config", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	after, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Server.Downloader.TransmissionRPC != "http://new:9091/transmission/rpc" {
		t.Errorf("transmission_rpc = %q, want the submitted value", after.Server.Downloader.TransmissionRPC)
	}
	if after.Server.Notifier.NtfyTopic != "http://new:8085/kishizu" {
		t.Errorf("ntfy_topic = %q, want the submitted value", after.Server.Notifier.NtfyTopic)
	}
	if after.Server.Quality.ResolutionFloor != "1080p" {
		t.Errorf("resolution_floor = %q, want the submitted value", after.Server.Quality.ResolutionFloor)
	}
	if len(after.Server.Quality.GroupOrder) != 1 || after.Server.Quality.GroupOrder[0] != "SubsPlease" {
		t.Errorf("group_order = %v, want the submitted value", after.Server.Quality.GroupOrder)
	}
	if after.Server.DryRun == nil || *after.Server.DryRun {
		t.Errorf("dry_run = %v, want false from the request", after.Server.DryRun != nil && *after.Server.DryRun)
	}
}

// TestSaveConfigRejectsInvalid: a config that cannot be loaded is worse than
// one that was never edited — kishizu would refuse to start, and the user
// would have to fix it by hand.
func TestSaveConfigRejectsInvalid(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	if err := os.WriteFile(path, []byte("server:\n  library: /a\n  staging: /b\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"no library":     `{"staging":"/b","downloader":{"kind":"transmission"},"notifier":{"kind":"none"},"naming":{"preset":"kishizu"}}`,
		"bad interval":   `{"library":"/a","staging":"/b","interval":"soon","downloader":{"kind":"transmission"},"notifier":{"kind":"none"},"naming":{"preset":"kishizu"}}`,
		"bad downloader": `{"library":"/a","staging":"/b","downloader":{"kind":"deluge"},"notifier":{"kind":"none"},"naming":{"preset":"kishizu"}}`,
		"bad preset":     `{"library":"/a","staging":"/b","downloader":{"kind":"transmission"},"notifier":{"kind":"none"},"naming":{"preset":"jellyfin"}}`,
		"custom no pat":  `{"library":"/a","staging":"/b","downloader":{"kind":"transmission"},"notifier":{"kind":"none"},"naming":{"preset":"custom"}}`,
	}
	for name, body := range cases {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/config", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

// TestConfigEndpointsNeedAPath: without a config file there is nothing to read
// or write, and pretending otherwise would show settings that do not exist.
func TestConfigEndpointsNeedAPath(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []*httptest.ResponseRecorder{
		post(t, srv, "/api/config", `{}`),
	} {
		if req.Code != 501 {
			t.Errorf("status = %d, want 501", req.Code)
		}
	}
}

// TestTimetableNeedsACache: browsing without a cache must say so, rather than
// returning an empty list that looks like "nothing is airing".
func TestTimetableNeedsACache(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable", nil))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

// TestTimetableMarksTracked: a show already being tracked must be marked, so
// the browse list does not offer something the user is already watching.
func TestTimetableMarksTracked(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	c, err := schedule.NewCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the cache directly: the point of this test is the marking, not
	// the fetching.
	if err := c.Seed(&schedule.Timetable{
		Fetched: time.Now(),
		Entries: []schedule.Entry{
			{Slug: "tracked-show", Title: "Tracked Show"},
			{Slug: "new-show", Title: "New Show"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	srv.SetTimetable(c)

	sh, err := st.CreateShow("Tracked Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSlug(sh.ID, "tracked-show"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Entries []struct {
			Slug    string `json:"slug"`
			Tracked bool   `json:"tracked"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(got.Entries))
	}
	bySlug := map[string]bool{}
	for _, e := range got.Entries {
		bySlug[e.Slug] = e.Tracked
	}
	if !bySlug["tracked-show"] {
		t.Error("tracked-show must be marked tracked")
	}
	if bySlug["new-show"] {
		t.Error("new-show must not be marked tracked")
	}
}

// TestTimetableReturnsTheWholeList: browsing must show everything without
// requiring a search first. An empty list until the user types made the panel
// look broken, and hid the fact that the cache was working.
func TestTimetableReturnsTheWholeList(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	c, err := schedule.NewCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Seed(&schedule.Timetable{
		Fetched: time.Now(),
		Entries: []schedule.Entry{
			{Slug: "a", Title: "Alpha"},
			{Slug: "b", Title: "Beta"},
			{Slug: "c", Title: "Gamma"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	srv.SetTimetable(c)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Alpha", "Beta", "Gamma"} {
		if !strings.Contains(body, want) {
			t.Errorf("full list missing %q", want)
		}
	}
	if !strings.Contains(body, `"count":3`) {
		t.Errorf("count wrong: %s", body)
	}
}

// TestTimetableFilterNarrows: the query still works, for when the list is
// long enough to need it.
func TestTimetableFilterNarrows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	c, err := schedule.NewCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Seed(&schedule.Timetable{
		Fetched: time.Now(),
		Entries: []schedule.Entry{
			{Slug: "a", Title: "Alpha"},
			{Slug: "b", Title: "Beta"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	srv.SetTimetable(c)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable?q=alp", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Alpha") || strings.Contains(body, "Beta") {
		t.Errorf("filter did not narrow: %s", body)
	}
}
