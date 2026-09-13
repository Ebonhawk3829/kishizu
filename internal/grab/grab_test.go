package grab

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
)

// offsets mirrors the real BLEACH group_offset rows: most groups continue the
// season numbering (offset 40) while Erai-raws restarts each cour (offset 0).
var offsets = map[string]int{
	"Erai-raws": 0, "SubsPlease": 40, "ToonsHub": 40, "VARYG": 40,
	"Doomdos": 40, "ASW": 40, "Raze": 40, "Lazier": 40, "Feibanyama": 40,
	"AnoZu": 40,
}

// TestEpisodeOfResolvesRealFilenames pins the episode resolution against the
// filenames that actually landed on disk. The rest of each name — resolution,
// codec, service, subtitle tags — is noise and must be ignored.
func TestEpisodeOfResolvesRealFilenames(t *testing.T) {
	cases := []struct {
		file string
		want int
	}{
		{"[Doomdos] - BLEACH Thousand-Year Blood War - The Calamity - 48 [1080p IQ WEB-DL].mkv", 8},
		{"[Erai-raws] Bleach - Sennen Kessen Hen - Kashin Tan - 08 [1080p DSNP WEB-DL AVC AAC][MultiSub][AD986D29].mkv", 8},
		{"[SubsPlease] Bleach - Sennen Kessen Hen - 48 (1080p) [F6CC4D70].mkv", 8},
		{"BLEACH.Thousand-Year.Blood.War.S01E48.THE.END.TWO.WORLD.1080p.CR.WEB-DL.JPN.AAC2.0.H.264.MSubs-ToonsHub.mkv", 8},
		{"BLEACH.Thousand.Year.Blood.War.S04E48.THE.END.TWO.WORLD.1080p.CR.WEB-DL.AAC2.0.H.264-VARYG.mkv", 8},
		{"Bleach.2004.S17E48.1080p.CR.WEB-DL.AAC2.0.H.264-AnoZu.mkv", 8},
		{"[ASW] Bleach - Sennen Kessen Hen - 48 [1080p HEVC][E93E8CDB].mkv", 8},
		{"[Raze] Bleach Thousand-Year Blood War - 48 (MultiSub) x265 10bit 1080p 143.8561fps.mkv", 8},
		{"[Lazier] Bleach Thousand-Year Blood War - 48 (WEB 1080p AAC) [EBECC595].mkv", 8},
		{"[Feibanyama] Bleach Thousand Year Blood War S01E48 [IQIYI WebRip 2160p H265 Vesyslow AAC Multi-Subs].mkv", 8},
	}
	for _, c := range cases {
		got, ok := episodeOf(c.file, offsets)
		if !ok {
			t.Errorf("episodeOf(%q) did not resolve", c.file)
			continue
		}
		if got != c.want {
			t.Errorf("episodeOf(%q) = %d, want %d", c.file, got, c.want)
		}
	}
}

// TestEpisodeOfRejectsUnresolvable guards against moving a file we cannot
// place. An unrecognised file must be left in staging, never guessed at.
func TestEpisodeOfRejectsUnresolvable(t *testing.T) {
	cases := map[string]string{
		"some-random-file.mkv":                 "no episode number",
		"[UnknownGroup] Show - 05 [1080p].mkv": "group has no known offset",
		"[Erai-raws] Show - 00 [1080p].mkv":    "resolves to episode 0",
	}
	for file, why := range cases {
		if got, ok := episodeOf(file, offsets); ok {
			t.Errorf("episodeOf(%q) = %d, want no match (%s)", file, got, why)
		}
	}
}

// TestLookupOffsetToleratesPunctuation: groups write their names
// interchangeably with hyphens, underscores and dots.
func TestLookupOffsetToleratesPunctuation(t *testing.T) {
	cases := map[string]int{
		"Erai-raws": 0, "Erai_raws": 0, "Erai raws": 0, "ERAIRAWS": 0,
		"SubsPlease": 40, "subsplease": 40,
	}
	for group, want := range cases {
		got, ok := lookupOffset(group, offsets)
		if !ok {
			t.Errorf("lookupOffset(%q) not found", group)
			continue
		}
		if got != want {
			t.Errorf("lookupOffset(%q) = %d, want %d", group, got, want)
		}
	}
}

// TestReconcileMovesStagedFile is the end-to-end path: a file appears in a
// show's staging directory, and reconcile moves it into the library under its
// final name and advances the latch.
func TestReconcileMovesStagedFile(t *testing.T) {
	st := newTestStore(t)
	sh, err := st.CreateShow("BLEACH: Thousand-Year Blood War - The Calamity", nil, 10)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	for g, off := range offsets {
		if err := st.SetGroupOffset(sh.ID, g, off, "test"); err != nil {
			t.Fatalf("SetGroupOffset: %v", err)
		}
	}
	if err := st.UpsertEpisode(sh.ID, 8, episode.Downloading, "abc123", "[Erai-raws] Bleach - Sennen Kessen Hen - Kashin Tan - 08 [1080p]"); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	library := filepath.Join(root, "library")
	showDir := filepath.Join(staging, "BLEACH Thousand-Year Blood War - The Calamity")
	if err := os.MkdirAll(showDir, 0o775); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(showDir, "[Erai-raws] Bleach - Sennen Kessen Hen - Kashin Tan - 08 [1080p DSNP WEB-DL AVC AAC][MultiSub][AD986D29].mkv")
	if err := os.WriteFile(src, []byte("video"), 0o664); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := New(st, nil, staging, library)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := filepath.Join(library, "BLEACH Thousand-Year Blood War - The Calamity",
		"BLEACH Thousand-Year Blood War - The Calamity - E08.mkv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s to exist: %v", want, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("staging file should be gone, stat err = %v", err)
	}

	ep, err := st.GetEpisode(sh.ID, 8)
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if ep.State != episode.Downloaded {
		t.Errorf("state = %s, want downloaded", ep.State)
	}
	if ep.FilePath != want {
		t.Errorf("file_path = %q, want %q", ep.FilePath, want)
	}
}

// TestReconcileIgnoresNonDownloading: a file that resolves to an episode we
// are not waiting for must be left alone. Deleting something unrecognised is
// worse than leaving it in staging.
func TestReconcileIgnoresNonDownloading(t *testing.T) {
	st := newTestStore(t)
	sh, err := st.CreateShow("BLEACH: Thousand-Year Blood War - The Calamity", nil, 10)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	for g, off := range offsets {
		if err := st.SetGroupOffset(sh.ID, g, off, "test"); err != nil {
			t.Fatalf("SetGroupOffset: %v", err)
		}
	}
	// No episode is downloading.
	if err := st.UpsertEpisode(sh.ID, 8, episode.Wanted, "", ""); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	library := filepath.Join(root, "library")
	showDir := filepath.Join(staging, "BLEACH Thousand-Year Blood War - The Calamity")
	if err := os.MkdirAll(showDir, 0o775); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(showDir, "[Erai-raws] Bleach - Sennen Kessen Hen - Kashin Tan - 08 [1080p].mkv")
	if err := os.WriteFile(src, []byte("video"), 0o664); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := New(st, nil, staging, library)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if _, err := os.Stat(src); err != nil {
		t.Errorf("file should have been left in staging, stat err = %v", err)
	}
	ep, _ := st.GetEpisode(sh.ID, 8)
	if ep.State != episode.Wanted {
		t.Errorf("state = %s, want wanted (unchanged)", ep.State)
	}
}

// TestReconcileCreatesLibraryDir: a daily cleanup job removes show folders
// left with no media files, so the destination cannot be assumed to exist.
func TestReconcileCreatesLibraryDir(t *testing.T) {
	st := newTestStore(t)
	sh, err := st.CreateShow("Firefly Wedding", nil, 12)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	if err := st.SetGroupOffset(sh.ID, "Erai-raws", 0, "test"); err != nil {
		t.Fatalf("SetGroupOffset: %v", err)
	}
	if err := st.UpsertEpisode(sh.ID, 3, episode.Downloading, "def456", "[Erai-raws] Firefly Wedding - 03 [1080p]"); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	library := filepath.Join(root, "library") // deliberately not created
	showDir := filepath.Join(staging, "Firefly Wedding")
	if err := os.MkdirAll(showDir, 0o775); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(showDir, "[Erai-raws] Firefly Wedding - 03 [1080p].mkv")
	if err := os.WriteFile(src, []byte("video"), 0o664); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := New(st, nil, staging, library)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := filepath.Join(library, "Firefly Wedding", "Firefly Wedding - E03.mkv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s to exist: %v", want, err)
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestFinaliseUsesRenameNotCopy guards the deployment constraint that made
// v0.3.3 fail in production.
//
// Staging and library MUST live under a single bind mount. Separate mounts are
// distinct mount points even on the same device, and rename(2) refuses to move
// a file between them (EXDEV). This test cannot see the container's mounts, so
// it asserts the weaker but still useful property that finalise relies on a
// plain rename and does not silently fall back to copying.
//
// The failure it protects against: os.Rename returned "invalid cross-device
// link" and the episode stayed in "downloading" forever. Note that `mv` would
// NOT have caught this — coreutils falls back to copy-then-delete on EXDEV,
// which is exactly why the original check passed and the bug shipped.
func TestFinaliseUsesRenameNotCopy(t *testing.T) {
	st := newTestStore(t)
	sh, err := st.CreateShow("EXDEV Show", nil, 12)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	if err := st.SetGroupOffset(sh.ID, "Erai-raws", 0, "test"); err != nil {
		t.Fatalf("SetGroupOffset: %v", err)
	}
	if err := st.UpsertEpisode(sh.ID, 1, episode.Downloading, "h", "[Erai-raws] EXDEV Show - 01 [1080p]"); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	library := filepath.Join(root, "library")
	showDir := filepath.Join(staging, "EXDEV Show")
	if err := os.MkdirAll(showDir, 0o775); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(showDir, "[Erai-raws] EXDEV Show - 01 [1080p].mkv")
	if err := os.WriteFile(src, []byte("video"), 0o664); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := New(st, nil, staging, library)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// A rename leaves no source behind. A copy fallback would leave the
	// original in staging, which is the symptom we must not reintroduce.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still present after move: a copy fallback would leave it behind (stat err = %v)", err)
	}
	want := filepath.Join(library, "EXDEV Show", "EXDEV Show - E01.mkv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s: %v", want, err)
	}
}

// TestSweepDeletesAllWhenKeepZero: the user wants no watched episodes retained.
func TestSweepDeletesAllWhenKeepZero(t *testing.T) {
	st := newTestStore(t)
	sh, err := st.CreateShow("Tidy Show", nil, 12)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	library := t.TempDir()
	var paths []string
	for i := 1; i <= 3; i++ {
		p := filepath.Join(library, "Tidy Show", fmt.Sprintf("Tidy Show - E%02d.mkv", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o775); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("video"), 0o664); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := st.UpsertEpisode(sh.ID, i, episode.Downloaded, "", ""); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
		if err := st.SetFilePath(sh.ID, i, p); err != nil {
			t.Fatalf("SetFilePath: %v", err)
		}
		if err := st.UpsertEpisode(sh.ID, i, episode.Watched, "", ""); err != nil {
			t.Fatalf("mark watched: %v", err)
		}
		paths = append(paths, p)
	}

	h := watch.New(st, library, 0)
	deleted, kept, err := h.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(kept) != 0 {
		t.Errorf("keep=0 retained %d files, want 0: %v", len(kept), kept)
	}
	if len(deleted) != 3 {
		t.Errorf("deleted %d files, want 3", len(deleted))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should have been deleted, stat err = %v", p, err)
		}
	}
}
