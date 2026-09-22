package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
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

// TestWatchedLatchIsTerminal: the watched latch is terminal. A duplicate signal
// cannot rewind it, and no later event can make it grabbable again.
//
// Marking happens through the store (via /api/watched), so this exercises the
// latch the handler depends on rather than a wrapper around it.
func TestWatchedLatchIsTerminal(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", nil, 12)
	_ = st.UpsertEpisode(sh.ID, 5, episode.Downloaded, "H", "rel")

	if err := st.UpsertEpisode(sh.ID, 5, episode.Watched, "", ""); err != nil {
		t.Fatal(err)
	}
	ep, _ := st.GetEpisode(sh.ID, 5)
	if ep.State != episode.Watched {
		t.Fatalf("state = %s, want watched", ep.State)
	}

	// A duplicate signal must not fail or rewind.
	if err := st.UpsertEpisode(sh.ID, 5, episode.Watched, "", ""); err != nil {
		t.Errorf("duplicate watch signal errored: %v", err)
	}
	ep2, _ := st.GetEpisode(sh.ID, 5)
	if ep2.State != episode.Watched {
		t.Errorf("state after duplicate = %s, want watched", ep2.State)
	}
}

// TestSweepDeletesBeyondKeep: the Keep window protects the most recent watched
// episodes; older ones are deleted and marked deleted.
func TestSweepDeletesBeyondKeep(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)

	// Three watched episodes with real files.
	for _, n := range []int{3, 4, 5} {
		p := filepath.Join(lib, "Show", "Show - E0"+string(rune('0'+n))+".mkv")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = st.UpsertEpisode(sh.ID, n, episode.Downloaded, "H", "rel")
		_ = st.SetFilePath(sh.ID, n, p)
		_ = st.UpsertEpisode(sh.ID, n, episode.Watched, "H", "rel")
	}

	h := New(st, lib, 2) // keep the 2 most recent
	deleted, kept, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 {
		t.Fatalf("deleted %d, want 1: %v", len(deleted), deleted)
	}
	if len(kept) != 2 {
		t.Errorf("kept %d, want 2: %v", len(kept), kept)
	}
	if _, err := os.Stat(deleted[0]); !os.IsNotExist(err) {
		t.Error("file still exists after sweep")
	}
	ep, _ := st.GetEpisode(sh.ID, 3)
	if ep.State != episode.Deleted {
		t.Errorf("state = %s, want deleted", ep.State)
	}
}

// TestSweepRefusesOutsideLibrary: a corrupted file_path must never cause a
// delete outside the library root. This is the guard that makes storing paths
// in the database safe.
func TestSweepRefusesOutsideLibrary(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", nil, 12)

	// A path outside any library.
	outside := filepath.Join(t.TempDir(), "elsewhere", "file.mkv")
	_ = os.MkdirAll(filepath.Dir(outside), 0o755)
	_ = os.WriteFile(outside, []byte("x"), 0o644)

	_ = st.UpsertEpisode(sh.ID, 5, episode.Downloaded, "H", "rel")
	_ = st.SetFilePath(sh.ID, 5, outside)
	_ = st.UpsertEpisode(sh.ID, 5, episode.Watched, "H", "rel")

	h := New(st, t.TempDir(), 0) // keep 0, so it would delete if unguarded
	deleted, _, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted %v, want nothing (outside library)", deleted)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("file outside the library was deleted")
	}
}

// TestSweepRefusesSymlinkEscape: a symlink inside the library pointing at a
// file outside it must not cause that file to be deleted.
//
// This is the case a string prefix check on the absolute path could not
// catch: the link's own path is inside the library, so the prefix matched,
// and the delete followed the link out. os.Root resolves each component
// against the root and refuses to follow a link that escapes.
func TestSweepRefusesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}

	// The target, deliberately outside the library.
	outside := filepath.Join(base, "precious.mkv")
	if err := os.WriteFile(outside, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The link, inside the library by path.
	link := filepath.Join(lib, "escape.mkv")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}

	st := testStore(t)
	sh, err := st.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertEpisode(sh.ID, 1, episode.Downloaded, "H", "rel")
	_ = st.SetFilePath(sh.ID, 1, link)
	_ = st.UpsertEpisode(sh.ID, 1, episode.Watched, "H", "rel")

	h := New(st, lib, 0)
	if _, _, err := h.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("the file outside the library was deleted through a symlink")
	}
}

// TestSweepRefusesParentTraversal: a path that climbs out of the library with
// ".." must be refused, not resolved.
func TestSweepRefusesParentTraversal(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "precious.mkv")
	if err := os.WriteFile(outside, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := testStore(t)
	sh, err := st.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertEpisode(sh.ID, 1, episode.Downloaded, "H", "rel")
	_ = st.SetFilePath(sh.ID, 1, filepath.Join(lib, "..", "precious.mkv"))
	_ = st.UpsertEpisode(sh.ID, 1, episode.Watched, "H", "rel")

	h := New(st, lib, 0)
	if _, _, err := h.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("the file outside the library was deleted via .. traversal")
	}
}
