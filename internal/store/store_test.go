package store

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/match"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateShowRoundTrip(t *testing.T) {
	s := testStore(t)

	sh, err := s.CreateShow("Tomb Raider King", []string{"Dogul Wang"}, 12)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	if sh.ID == 0 {
		t.Fatal("want non-zero id")
	}
	if sh.MaxEpisode != 12 {
		t.Errorf("max_episode = %d, want 12", sh.MaxEpisode)
	}

	// The canonical name must be an alias too, so matching never special-cases it.
	want := map[string]bool{"Tomb Raider King": true, "Dogul Wang": true}
	if len(sh.Aliases) != len(want) {
		t.Fatalf("aliases = %v, want %v", sh.Aliases, want)
	}
	for _, a := range sh.Aliases {
		if !want[a] {
			t.Errorf("unexpected alias %q", a)
		}
	}

	got, err := s.GetShow(sh.ID)
	if err != nil {
		t.Fatalf("GetShow: %v", err)
	}
	if got.CanonicalName != "Tomb Raider King" {
		t.Errorf("canonical = %q", got.CanonicalName)
	}
}

func TestCreateShowIsUnique(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateShow("Dupe", nil, 0); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := s.CreateShow("Dupe", nil, 0); err == nil {
		t.Error("duplicate canonical name should fail")
	}
}

func TestAddAliasIsIdempotent(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", []string{"Alt"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAlias(sh.ID, "Alt"); err != nil {
		t.Fatalf("AddAlias: %v", err)
	}
	got, _ := s.GetShow(sh.ID)
	if len(got.Aliases) != 2 {
		t.Errorf("aliases = %v, want 2 (canonical + Alt)", got.Aliases)
	}
}

func TestListShows(t *testing.T) {
	s := testStore(t)
	for _, n := range []string{"B", "A", "C"} {
		if _, err := s.CreateShow(n, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListShows()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d shows, want 3", len(got))
	}
	if got[0].CanonicalName != "A" {
		t.Errorf("should be ordered by name, got %q first", got[0].CanonicalName)
	}
}

func TestDeleteShowCascades(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Gone", []string{"Alt"}, 0)
	_ = s.SetGroupOffset(sh.ID, "SubsPlease", 40, "seed")
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloaded, "hash", "title")

	if err := s.DeleteShow(sh.ID); err != nil {
		t.Fatalf("DeleteShow: %v", err)
	}
	if got, err := s.GetShow(sh.ID); err == nil && got != nil {
		t.Error("show should be gone")
	}
	// Cascade: offsets and episodes must not survive.
	if off, _ := s.GroupOffsets(sh.ID); len(off) != 0 {
		t.Errorf("offsets survived delete: %v", off)
	}
	if e, _ := s.GetEpisode(sh.ID, 1); e != nil {
		t.Errorf("episode survived delete: %+v", e)
	}
}

func TestGroupOffsets(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("BLEACH", nil, 30)

	if err := s.SetGroupOffset(sh.ID, "Erai-raws", 0, "seed"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupOffset(sh.ID, "SubsPlease", 40, "seed"); err != nil {
		t.Fatal(err)
	}
	// Overwrite should update, not duplicate.
	if err := s.SetGroupOffset(sh.ID, "SubsPlease", 41, "later"); err != nil {
		t.Fatal(err)
	}

	off, err := s.GroupOffsets(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if off["Erai-raws"] != 0 {
		t.Errorf("Erai-raws = %d, want 0", off["Erai-raws"])
	}
	if off["SubsPlease"] != 41 {
		t.Errorf("SubsPlease = %d, want 41 (updated)", off["SubsPlease"])
	}
	if len(off) != 2 {
		t.Errorf("got %d offsets, want 2", len(off))
	}
}

func TestFiltersAndPreferencesAreSeparate(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil, 0)

	// A codec preference (ranking) and a resolution filter (accept/reject) must
	// not land in the same table — that separation is load-bearing.
	if err := s.AddPreference(sh.ID, Preference{Kind: "codec", Value: "x264", Rank: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPreference(sh.ID, Preference{Kind: "codec", Value: "x265", Rank: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFilter(sh.ID, Filter{Kind: "resolution", Op: "min", Value: "1080p"}); err != nil {
		t.Fatal(err)
	}

	prefs, err := s.Preferences(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefs) != 2 {
		t.Fatalf("got %d preferences, want 2", len(prefs))
	}
	// Ordered by rank: x264 before x265.
	if prefs[0].Value != "x264" || prefs[1].Value != "x265" {
		t.Errorf("preferences not rank-ordered: %+v", prefs)
	}

	filters, err := s.Filters(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 1 || filters[0].Kind != "resolution" {
		t.Errorf("filters = %+v", filters)
	}
}

func TestSeenInfohash(t *testing.T) {
	s := testStore(t)

	if seen, _ := s.HasSeen("abc"); seen {
		t.Error("nothing seen yet")
	}
	if err := s.MarkSeen("abc", 1, 7); err != nil {
		t.Fatal(err)
	}
	if seen, _ := s.HasSeen("abc"); !seen {
		t.Error("should be seen after MarkSeen")
	}
	// Idempotent.
	if err := s.MarkSeen("abc", 1, 7); err != nil {
		t.Fatalf("MarkSeen twice: %v", err)
	}
	// Empty hash is a no-op, not an error.
	if err := s.MarkSeen("", 1, 7); err != nil {
		t.Errorf("empty hash: %v", err)
	}
}

// TestDeletedEpisodeIsNotResurrected is the end-to-end version of the
// resurrection guard: walk an episode through the full lifecycle, then confirm a
// late release for it is refused.
func TestDeletedEpisodeIsNotResurrected(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Tomb Raider King", []string{"Dogul Wang"}, 12)

	flow := []episode.State{
		episode.Wanted,
		episode.Downloading,
		episode.Downloaded,
		episode.Watched,
		episode.Deleted,
	}
	for _, st := range flow {
		if err := s.UpsertEpisode(sh.ID, 9, st, "hash1", "Tomb Raider King S01E09"); err != nil {
			t.Fatalf("advance to %s: %v", st, err)
		}
	}

	e, err := s.GetEpisode(sh.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != episode.Deleted {
		t.Fatalf("state = %s, want deleted", e.State)
	}
	if !e.State.Terminal() {
		t.Error("deleted must be terminal")
	}
	if e.State.MayAutoGrab() {
		t.Error("deleted episode must not be auto-grabbable")
	}

	// A late re-release arrives. Upserting to Wanted must NOT rewind the latch.
	if err := s.UpsertEpisode(sh.ID, 9, episode.Wanted, "hash2", "Tomb Raider King S01E09v2"); err != nil {
		t.Fatalf("late upsert: %v", err)
	}
	e, _ = s.GetEpisode(sh.ID, 9)
	if e.State != episode.Deleted {
		t.Errorf("late release resurrected the episode: state = %s", e.State)
	}
}

// TestUnlatchAllowsRedownload: the only way out of a terminal state is explicit
// user action, and it must work when asked for.
func TestUnlatchAllowsRedownload(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil, 12)

	for _, st := range []episode.State{episode.Wanted, episode.Downloading, episode.Downloaded, episode.Watched, episode.Deleted} {
		_ = s.UpsertEpisode(sh.ID, 3, st, "hash", "title")
	}
	_ = s.SetFilePath(sh.ID, 3, "/media/Show/Show - E03.mkv")

	if err := s.Unlatch(sh.ID, 3); err != nil {
		t.Fatalf("Unlatch: %v", err)
	}
	e, _ := s.GetEpisode(sh.ID, 3)
	if e.State != episode.Wanted {
		t.Errorf("state = %s, want wanted", e.State)
	}
	if e.FilePath != "" {
		t.Errorf("file path should be cleared, got %q", e.FilePath)
	}
	if !e.State.MayAutoGrab() {
		t.Error("unlatched episode should be grabbable again")
	}
}

func TestUnlatchOnNonTerminalIsNoOp(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloaded, "hash", "title")

	if err := s.Unlatch(sh.ID, 1); err != nil {
		t.Fatalf("Unlatch: %v", err)
	}
	e, _ := s.GetEpisode(sh.ID, 1)
	if e.State != episode.Downloaded {
		t.Errorf("state = %s, want downloaded (unchanged)", e.State)
	}
}

func TestEpisodesByState(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloaded, "h1", "t1")
	_ = s.UpsertEpisode(sh.ID, 2, episode.Downloaded, "h2", "t2")
	_ = s.UpsertEpisode(sh.ID, 3, episode.Watched, "h3", "t3")

	got, err := s.EpisodesByState(episode.Downloaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d downloaded, want 2", len(got))
	}
}

// TestMatcherAdapter checks the store-backed Show satisfies the matcher and
// resolves the BLEACH two-convention case through the database rather than
// in-memory fixtures.
func TestMatcherAdapter(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("BLEACH: Thousand-Year Blood War - The Calamity", []string{
		"Bleach: Sennen Kessen Hen - Kashin Tan",
		"BLEACH Thousand Year Blood War",
	}, 30)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetGroupOffset(sh.ID, "Erai-raws", 0, "seed")
	_ = s.SetGroupOffset(sh.ID, "SubsPlease", 40, "seed")

	m, err := s.NewMatcher(sh)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		title string
		want  int
	}{
		{"[Erai-raws] Bleach: Sennen Kessen Hen - Kashin Tan - 07 [1080p]", 7},
		{"[SubsPlease] Bleach - Sennen Kessen Hen - 47 (1080p) [B657D64E].mkv", 7},
		// ToonsHub is unseen; should generalise via the known offset set.
		{"[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p CR WEB-DL", 7},
	}
	for _, tc := range cases {
		got := match.Match(m, tc.title)
		if !got.Matched || got.Episode != tc.want {
			t.Errorf("%s: matched=%v ep=%d want %d (%s)", tc.title, got.Matched, got.Episode, tc.want, got.Reason)
		}
	}
}
