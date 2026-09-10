package listen

import (
	"testing"
	"time"
)

// TestAirDateGuardRejectsOldRelease: with a known cadence, a release published
// well before this week's air date is for an older episode and must be
// rejected. This is what stops a first run grabbing the back catalogue.
func TestAirDateGuardRejectsOldRelease(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	wd := 3 // Wednesday
	_ = st.SetCadence(sh.ID, wd, "animeschedule", time.Now())
	sh, _ = st.GetShow(sh.ID)

	l := New(st)
	// Fake "now" to a known Saturday.
	l.Now = func() time.Time {
		return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC) // Saturday
	}

	// Published 10 days ago: before this week's Wednesday anchor.
	old := item("H1", "[ToonsHub] Show S01E09 1080p WEB-DL")
	old.PubDate = l.Now().AddDate(0, 0, -10)
	if !l.airDateOK(sh, old) {
		t.Log("old release correctly rejected")
	} else {
		t.Error("release published 10 days ago passed the air-date guard")
	}

	// Published today: fine.
	fresh := item("H2", "[ToonsHub] Show S01E09 1080p WEB-DL")
	fresh.PubDate = l.Now()
	if !l.airDateOK(sh, fresh) {
		t.Error("fresh release rejected by the air-date guard")
	}
}

// TestAirDateGuardAllowsLateRepacks: v2 re-uploads land LATE and are good
// candidates, so there must be no upper bound. A release published after the
// anchor always passes.
func TestAirDateGuardAllowsLateRepacks(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	wd := 3
	_ = st.SetCadence(sh.ID, wd, "animeschedule", time.Now())
	sh, _ = st.GetShow(sh.ID)

	l := New(st)
	late := item("H1", "[ToonsHub] Show S01E09 1080p WEB-DL REPACK")
	late.PubDate = l.Now().AddDate(0, 0, 2) // two days after "now"
	if !l.airDateOK(sh, late) {
		t.Error("late repack rejected; the guard must be a lower bound only")
	}
}

// TestAirDateGuardSkipsWhenCadenceUnknown: the guard is diagnostic-quality
// data and must never block a grab for a show whose cadence is unknown.
func TestAirDateGuardSkipsWhenCadenceUnknown(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12) // no cadence

	l := New(st)
	it := item("H1", "[ToonsHub] Show S01E09 1080p WEB-DL")
	it.PubDate = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if !l.airDateOK(sh, it) {
		t.Error("guard blocked a show with no cadence")
	}
}

// TestAirDateGuardSkipsWhenPubDateMissing: an RSS item without a date has no
// evidence either way, so it passes.
func TestAirDateGuardSkipsWhenPubDateMissing(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	wd := 3
	_ = st.SetCadence(sh.ID, wd, "animeschedule", time.Now())
	sh, _ = st.GetShow(sh.ID)

	l := New(st)
	it := item("H1", "[ToonsHub] Show S01E09 1080p WEB-DL") // zero PubDate
	if !l.airDateOK(sh, it) {
		t.Error("guard blocked a release with no pubDate")
	}
}

// TestEvaluateRejectsOldRelease: the guard is wired into the pipeline, not just
// available as a helper.
func TestEvaluateRejectsOldRelease(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	_ = st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training")
	wd := 3
	_ = st.SetCadence(sh.ID, wd, "animeschedule", time.Now())
	sh, _ = st.GetShow(sh.ID)
	// Re-fetch: CreateShow returns the show as it was before SetCadence.
	sh, _ = st.GetShow(sh.ID)

	l := New(st)
	l.Now = func() time.Time { return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC) }

	it := item("H1", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL")
	it.PubDate = l.Now().AddDate(0, 0, -10)
	d := l.evaluate(sh, mustMatcher(t, st, sh), nil, it)
	if d.Grab {
		t.Errorf("old release grabbed: %+v", d)
	}
	if d.Reason != "published before this week's air date" {
		t.Errorf("reason = %q", d.Reason)
	}
}
