package train

import (
	"fmt"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
)

// watchedThrough marks episodes 1..n watched, so the show's current position
// is n+1.
func watchedThrough(t *testing.T, st interface {
	UpsertEpisode(showID int64, number int, next episode.State, infohash, releaseTitle string) error
}, showID int64, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if err := st.UpsertEpisode(showID, i, episode.Watched, "", ""); err != nil {
			t.Fatal(err)
		}
	}
}

// TestInferOffsetsAbsoluteNumbering: a group continuing the season (raw 47 for
// episode 7) yields offset 40. Derived from the numbering alone — no dates.
func TestInferOffsetsAbsoluteNumbering(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("BLEACH", []string{"BLEACH"}, 13)
	watchedThrough(t, st, sh.ID, 7) // watched through ep 7
	items := []nyaa.Item{
		{Title: "[VARYG] BLEACH - 47 (1080p)"},
		{Title: "[VARYG] BLEACH - 46 (1080p)"},
		{Title: "[VARYG] BLEACH - 45 (1080p)"},
	}
	got, err := InferOffsets(st, sh, items)
	if err != nil {
		t.Fatal(err)
	}
	if got["VARYG"] != 40 {
		t.Errorf("VARYG offset = %d, want 40 (raw 47 -> ep 7)", got["VARYG"])
	}
}

// TestInferOffsetsRestartedNumbering: a group that restarted for this cour
// (raw 7 for episode 7) yields offset 0.
func TestInferOffsetsRestartedNumbering(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("BLEACH", []string{"BLEACH"}, 13)
	watchedThrough(t, st, sh.ID, 7)
	items := []nyaa.Item{
		{Title: "[Erai-raws] BLEACH - 07 (1080p)"},
		{Title: "[Erai-raws] BLEACH - 06 (1080p)"},
		{Title: "[Erai-raws] BLEACH - 05 (1080p)"},
	}
	got, err := InferOffsets(st, sh, items)
	if err != nil {
		t.Fatal(err)
	}
	if got["Erai-raws"] != 0 {
		t.Errorf("Erai-raws offset = %d, want 0 (raw 7 -> ep 7)", got["Erai-raws"])
	}
}

// TestInferOffsetsTwoConventions: both conventions on the same show resolve
// correctly, which is the whole point of per-group offsets.
func TestInferOffsetsTwoConventions(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("BLEACH", []string{"BLEACH"}, 13)
	watchedThrough(t, st, sh.ID, 7)
	items := []nyaa.Item{
		{Title: "[VARYG] BLEACH - 47 (1080p)"},
		{Title: "[VARYG] BLEACH - 46 (1080p)"},
		{Title: "[Erai-raws] BLEACH - 07 (1080p)"},
		{Title: "[Erai-raws] BLEACH - 06 (1080p)"},
	}
	got, err := InferOffsets(st, sh, items)
	if err != nil {
		t.Fatal(err)
	}
	if got["VARYG"] != 40 {
		t.Errorf("VARYG = %d, want 40", got["VARYG"])
	}
	if got["Erai-raws"] != 0 {
		t.Errorf("Erai-raws = %d, want 0", got["Erai-raws"])
	}
}

// TestInferOffsetsBulkDrop: a group posting the whole season at once still
// gets the right offset, because dates are irrelevant to the numbering.
func TestInferOffsetsBulkDrop(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	watchedThrough(t, st, sh.ID, 9) // current position 10
	var items []nyaa.Item
	for i := 1; i <= 9; i++ {
		items = append(items, nyaa.Item{Title: fmt.Sprintf("[Bulk] Show - %02d (1080p)", i)})
	}
	got, err := InferOffsets(st, sh, items)
	if err != nil {
		t.Fatal(err)
	}
	if got["Bulk"] != 0 {
		t.Errorf("Bulk offset = %d, want 0 (raw 9 -> ep 9)", got["Bulk"])
	}
}

// TestInferOffsetsIgnoresUnparseable: titles with no episode number contribute
// nothing rather than skewing the result.
func TestInferOffsetsIgnoresUnparseable(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	watchedThrough(t, st, sh.ID, 4)
	items := []nyaa.Item{
		{Title: "[G] Show - 05 (1080p)"},
		{Title: "[G] Show Batch 1080p"}, // no episode number
		{Title: "[G] Show NCOP 1080p"},  // no episode number
	}
	got, err := InferOffsets(st, sh, items)
	if err != nil {
		t.Fatal(err)
	}
	if got["G"] != 0 {
		t.Errorf("G offset = %d, want 0", got["G"])
	}
}
