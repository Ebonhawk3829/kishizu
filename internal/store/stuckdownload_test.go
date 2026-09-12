package store

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestMarkWatchedUpToForce: a stuck download can be marked watched.
//
// A torrent with no seeders never completes, so the episode sits in
// "downloading" forever. The user may still have watched it another way, and
// without force there is no way to record that — the in-flight guard blocks
// it. Force is the escape hatch, and it is opt-in per request.
func TestMarkWatchedUpToForce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloading, "hash1", "rel")

	// Without force, the in-flight episode is left alone.
	if marked, _ := s.MarkWatchedUpTo(sh.ID, 1, false); marked != 0 {
		t.Errorf("without force: marked %d, want 0", marked)
	}
	if ep, _ := s.GetEpisode(sh.ID, 1); ep.State != episode.Downloading {
		t.Errorf("without force: state = %s, want downloading", ep.State)
	}

	// With force, it is marked watched.
	if marked, _ := s.MarkWatchedUpTo(sh.ID, 1, true); marked != 1 {
		t.Errorf("with force: marked %d, want 1", marked)
	}
	if ep, _ := s.GetEpisode(sh.ID, 1); ep.State != episode.Watched {
		t.Errorf("with force: state = %s, want watched", ep.State)
	}
}

// TestMarkWatchedUpToForceRespectsTerminal: force does not resurrect an
// episode the user already finished.
func TestMarkWatchedUpToForceRespectsTerminal(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Watched, "", "")
	_ = s.UpsertEpisode(sh.ID, 2, episode.Deleted, "", "")

	if marked, _ := s.MarkWatchedUpTo(sh.ID, 2, true); marked != 0 {
		t.Errorf("marked %d, want 0 (both terminal)", marked)
	}
}

// TestUpsertEpisodeKeepsFirstTorrent: a second grab for the same episode must
// not replace the torrent already downloading.
//
// Advance() allows Downloading -> Downloading, so the second grab would
// overwrite the infohash. The episode then points at the newer torrent while
// the older one is still going, and neither reconciles: the first is orphaned
// and the episode never leaves "downloading".
func TestUpsertEpisodeKeepsFirstTorrent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloading, "hash-first", "first")
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloading, "hash-second", "second")

	ep, _ := s.GetEpisode(sh.ID, 1)
	if ep.InfoHash != "hash-first" {
		t.Errorf("infohash = %q, want hash-first (the first grab wins)", ep.InfoHash)
	}
	if ep.ReleaseTitle != "first" {
		t.Errorf("release_title = %q, want first", ep.ReleaseTitle)
	}
}
