package train

import (
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
)

// TestInferOffsetsDerivesAbsoluteNumbering: a group using absolute numbering
// (raw 47 for episode 7) must yield offset 40.
func TestInferOffsetsDerivesAbsoluteNumbering(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("BLEACH", []string{"BLEACH"}, 13)
	// Episodes air weekly; ep 7 aired Sep 5, ep 8 airs Sep 12.
	base := time.Date(2026, 9, 12, 14, 0, 0, 0, time.UTC)
	_ = st.SetNextEpisode(sh.ID, 8, base)
	if err := st.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}
	items := []nyaa.Item{
		// Published 2h after ep 7 aired: raw 47 -> offset 40.
		{Title: "[VARYG] BLEACH - 47 (1080p)", PubDate: base.AddDate(0, 0, -7).Add(2 * time.Hour)},
		{Title: "[VARYG] BLEACH - 46 (1080p)", PubDate: base.AddDate(0, 0, -14).Add(2 * time.Hour)},
	}
	got, err := InferOffsets(st, sh, items)
	if err != nil {
		t.Fatal(err)
	}
	if got["VARYG"] != 40 {
		t.Errorf("VARYG offset = %d, want 40", got["VARYG"])
	}
}

// TestInferOffsetsPrefersCurrentOverBackCatalogue: when a group posts the
// current episode alongside older ones, the current one wins.
//
// VARYG publishes three episodes at a time, all within an hour of airing, so
// a freshness window cannot separate them. The back catalogue is always
// lower-numbered, so the current episode is the highest offset. Majority
// voting picked the back catalogue and was confidently wrong.
func TestInferOffsetsPrefersCurrentOverBackCatalogue(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	base := time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC)
	_ = st.SetNextEpisode(sh.ID, 10, base)
	if err := st.ProjectAirDates(sh.ID); err != nil {
		t.Fatal(err)
	}
	items := []nyaa.Item{
		// Three posted within an hour of ep 10 airing: raw 10 (current),
		// plus raw 7 and 8 from the back catalogue.
		{Title: "[VARYG] Show - 10 (1080p)", PubDate: base.Add(time.Hour)},
		{Title: "[VARYG] Show - 07 (1080p)", PubDate: base.Add(2 * time.Hour)},
		{Title: "[VARYG] Show - 08 (1080p)", PubDate: base.Add(3 * time.Hour)},
	}
	got, err := InferOffsets(st, sh, items)
	if err != nil {
		t.Fatal(err)
	}
	if got["VARYG"] != 0 {
		t.Errorf("VARYG offset = %d, want 0 (current episode, not back catalogue)", got["VARYG"])
	}
}

// TestInferOffsetsNeedsAirDates: without air dates there is nothing to infer
// from, and it must say so rather than guessing.
func TestInferOffsetsNeedsAirDates(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	_, err := InferOffsets(st, sh, []nyaa.Item{{Title: "[G] Show - 01"}})
	if err == nil {
		t.Error("expected an error when no air dates exist")
	}
}

// TestEpisodeAt: the most recently aired episode at a given time.
func TestEpisodeAt(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	airs := []airing{{1, base}, {2, base.AddDate(0, 0, 7)}, {3, base.AddDate(0, 0, 14)}}
	cases := []struct {
		t    time.Time
		want int
	}{
		{base.Add(-time.Hour), 0},
		{base, 1},
		{base.AddDate(0, 0, 6), 1},
		{base.AddDate(0, 0, 7), 2},
		{base.AddDate(0, 0, 30), 3},
	}
	for _, c := range cases {
		if got, _ := episodeAt(airs, c.t); got != c.want {
			t.Errorf("episodeAt(%v) = %d, want %d", c.t, got, c.want)
		}
	}
}
