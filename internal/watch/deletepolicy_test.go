package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// seedWatched files one watched episode with a real file on disk and returns
// its path. The caller decides how old the watch is.
func seedWatched(t *testing.T, st *store.Store, showID int64, n int, lib string) string {
	t.Helper()
	p := filepath.Join(lib, "Show", "Show - E0"+string(rune('0'+n))+".mkv")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertEpisode(showID, n, episode.Downloaded, "H", "rel")
	_ = st.SetFilePath(showID, n, p)
	_ = st.UpsertEpisode(showID, n, episode.Watched, "H", "rel")
	return p
}

// TestSweepDeleteOff: "off" deletes nothing, whatever the keep window says.
func TestSweepDeleteOff(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)
	p := seedWatched(t, st, sh.ID, 1, lib)

	h := New(st, lib, 0, DeleteOff, 0)
	deleted, kept, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 || len(kept) != 0 {
		t.Errorf("off mode returned deleted=%v kept=%v, want both empty", deleted, kept)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file deleted in off mode: %v", err)
	}
}

// TestSweepDeleteAfterProtectsFreshWatches: "after" keeps anything watched
// within the delay, even past the keep window.
func TestSweepDeleteAfterProtectsFreshWatches(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)
	p := seedWatched(t, st, sh.ID, 1, lib)

	h := New(st, lib, 0, DeleteAfterMode, 48*time.Hour)
	deleted, kept, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 || len(kept) != 1 {
		t.Errorf("deleted=%v kept=%v, want the fresh watch kept", deleted, kept)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("fresh watch deleted within delete_after: %v", err)
	}
}

// TestSweepDeleteAfterReleasesOldWatches: once the delay has passed, the
// episode is deletable like immediate mode.
func TestSweepDeleteAfterReleasesOldWatches(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)
	p := seedWatched(t, st, sh.ID, 1, lib)
	if err := st.BackdateWatched(sh.ID, 1, 72*time.Hour); err != nil {
		t.Fatal(err)
	}

	h := New(st, lib, 0, DeleteAfterMode, 48*time.Hour)
	deleted, _, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 {
		t.Fatalf("deleted %d, want 1: %v", len(deleted), deleted)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("old watch still on disk after delete_after: %v", err)
	}
}

// TestSweepDeleteAfterWithKeep: keep and delay both apply — keep protects the
// most recent N regardless of age, delay protects everything fresh.
func TestSweepDeleteAfterWithKeep(t *testing.T) {
	st := testStore(t)
	lib := t.TempDir()
	sh, _ := st.CreateShow("Show", nil, 12)
	old := seedWatched(t, st, sh.ID, 1, lib)
	if err := st.BackdateWatched(sh.ID, 1, 72*time.Hour); err != nil {
		t.Fatal(err)
	}
	fresh := seedWatched(t, st, sh.ID, 2, lib)

	// Keep 1 protects the most recent (ep 2, fresh); the delay protects ep 1
	// only until its age passes. Here ep 1 is old but keep=1 already shields
	// nothing for it — it is the second most recent — so it deletes.
	h := New(st, lib, 1, DeleteAfterMode, 48*time.Hour)
	deleted, kept, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != old {
		t.Errorf("deleted=%v, want [%s]", deleted, old)
	}
	if len(kept) != 1 || kept[0] != fresh {
		t.Errorf("kept=%v, want [%s]", kept, fresh)
	}
}

// TestNewRejectsBadMode: an unknown mode is a construction-time panic, not a
// silent fallthrough to some default.
func TestNewRejectsBadMode(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("New with unknown mode did not panic")
		}
	}()
	New(testStore(t), t.TempDir(), 0, "sometimes", 0)
}

// TestNewRejectsAfterWithoutDelay: "after" with no delay would either never
// delete or delete instantly, depending on the comparison; both are wrong,
// so it refuses to start.
func TestNewRejectsAfterWithoutDelay(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("New with after and no delay did not panic")
		}
	}()
	New(testStore(t), t.TempDir(), 0, DeleteAfterMode, 0)
}
