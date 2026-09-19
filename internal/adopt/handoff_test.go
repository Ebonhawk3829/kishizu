package adopt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/grab"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// The last link: after adoption marks episodes downloading, does Reconcile
// actually file the staged files into the library?
//
// This is the whole point of pre-declaring the episodes — Reconcile only
// finalises files that resolve to an episode already in flight, so without
// the downloading rows a batch would sit in staging untouched.
func TestReconcileFilesAdoptedBatch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	staging := t.TempDir()
	library := t.TempDir()
	title := "DanMachi III"

	sh, _ := st.CreateShow(title, nil, 12)
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	// Adoption marks these downloading before the torrent completes.
	for i := 1; i <= 3; i++ {
		if err := st.UpsertEpisode(sh.ID, i, episode.Downloading, "H", "sam"); err != nil {
			t.Fatal(err)
		}
	}
	// The group's offset: BD groups number from 1, so offset 0.
	if err := st.SetGroupOffset(sh.ID, "sam", 0, "adopt"); err != nil {
		t.Fatal(err)
	}

	// Simulate Transmission having completed the download into staging.
	dir := filepath.Join(staging, title)
	if err := os.MkdirAll(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		name := filepath.Join(dir, "[sam] DanMachi III - 0"+string(rune('0'+i))+"v2 [BD].mkv")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := grab.New(st, staging, library)
	if err := r.Reconcile(); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		ep, _ := st.GetEpisode(sh.ID, i)
		if ep.State != episode.Downloaded {
			t.Errorf("episode %d state = %s, want downloaded", i, ep.State)
		}
		if ep.FilePath == "" {
			t.Errorf("episode %d has no file path", i)
		} else {
			t.Logf("ep%d -> %s", i, filepath.Base(ep.FilePath))
		}
	}
}
