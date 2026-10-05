package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestNextEpisodeRoundTrip: the schedule's next-episode point survives a write
// and read.
func TestNextEpisodeRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil)
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if err := s.SetNextEpisode(sh.ID, 8, airs); err != nil {
		t.Fatal(err)
	}
	// The site fact round-trips verbatim through SiteNextEpisode...
	n, at, err := s.SiteNextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Errorf("n = %d, want 8", n)
	}
	if at == nil || !at.Equal(airs) {
		t.Errorf("at = %v, want %v", at, airs)
	}
	// With nothing watched, the effective anchor is ep1 — the gate caps at
	// progress, and ep8's slot says nothing about when ep1 aired. The cache
	// only holds slots the refresh saw while the episode was "next", so a
	// show set straight to ep8 has no ep1 time: the effective anchor hunts
	// immediately rather than waiting on a date that will never come.
	en, eat, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if en != 1 || eat != nil {
		t.Errorf("effective = %d, %v; want 1, nil (gated to progress, no cached slot)", en, eat)
	}
}

// TestNextEpisodeNilWhenUnknown: a show not on the schedule has no point, and
// that must be distinguishable from a zero time.
func TestNextEpisodeNilWhenUnknown(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil)
	n, at, err := s.SiteNextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || at != nil {
		t.Errorf("n = %d, at = %v, want 0, nil", n, at)
	}
}

// TestWatchGateCapsAnchorAtProgress: the effective anchor is the earlier of
// the site's cursor and the user's progress. The site advertising ep8 while
// nothing is watched anchors kishizu at ep1 — the episodes the user has not
// consumed are not done with, whatever the countdown says.
func TestWatchGateCapsAnchorAtProgress(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)
	airs := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if err := s.SetNextEpisode(sh.ID, 8, airs); err != nil {
		t.Fatal(err)
	}

	// Nothing watched: effective anchor is ep1. The show was set straight
	// to ep8, so ep1's slot was never published to us — the anchor has no
	// air time and hunts immediately.
	n, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("n = %d, want 1 (nothing watched)", n)
	}
	if at != nil {
		t.Errorf("at = %v, want nil (ep1 slot never published)", at)
	}

	// Watch ep1: the gate advances to ep2. The site's cursor is untouched.
	if _, err := s.MarkWatchedUpTo(sh.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	n, _, err = s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("n = %d, want 2 after watching ep1", n)
	}
	siteN, _, _ := s.SiteNextEpisode(sh.ID)
	if siteN != 8 {
		t.Errorf("site next = %d, want 8 (gate must not move the site fact)", siteN)
	}
}

// TestWatchGateHiatusNoPhantomDates: the site skipped a week (ep3 airs three
// weeks after ep2). With ep2 watched, the effective anchor is ep3 at its real
// published time — no projection invents an intermediate date.
func TestWatchGateHiatusNoPhantomDates(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)
	ep2 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	ep3 := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	// The refresh saw ep2 when it was next, then ep3 after the skip.
	if err := s.SetNextEpisode(sh.ID, 2, ep2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNextEpisode(sh.ID, 3, ep3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkWatchedUpTo(sh.ID, 2, false); err != nil {
		t.Fatal(err)
	}
	n, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("n = %d, want 3", n)
	}
	if at == nil || !at.Equal(ep3) {
		t.Errorf("at = %v, want %v (the real published slot, not a projection)", at, ep3)
	}
}

// TestCatchUpWindowOpensAtExposure: the site is at ep8 and the user just
// watched ep1. Ep2's air window closed weeks ago, but the episode was never
// huntable until the watch signal exposed it — so the effective anchor's
// time is the exposure moment, not the stale air date, and the episode reads
// as hunting instead of "no release found" before the listener ever looked.
func TestCatchUpWindowOpensAtExposure(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)
	// The refresh saw ep2 when it was next, long ago.
	ep2 := time.Now().AddDate(0, 0, -30)
	if err := s.SetNextEpisode(sh.ID, 2, ep2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNextEpisode(sh.ID, 8, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Watch ep1: the gate exposes ep2. The exposure stamp must move the
	// effective time from 30 days ago to now.
	if _, err := s.MarkWatchedUpTo(sh.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	n, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("n = %d, want 2", n)
	}
	if at == nil {
		t.Fatal("at = nil, want the exposure stamp")
	}
	if at.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("at = %v, want ~now (exposure), not the stale air date %v", at, ep2)
	}
}

// TestCatchUpWindowClosesEventually: the exposure-bounded window still
// closes. 72 hours after exposure with nothing grabbed, the episode reads as
// no release found and drops to the slow safety-net poll — the same honest
// end state as any other dead window.
func TestCatchUpWindowClosesEventually(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)
	if err := s.SetNextEpisode(sh.ID, 2, time.Now().AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNextEpisode(sh.ID, 8, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkWatchedUpTo(sh.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	// Simulate the window closing: backdate the exposure stamp.
	if _, err := s.db.Exec(`UPDATE episode SET exposed_at = datetime('now', '-73 hours')
		WHERE show_id = ? AND number = 2`, sh.ID); err != nil {
		t.Fatal(err)
	}
	_, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if at == nil {
		t.Fatal("at = nil, want the backdated exposure")
	}
	// The effective time is 73h old: past the 72h window. StateOf turns that
	// into no-release-found; here we assert the store hands over the right
	// time for it to do so.
	if at.After(time.Now().Add(-72 * time.Hour)) {
		t.Errorf("at = %v, want older than the 72h window", at)
	}
}

// TestWatchGateBehindSiteHuntsImmediately: the site is at ep8, the user has
// watched nothing, and ep1's slot was never cached (the show was added
// mid-season). The effective anchor has no air time, which reads as hunting —
// the episode aired, and waiting for a date that will never come would
// forget it.
func TestWatchGateBehindSiteHuntsImmediately(t *testing.T) {
	s := testStore(t)
	sh, _ := s.CreateShow("Show", nil)
	if err := s.SetNextEpisode(sh.ID, 8, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, at, err := s.NextEpisode(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}
	if at != nil {
		t.Errorf("at = %v, want nil (no slot was ever published to us)", at)
	}
}
