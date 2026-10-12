package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// showsResponse is the subset of /shows these tests read.
type showsResponse struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
	Next  int    `json:"next"`
}

// TestAiredAnchorEpisodeReadsAsHunting: a show whose air time has passed with
// nothing grabbed must read as hunting, not up-to-date.
//
// The regression this pins: the card's state is derived from episode rows, so
// an anchor episode with no row reads as up-to-date however long ago it
// aired. The schedule refresh now materialises the anchor's row, so the
// state derivation has something to work with.
func TestAiredAnchorEpisodeReadsAsHunting(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Show", nil)
	// Ep1 consumed a week ago and the anchor at ep2, whose slot has passed —
	// the PSYREN shape, where ep2 had no row at all.
	_ = st.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -7))
	_ = st.UpsertEpisode(sh.ID, 1, episode.Watched, "", "")
	_ = st.SetGroupOffset(sh.ID, "Group", 0, "training")
	if err := st.SetNextEpisode(sh.ID, 2, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	rec := get(t, srv, "/shows")
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var shows []showsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &shows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range shows {
		if s.ID != sh.ID {
			continue
		}
		if s.State != "hunting" {
			t.Errorf("state = %q, want hunting (an aired, unwatched anchor must hunt)", s.State)
		}
		return
	}
	t.Fatalf("show %d not in response", sh.ID)
}

// TestFutureAnchorStaysUpToDate: materialising a row must not make an episode
// that has not aired read as due.
func TestFutureAnchorStaysUpToDate(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Show", nil)
	_ = st.SetGroupOffset(sh.ID, "Group", 0, "training")
	// Watched through ep4 so the effective anchor is ep5, the future episode
	// rather than the user's progress.
	for i := 1; i <= 4; i++ {
		_ = st.UpsertEpisode(sh.ID, i, episode.Watched, "", "")
	}
	if err := st.SetNextEpisode(sh.ID, 5, time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The row exists now, but its state is wanted and its air time is ahead.
	ep, err := st.GetEpisode(sh.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if ep == nil {
		t.Fatal("anchor row missing; the state derivation cannot see the anchor")
	}

	rec := get(t, srv, "/shows")
	var shows []showsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &shows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range shows {
		if s.ID == sh.ID && s.State != "up-to-date" {
			t.Errorf("state = %q, want up-to-date (the anchor has not aired yet)", s.State)
		}
	}
}
