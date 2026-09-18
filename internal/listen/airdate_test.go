package listen

import (
	"testing"
	"time"
)

// TestAirDateGuardRejectsOldRelease: with a schedule point, a release
// published well before the earliest in-window episode is for an older
// episode and must be rejected. This is what stops a first run grabbing the
// back catalogue.
func TestAirDateGuardRejectsOldRelease(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	// Episode 5 airs "now": eps 1..5 project back a week each, so the oldest
	// acceptable publication is ~5 weeks ago.
	airing := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	_ = st.SetNextEpisode(sh.ID, 5, airing)
	_ = st.ProjectAirDates(sh.ID)

	l := New(st)
	l.Now = func() time.Time { return airing }

	// Published 10 weeks ago: long before episode 1's air date.
	old := item("H1", "[ToonsHub] Show S01E05 1080p WEB-DL")
	old.PubDate = airing.AddDate(0, 0, -70)
	if l.airDateOK(sh, old) {
		t.Error("release published 10 weeks ago passed the air-date guard")
	}

	// Published today: fine.
	fresh := item("H2", "[ToonsHub] Show S01E05 1080p WEB-DL")
	fresh.PubDate = airing
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
	airing := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	_ = st.SetNextEpisode(sh.ID, 5, airing)
	_ = st.ProjectAirDates(sh.ID)

	l := New(st)
	late := item("H1", "[ToonsHub] Show S01E05 1080p WEB-DL REPACK")
	late.PubDate = airing.AddDate(0, 0, 2) // two days after the air date
	if !l.airDateOK(sh, late) {
		t.Error("late repack rejected; the guard must be a lower bound only")
	}
}

// TestAirDateGuardSkipsWhenNoSchedulePoint: the guard is diagnostic-quality
// data and must never block a grab for a show with no schedule point.
func TestAirDateGuardSkipsWhenNoSchedulePoint(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12) // no schedule point

	l := New(st)
	it := item("H1", "[ToonsHub] Show S01E09 1080p WEB-DL")
	it.PubDate = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if !l.airDateOK(sh, it) {
		t.Error("guard blocked a show with no schedule point")
	}
}

// TestAirDateGuardSkipsWhenPubDateMissing: an RSS item without a date has no
// evidence either way, so it passes.
func TestAirDateGuardSkipsWhenPubDateMissing(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	_ = st.SetNextEpisode(sh.ID, 5, time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC))
	_ = st.ProjectAirDates(sh.ID)

	l := New(st)
	it := item("H1", "[ToonsHub] Show S01E05 1080p WEB-DL") // zero PubDate
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
	airing := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	_ = st.SetNextEpisode(sh.ID, 9, airing)
	_ = st.ProjectAirDates(sh.ID)

	l := New(st)
	l.Now = func() time.Time { return airing }

	it := item("H1", "[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL")
	it.PubDate = airing.AddDate(0, 0, -70)
	d := l.evaluate(sh, mustMatcher(t, st, sh), it)
	if d.Grab {
		t.Errorf("old release grabbed: %+v", d)
	}
	if d.Reason != "published before this week's air date" {
		t.Errorf("reason = %q", d.Reason)
	}
}
