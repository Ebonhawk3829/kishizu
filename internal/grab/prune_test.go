package grab

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

func on(b bool) *bool { return &b }

// TestPruneRemovesUnselectedOnceComplete: a magnet carries no file list, so an
// adoption downloads the whole release and the checkboxes decide which files
// become episodes — not which files arrive. Without pruning, the unselected
// extras sit in staging forever holding disk.
func TestPruneRemovesUnselectedOnceComplete(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, err := st.CreateShow("Show", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2} {
		if err := st.UpsertEpisode(sh.ID, n, episode.Downloading, "HASH", "Show"); err != nil {
			t.Fatal(err)
		}
	}

	staging := t.TempDir()
	library := t.TempDir()
	pack := filepath.Join(staging, "Show", "[sam] Show [BD]")
	if err := os.MkdirAll(filepath.Join(pack, "Extras"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pack, "[sam] Show - 01 [BD].mkv"))
	write(t, filepath.Join(pack, "[sam] Show - 02 [BD].mkv"))
	extra := filepath.Join(pack, "Extras", "[sam] Show - NCOP 1 [BD].mkv")
	write(t, extra)

	r := NewWithScheme(st, staging, library, nil)
	r.PruneUnselected = on(true)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// The episodes were filed.
	eps, _ := st.EpisodesForShow(sh.ID)
	for _, ep := range eps {
		if ep.State != episode.Downloaded {
			t.Errorf("ep%d = %q, want downloaded", ep.Number, ep.State)
		}
	}
	// The unselected extra is gone.
	if _, err := os.Stat(extra); !os.IsNotExist(err) {
		t.Errorf("unselected extra still present: %v", err)
	}
}

// TestPruneWaitsForCompletion: deleting while episodes are still in flight
// would race the download. A file that has not finished writing yet resolves
// to nothing, and pruning it would destroy an episode the user is waiting for.
func TestPruneWaitsForCompletion(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, err := st.CreateShow("Show", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	// Only ep1 has arrived; ep2 is still downloading.
	if err := st.UpsertEpisode(sh.ID, 1, episode.Downloading, "HASH", "Show"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEpisode(sh.ID, 2, episode.Downloading, "HASH", "Show"); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	library := t.TempDir()
	pack := filepath.Join(staging, "Show", "[sam] Show [BD]")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pack, "[sam] Show - 01 [BD].mkv"))
	extra := filepath.Join(pack, "[sam] Show - NCOP 1 [BD].mkv")
	write(t, extra)

	r := NewWithScheme(st, staging, library, nil)
	r.PruneUnselected = on(true)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// ep2 never arrived, so the pack is not complete and nothing is pruned.
	if _, err := os.Stat(extra); err != nil {
		t.Errorf("extra was pruned while ep2 was still downloading: %v", err)
	}
}

// TestPruneOffByDefault: it deletes data, so it must be opt-in. A deployment
// that never asked for it must not lose files.
func TestPruneOffByDefault(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, err := st.CreateShow("Show", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEpisode(sh.ID, 1, episode.Downloading, "HASH", "Show"); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	library := t.TempDir()
	pack := filepath.Join(staging, "Show", "[sam] Show [BD]")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pack, "[sam] Show - 01 [BD].mkv"))
	extra := filepath.Join(pack, "[sam] Show - NCOP 1 [BD].mkv")
	write(t, extra)

	r := NewWithScheme(st, staging, library, nil) // PruneUnselected nil
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Errorf("extra was pruned with the setting off: %v", err)
	}
}

// TestPruneNeverLeavesStaging: a path outside the show's staging directory
// must never be deleted, whatever it resolves to.
func TestPruneNeverLeavesStaging(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, err := st.CreateShow("Show", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEpisode(sh.ID, 1, episode.Downloading, "HASH", "Show"); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	library := t.TempDir()
	pack := filepath.Join(staging, "Show", "[sam] Show [BD]")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pack, "[sam] Show - 01 [BD].mkv"))

	// A file in the library that also resolves to nothing.
	outside := filepath.Join(library, "precious.mkv")
	write(t, outside)

	r := NewWithScheme(st, staging, library, nil)
	r.PruneUnselected = on(true)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a file outside staging was deleted: %v", err)
	}
}

// TestPruneDropsEmptyDirs: the pack's own directory and its Extras
// subdirectory should not be left behind as empty folders.
func TestPruneDropsEmptyDirs(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "pack", "Extras")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	pruneEmptyDirs(root)
	if _, err := os.Stat(filepath.Join(root, "pack")); !os.IsNotExist(err) {
		t.Errorf("empty pack dir left behind: %v", err)
	}
	// The root itself survives: it is the show's staging directory.
	if _, err := os.Stat(root); err != nil {
		t.Errorf("staging root was removed: %v", err)
	}
}

// TestPruneKeepsNonEmptyDirs: a directory still holding a file must survive.
func TestPruneKeepsNonEmptyDirs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "kept.mkv"))
	pruneEmptyDirs(root)
	if _, err := os.Stat(filepath.Join(dir, "kept.mkv")); err != nil {
		t.Errorf("a file in a non-empty dir was lost: %v", err)
	}
}
