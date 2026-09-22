package listen

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/adapt"
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

	l := New(st, nil)
	d := l.evaluate(sh, mustMatcher(t, st, sh),
		item("HASH1", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL"))

	if !d.Grab {
		t.Fatalf("first sighting should grab: %+v", d)
	}
	if err := l.MarkGrabbed(d); err != nil {
		t.Fatal(err)
	}

	// Second sighting of the same release.
	d2 := l.evaluate(sh, mustMatcher(t, st, sh),
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

	l := New(st, nil)
	d := l.evaluate(sh, mustMatcher(t, st, sh),
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

	l := New(st, nil)
	d := l.evaluate(sh, mustMatcher(t, st, sh),
		item("HASH2", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL REPACK"))
	if d.Grab {
		t.Errorf("downloaded episode re-grabbed: %+v", d)
	}
}

// TestResolutionFloor: the global floor. 720p is rejected; 2160p passes. The
// floor is hardcoded in rules.go — training cannot change it.
func TestResolutionFloor(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")

	l := New(st, nil)
	m := mustMatcher(t, st, sh)

	low := l.evaluate(sh, m,
		item("H1", "[ToonsHub] Tomb Raider King S01E09 720p WEB-DL"))
	if low.Grab {
		t.Errorf("720p grabbed below the global floor: %+v", low)
	}
	high := l.evaluate(sh, m,
		item("H2", "[ToonsHub] Tomb Raider King S01E09 2160p WEB-DL"))
	if !high.Grab {
		t.Errorf("2160p rejected above the floor: %+v", high)
	}
}

// TestUnreadableResolutionIsRejected: a title whose resolution the parser
// cannot read is below the floor by default. If it is genuinely 1080p written
// unusually, the fix is to teach the vocabulary, not to lower the guard.
func TestUnreadableResolutionIsRejected(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")

	l := New(st, nil)
	d := l.evaluate(sh, mustMatcher(t, st, sh),
		item("H1", "[ToonsHub] Tomb Raider King S01E09 FHD WEB-DL"))
	if d.Grab {
		t.Errorf("unreadable resolution grabbed: %+v", d)
	}
	if want := "no readable resolution"; d.Reason != want {
		t.Errorf("reason = %q, want %q", d.Reason, want)
	}
}

// TestVocabularyReadsUnusualResolution: once a token is learned, the same
// title parses and clears the floor. The vocabulary must be applied at the
// point of enforcement, not only in the training panel.
func TestVocabularyReadsUnusualResolution(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")
	if err := st.LearnVocabulary("resolution", "FHD", "1080p"); err != nil {
		t.Fatal(err)
	}

	l := New(st, nil)
	d := l.evaluate(sh, mustMatcher(t, st, sh),
		item("H1", "[ToonsHub] Tomb Raider King S01E09 FHD WEB-DL"))
	if !d.Grab {
		t.Errorf("learned vocabulary not applied at enforcement: %+v", d)
	}
}

// TestBatchIsRejected: batches are out of scope globally.
func TestBatchIsRejected(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")

	l := New(st, nil)
	d := l.evaluate(sh, mustMatcher(t, st, sh),
		item("H1", "[ToonsHub] Tomb Raider King (01-12) 1080p WEB-DL"))
	if d.Grab {
		t.Errorf("batch grabbed: %+v", d)
	}
	if d.Reason != "batch" {
		t.Errorf("reason = %q, want %q", d.Reason, "batch")
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

	l := New(st, nil)
	d := l.evaluate(sh, mustMatcher(t, st, sh),
		item("H1", "[BrandNewGroup] Tomb Raider King S01E09 1080p WEB-DL"))
	if d.Grab {
		t.Errorf("low-confidence grab: %+v", d)
	}
}

// TestBestPicksPreferredGroup: the global group order decides between two
// candidates for the same episode. ToonsHub is in the default order;
// OtherGroup is not.
func TestBestPicksPreferredGroup(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")
	_ = st.SetGroupOffset(sh.ID, "OtherGroup", 0, "training")

	decisions := []Decision{
		{Grab: true, ShowID: sh.ID, Episode: 9,
			Item: item("H1", "[OtherGroup] Tomb Raider King S01E09 1080p H.264")},
		{Grab: true, ShowID: sh.ID, Episode: 9,
			Item: item("H2", "[ToonsHub] Tomb Raider King S01E09 1080p HEVC")},
	}
	best := New(st, nil).Best(decisions)
	if len(best) != 1 {
		t.Fatalf("got %d best, want 1", len(best))
	}
	if best[0].Item.InfoHash != "H2" {
		t.Errorf("best = %s, want H2 (preferred group)", best[0].Item.InfoHash)
	}
}

// TestBestBreaksTiesOnSeeders: equal rank, more seeders wins.
func TestBestBreaksTiesOnSeeders(t *testing.T) {
	a := Decision{Grab: true, ShowID: 1, Episode: 5, Item: item("HA", "[ToonsHub] Show S01E05 1080p")}
	a.Item.Seeders = 3
	b := Decision{Grab: true, ShowID: 1, Episode: 5, Item: item("HB", "[ToonsHub] Show S01E05 1080p")}
	b.Item.Seeders = 30

	best := New(nil, nil).Best([]Decision{a, b})
	if len(best) != 1 || best[0].Item.InfoHash != "HB" {
		t.Errorf("best = %+v, want HB (more seeders)", best)
	}
}

func mustMatcher(t *testing.T, st *store.Store, sh *store.Show) *adapt.Show {
	t.Helper()
	m, err := adapt.NewVocab(st).Show(st, sh)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
