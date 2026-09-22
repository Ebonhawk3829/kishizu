package grab

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/naming"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestEpisodeOfWithoutOffsets: an adopted season has no group offsets, because
// the release was chosen by hand and there was never a training run to learn
// them from.
//
// Requiring an offset meant every file in the pack failed to resolve, so a
// completed download sat in staging and its episode stayed "downloading"
// forever. The raw number is correct here: the user confirmed which file is
// which episode at adoption time.
func TestEpisodeOfWithoutOffsets(t *testing.T) {
	cases := map[string]int{
		"[sam] Show - 01 [BD 1080p FLAC] [5E61D169].mkv": 1,
		"[sam] Show - 09 [BD 1080p FLAC] [F81C42B6].mkv": 9,
		"[sam] Show - 22 [BD 1080p FLAC] [EEDBA47E].mkv": 22,
	}
	for name, want := range cases {
		got, ok := episodeOf(filepath.Join("/staging", name), nil, true)
		if !ok {
			t.Errorf("episodeOf(%q) with no offsets: not resolved", name)
			continue
		}
		if got != want {
			t.Errorf("episodeOf(%q) = %d, want %d", name, got, want)
		}
	}
}

// TestEpisodeOfRefusesRawWithoutTrust: the raw-number fallback is keyed on
// the caller's say-so, not on offsets being empty.
//
// Empty offsets is also true of an airing show whose offsets were cleared
// (Reset in the UI). Keying the fallback on emptiness alone would file a
// file numbered 47 against a nonexistent episode row while the episode it
// should have resolved to stayed downloading. The caller's confirmation is
// what makes the raw number trustworthy.
func TestEpisodeOfRefusesRawWithoutTrust(t *testing.T) {
	// Same input as the adopted case, but trustRaw is false: no resolution.
	if _, ok := episodeOf("/staging/[sam] Show - 09 [BD 1080p FLAC].mkv", nil, false); ok {
		t.Error("raw numbers must not be trusted when the caller does not confirm the mapping")
	}
}

// TestEpisodeOfStillUsesOffsetsWhenKnown: the fallback must not displace the
// offset logic for a show that HAS been trained. Otherwise adopting one season
// would silently break numbering for every other show.
func TestEpisodeOfStillUsesOffsetsWhenKnown(t *testing.T) {
	offsets := map[string]int{"VARYG": 40}
	// Raw 47 with a VARYG offset of 40 is episode 7.
	got, ok := episodeOf("/staging/[VARYG] Show - 47 [1080p].mkv", offsets, false)
	if !ok || got != 7 {
		t.Errorf("episodeOf with offsets = %d, %v; want 7, true", got, ok)
	}
	// An unknown group still fails to resolve: guessing would be worse.
	if _, ok := episodeOf("/staging/[Unknown] Show - 47 [1080p].mkv", offsets, false); ok {
		t.Error("an unknown group must not resolve when offsets exist")
	}
}

// TestMediaFilesIsRecursive: a season pack is not a flat list of files. It
// arrives as one directory named after the release, with the episodes inside
// it — and sometimes an Extras subdirectory too.
//
// Reading only the top level found nothing for such a torrent, so a completed
// download sat in staging forever. This is the real layout from a SeaDex
// adoption of a 22-episode BD pack.
func TestMediaFilesIsRecursive(t *testing.T) {
	root := t.TempDir()
	pack := filepath.Join(root, "[sam] Show [BD 1080p FLAC]")
	if err := os.MkdirAll(filepath.Join(pack, "Extras"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"01", "02", "03"} {
		write(t, filepath.Join(pack, "[sam] Show - "+n+" [BD 1080p FLAC].mkv"))
	}
	write(t, filepath.Join(pack, "Extras", "[sam] Show - NCOP 1 [BD].mkv"))
	// A non-media file must be ignored even though it is in the pack.
	write(t, filepath.Join(pack, "notes.txt"))

	got, err := mediaFiles(root)
	if err != nil {
		t.Fatalf("mediaFiles: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d files, want 4 (3 episodes + 1 extra): %v", len(got), got)
	}
	for _, f := range got {
		if filepath.Ext(f) == ".txt" {
			t.Errorf("non-media file included: %q", f)
		}
	}
}

// TestReconcileAdoptedPack: the whole path, end to end. An adopted season with
// no offsets, whose files land in a subdirectory, must be filed into the
// library and marked downloaded — not left in "downloading" with nothing
// moved or renamed, which is what a top-level-only scan or a missing
// raw-number fallback produces.
func TestReconcileAdoptedPack(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, err := st.CreateShow("Show", nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	// Adopted: episodes marked downloading, and NO offsets recorded.
	for _, n := range []int{1, 2, 3} {
		if err := st.UpsertEpisode(sh.ID, n, episode.Downloading, "HASH", "Show"); err != nil {
			t.Fatal(err)
		}
	}

	staging := t.TempDir()
	library := t.TempDir()
	pack := filepath.Join(staging, "Show", "[sam] Show [BD 1080p FLAC]")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"01", "02", "03"} {
		write(t, filepath.Join(pack, "[sam] Show - "+n+" [BD 1080p FLAC].mkv"))
	}

	scheme, err := naming.Resolve(naming.PresetKishizu, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := NewWithScheme(st, staging, library, scheme)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	eps, err := st.EpisodesForShow(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range eps {
		if ep.State != episode.Downloaded {
			t.Errorf("ep%d state = %q, want downloaded", ep.Number, ep.State)
			continue
		}
		if ep.FilePath == "" {
			t.Errorf("ep%d has no file path", ep.Number)
			continue
		}
		if _, err := os.Stat(ep.FilePath); err != nil {
			t.Errorf("ep%d file missing at %s: %v", ep.Number, ep.FilePath, err)
		}
	}
}

// TestReconcileLeavesUnrecognisedFiles: a file that resolves to no in-flight
// episode must stay in staging. Deleting something unrecognised is worse than
// leaving it — this is what protects the Extras directory in a pack.
func TestReconcileLeavesUnrecognisedFiles(t *testing.T) {
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
	kept := filepath.Join(pack, "[sam] Show - 99 [BD].mkv") // not in flight
	write(t, kept)

	r := NewWithScheme(st, staging, library, nil)
	if err := r.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("an unrecognised file was removed from staging: %v", err)
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
