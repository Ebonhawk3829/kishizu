package listen

import (
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// dueIDs returns the IDs of shows DueShows considers due.
//
// Compared by ID rather than pointer: DueShows builds fresh Show values from
// the database, so the pointers never match the ones CreateShow returned.
func dueIDs(t *testing.T, l *Listener, legacy time.Duration) map[int64]bool {
	t.Helper()
	out := map[int64]bool{}
	for sh := range l.DueShows(legacy) {
		out[sh.ID] = true
	}
	return out
}

// TestSeasonCompleteIsNotDue: once the next episode is past the season length,
// the show must stop being polled. Otherwise it hunts weekly for an episode
// that will never exist.
func TestSeasonCompleteIsNotDue(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	// Every episode watched — the season is over.
	for i := 1; i <= 12; i++ {
		_ = st.UpsertEpisode(sh.ID, i, episode.Watched, "", "")
	}

	l := New(st, nil)
	if dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("completed season is still due; it would poll forever")
	}
}

// TestSeasonInProgressIsDue: a TRAINED show mid-season must still be polled.
func TestSeasonInProgressIsDue(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	for i := 1; i <= 5; i++ {
		_ = st.UpsertEpisode(sh.ID, i, episode.Watched, "", "")
	}
	// Give ep 6 a past air date so it is hunting.
	_ = st.UpsertEpisode(sh.ID, 6, episode.Wanted, "", "")
	_ = st.SetNextEpisode(sh.ID, 6, time.Now().AddDate(0, 0, -1))
	_ = st.ProjectAirDates(sh.ID)
	// Trained: at least one group's offset is known.
	_ = st.SetGroupOffset(sh.ID, "SomeGroup", 0, "training")

	l := New(st, nil)
	if !dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("mid-season show with a due episode is not being polled")
	}
}

// TestUntrainedShowIsNotDue: an untrained show has no group offsets, so the
// matcher cannot reach the grab threshold whatever it finds. Polling it would
// burn requests to conclude what was already known.
func TestUntrainedShowIsNotDue(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	_ = st.UpsertEpisode(sh.ID, 1, episode.Wanted, "", "")
	_ = st.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -1))
	_ = st.ProjectAirDates(sh.ID)
	// No SetGroupOffset: the show has never been trained.

	l := New(st, nil)
	if dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("untrained show is being polled; it can never match a release")
	}
}

// TestOnUntrainedFiresForAiredShow: a show whose first episode has aired but
// which has no group offsets is inert — never polled, never downloading,
// never alerting through any other path. The OnUntrained callback is the one
// signal it can produce, and it must fire.
func TestOnUntrainedFiresForAiredShow(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	_ = st.UpsertEpisode(sh.ID, 1, episode.Wanted, "", "")
	_ = st.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -1))
	_ = st.ProjectAirDates(sh.ID)
	// No SetGroupOffset: the show has never been trained.

	var got []string
	l := New(st, nil)
	l.OnUntrained = func(show string) { got = append(got, show) }
	_ = l.DueShows(5 * time.Minute)
	if len(got) != 1 || got[0] != "Show" {
		t.Errorf("OnUntrained = %v, want [Show]", got)
	}
}

// TestOnUntrainedSilentForUnairedShow: an announced-but-unscheduled show has
// nothing to train on yet — there are no releases for an episode that does
// not exist. Asking for training then would be asking for the impossible, so
// no signal.
func TestOnUntrainedSilentForUnairedShow(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	_ = st.UpsertEpisode(sh.ID, 1, episode.Wanted, "", "")
	// No air date at all: the show is waiting for a slot.
	// No SetGroupOffset: never trained.

	var got []string
	l := New(st, nil)
	l.OnUntrained = func(show string) { got = append(got, show) }
	_ = l.DueShows(5 * time.Minute)
	if len(got) != 0 {
		t.Errorf("OnUntrained = %v, want none: an unaired show has nothing to train on", got)
	}
}

// TestOnUntrainedSilentOnceTrained: the callback is about the gap, not the
// show. Once an offset exists the show hunts normally and must not signal.
func TestOnUntrainedSilentOnceTrained(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	_ = st.UpsertEpisode(sh.ID, 1, episode.Wanted, "", "")
	_ = st.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -1))
	_ = st.ProjectAirDates(sh.ID)
	_ = st.SetGroupOffset(sh.ID, "SomeGroup", 0, "training")

	var got []string
	l := New(st, nil)
	l.OnUntrained = func(show string) { got = append(got, show) }
	_ = l.DueShows(5 * time.Minute)
	if len(got) != 0 {
		t.Errorf("OnUntrained = %v, want none: a trained show hunts normally", got)
	}
}

// TestUntrainedShowIsDueOnceTrained: the guard is about training, not about
// the show. Learning one offset is enough to start polling.
func TestUntrainedShowIsDueOnceTrained(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 12)
	_ = st.UpsertEpisode(sh.ID, 1, episode.Wanted, "", "")
	_ = st.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -1))
	_ = st.ProjectAirDates(sh.ID)

	l := New(st, nil)
	if dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Fatal("untrained show should not be due")
	}
	if err := st.SetGroupOffset(sh.ID, "SomeGroup", 0, "training"); err != nil {
		t.Fatalf("SetGroupOffset: %v", err)
	}
	if !dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("show should be due once an offset is learned")
	}
}

// TestUnknownMaxStillPolls: a TRAINED show with no known season length must
// not be treated as complete.
func TestUnknownMaxStillPolls(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 0) // max unknown
	_ = st.UpsertEpisode(sh.ID, 1, episode.Wanted, "", "")
	_ = st.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -1))
	_ = st.ProjectAirDates(sh.ID)
	_ = st.SetGroupOffset(sh.ID, "SomeGroup", 0, "training")

	l := New(st, nil)
	if !dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("show with unknown max is not being polled")
	}
}
