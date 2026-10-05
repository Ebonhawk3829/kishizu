package web

import (
	"encoding/json"
	"testing"
	"time"
)

// A stored anchor of 0 is corruption, not a schedule: it comes from a pre-clamp
// "Ep 0" premiere countdown the site no longer publishes. These tests pin the
// UI contract that a 0 anchor is never served and never reads as aired — the
// show must present as waiting, which is what an unscheduled premiere is.

func TestZeroAnchorIsNotAired(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Unaired Show", nil)
	// The corrupt row: episode 0 with a past air time. SetNextEpisode clamps
	// now, so the row is written directly the way the pre-clamp build left it.
	if _, err := st.Exec(`UPDATE show SET next_ep = 0, next_airs_at = ? WHERE id = ?`,
		time.Now().AddDate(0, 0, -16).UTC().Format("2006-01-02 15:04:05"), sh.ID); err != nil {
		t.Fatal(err)
	}

	rec := get(t, srv, "/shows")
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var shows []struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
		Next  *struct {
			Episode int    `json:"episode"`
			AirsAt  string `json:"airs_at"`
			Status  string `json:"status"`
		} `json:"next_schedule"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &shows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range shows {
		if s.ID != sh.ID {
			continue
		}
		if s.State != "upcoming" {
			t.Errorf("state = %q, want upcoming (a 0 anchor is not evidence the premiere happened)", s.State)
		}
		if s.Next != nil {
			t.Errorf("next_schedule = %+v, want nil (a 0 anchor must not render as an episode line)", s.Next)
		}
		return
	}
	t.Fatalf("show %d not in response", sh.ID)
}

func TestClearNextEpisodeRemovesAnchor(t *testing.T) {
	srv := testServer(t)
	st := srv.st
	sh, _ := st.CreateShow("Stale Show", nil)
	if err := st.SetNextEpisode(sh.ID, 0, time.Now().AddDate(0, 0, -16)); err != nil {
		t.Fatal(err)
	}
	// The clamp means the stored value is 1, not 0 — the corrupt row above is
	// only reachable from a pre-clamp database. Clear must remove both halves
	// of the anchor regardless.
	if err := st.ClearNextEpisode(sh.ID); err != nil {
		t.Fatal(err)
	}
	// The site fact is what clear removes; read it through SiteNextEpisode.
	// The effective anchor still reads the user's progress (ep1) — that is
	// the gate working, not a stale anchor.
	n, at, err := st.SiteNextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || at != nil {
		t.Errorf("anchor = (%d, %v), want (0, nil) after clear", n, at)
	}
}
