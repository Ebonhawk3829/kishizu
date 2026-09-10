package listen

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func item(hash, title string) nyaa.Item {
	return nyaa.Item{InfoHash: hash, Title: title, Seeders: 10}
}

// TestDedupesOnInfohash: the same torrent reappearing on the next poll must be
// skipped without downloading. Infohash is the identity of a release.
func TestDedupesOnInfohash(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")

	l := New(st)
	d := l.evaluate(sh, mustMatcher(t, st, sh), nil,
		item("HASH1", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL"))

	if !d.Grab {
		t.Fatalf("first sighting should grab: %+v", d)
	}
	if err := l.MarkGrabbed(d); err != nil {
		t.Fatal(err)
	}

	// Second sighting of the same release.
	d2 := l.evaluate(sh, mustMatcher(t, st, sh), nil,
		item("HASH1", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL"))
	if d2.Grab {
		t.Errorf("same infohash grabbed twice: %+v", d2)
	}
	if d2.Reason != "already seen" {
		t.Errorf("reason = %q, want %q", d2.Reason, "already seen")
	}
}

// TestTerminalEpisodeIsNeverRegrabbed: the resurrection guard. An episode that
// was watched and deleted must not be re-grabbed by a late release, no matter
// how good it is.
func TestTerminalEpisodeIsNeverRegrabbed(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")

	// Episode 9 was watched and deleted.
	if err := st.UpsertEpisode(sh.ID, 9, episode.Watched, "OLD", "old release"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEpisode(sh.ID, 9, episode.Deleted, "OLD", "old release"); err != nil {
		t.Fatal(err)
	}

	l := New(st)
	d := l.evaluate(sh, mustMatcher(t, st, sh), nil,
		item("HASH2", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL REPACK"))

	if d.Grab {
		t.Errorf("terminal episode re-grabbed: %+v", d)
	}
	if want := "episode 9 is deleted, not grabbable"; d.Reason != want {
		t.Errorf("reason = %q, want %q", d.Reason, want)
	}
}

// TestDownloadedEpisodeIsNotRegrabbed: the episode is on disk. A remake is
// surfaced for manual action rather than auto-replacing a file that might be
// mid-watch.
func TestDownloadedEpisodeIsNotRegrabbed(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")
	_ = st.UpsertEpisode(sh.ID, 9, episode.Downloaded, "OLD", "old")

	l := New(st)
	d := l.evaluate(sh, mustMatcher(t, st, sh), nil,
		item("HASH2", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL REPACK"))
	if d.Grab {
		t.Errorf("downloaded episode re-grabbed: %+v", d)
	}
}

// TestResolutionFloor: resolution is a floor, not a ladder. 720p is rejected
// when the floor is 1080p; 2160p passes.
func TestResolutionFloor(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")
	_ = st.AddFilter(sh.ID, store.Filter{Kind: "resolution", Op: "min", Value: "1080p"})

	l := New(st)
	m := mustMatcher(t, st, sh)

	low := l.evaluate(sh, m, mustFilters(t, st, sh.ID),
		item("H1", "[ToonsHub] Tomb Raider King S01E09 720p WEB-DL"))
	if low.Grab {
		t.Errorf("720p grabbed with a 1080p floor: %+v", low)
	}
	high := l.evaluate(sh, m, mustFilters(t, st, sh.ID),
		item("H2", "[ToonsHub] Tomb Raider King S01E09 2160p WEB-DL"))
	if !high.Grab {
		t.Errorf("2160p rejected with a 1080p floor: %+v", high)
	}
}

// TestGroupExclusion: an excluded group is never grabbed.
func TestGroupExclusion(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")
	_ = st.AddFilter(sh.ID, store.Filter{Kind: "group", Op: "exclude", Value: "BadGroup"})

	l := New(st)
	d := l.evaluate(sh, mustMatcher(t, st, sh), mustFilters(t, st, sh.ID),
		item("H1", "[BadGroup] Tomb Raider King S01E09 1080p WEB-DL"))
	if d.Grab {
		t.Errorf("excluded group grabbed: %+v", d)
	}
}

// TestLowConfidenceIsNotGrabbed: when the model is guessing, it must not
// download. A wrong grab is worse than a missed one.
func TestLowConfidenceIsNotGrabbed(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	// Offsets disagree, so an unseen group is a coin flip.
	_ = st.SetGroupOffset(sh.ID, "A", 0, "training")
	_ = st.SetGroupOffset(sh.ID, "B", 40, "training")

	l := New(st)
	d := l.evaluate(sh, mustMatcher(t, st, sh), nil,
		item("H1", "[BrandNewGroup] Tomb Raider King S01E09 1080p WEB-DL"))
	if d.Grab {
		t.Errorf("low-confidence grab: %+v", d)
	}
}

// TestBestPicksPreferredGroup: when two releases for the same episode are
// candidates, the one matching more preferences wins.
func TestBestPicksPreferredGroup(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)

	prefs := []store.Preference{
		{Kind: "group", Value: "ToonsHub", Rank: 0},
		{Kind: "codec", Value: "h.264", Rank: 0},
		{Kind: "codec", Value: "hevc", Rank: 99},
	}

	decisions := []Decision{
		{Grab: true, ShowID: sh.ID, Episode: 9,
			Item: item("H1", "[OtherGroup] Tomb Raider King S01E09 1080p HEVC")},
		{Grab: true, ShowID: sh.ID, Episode: 9,
			Item: item("H2", "[ToonsHub] Tomb Raider King S01E09 1080p H.264")},
	}
	best := Best(decisions, prefs)
	if len(best) != 1 {
		t.Fatalf("got %d best, want 1", len(best))
	}
	if best[0].Item.InfoHash != "H2" {
		t.Errorf("best = %s, want H2 (preferred group and codec)", best[0].Item.InfoHash)
	}
}

// TestBestBreaksTiesOnSeeders: equal preference, more seeders wins.
func TestBestBreaksTiesOnSeeders(t *testing.T) {
	a := Decision{Grab: true, ShowID: 1, Episode: 5, Item: item("HA", "[G] Show S01E05")}
	a.Item.Seeders = 3
	b := Decision{Grab: true, ShowID: 1, Episode: 5, Item: item("HB", "[G] Show S01E05")}
	b.Item.Seeders = 30

	best := Best([]Decision{a, b}, nil)
	if len(best) != 1 || best[0].Item.InfoHash != "HB" {
		t.Errorf("best = %+v, want HB (more seeders)", best)
	}
}

func mustMatcher(t *testing.T, st *store.Store, sh *store.Show) *store.Matcher {
	t.Helper()
	m, err := st.NewMatcher(sh)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustFilters(t *testing.T, st *store.Store, showID int64) []store.Filter {
	t.Helper()
	f, err := st.Filters(showID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
