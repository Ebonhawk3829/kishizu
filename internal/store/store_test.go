package store

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
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

// TestUnlatchResetsDownloaded: a downloaded episode CAN be unlatched, which is
// the "wrong release" path. The user has the file and wants a different one,
// so the episode goes back to wanted and the next grab skips the release
// already seen.
//
// This is not a terminal rewind: downloaded is not terminal, and the file is
// cleared so nothing is left pointing at a release being replaced.
func TestUnlatchResetsDownloaded(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloaded, "hash", "title")

	if err := s.Unlatch(sh.ID, 1); err != nil {
		t.Fatalf("Unlatch: %v", err)
	}
	e, _ := s.GetEpisode(sh.ID, 1)
	if e.State != episode.Wanted {
		t.Errorf("state = %s, want wanted", e.State)
	}
	if e.FilePath != "" || e.InfoHash != "" {
		t.Errorf("unlatch left release data behind: path=%q hash=%q", e.FilePath, e.InfoHash)
	}
}

// TestUnlatchOnInFlightIsNoOp: wanted and downloading are genuinely already
// grabbable or in flight, so unlatching them would be a no-op at best and
// disruptive at worst.
func TestUnlatchOnInFlightIsNoOp(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloading, "hash", "title")

	if err := s.Unlatch(sh.ID, 1); err != nil {
		t.Fatalf("Unlatch: %v", err)
	}
	e, _ := s.GetEpisode(sh.ID, 1)
	if e.State != episode.Downloading {
		t.Errorf("state = %s, want downloading (unchanged)", e.State)
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

// The matcher adapter used to live in this package, which made the
// persistence layer import the domain logic it was meant to be decoupled
// from. It now lives in internal/adapt, along with these tests.
