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

	l := New(st)
	if dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("completed season is still due; it would poll forever")
	}
}

// TestSeasonInProgressIsDue: a show mid-season must still be polled.
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

	l := New(st)
	if !dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("mid-season show with a due episode is not being polled")
	}
}

// TestUnknownMaxStillPolls: a show with no known season length must not be
// treated as complete.
func TestUnknownMaxStillPolls(t *testing.T) {
	st := testStore(t)
	sh, _ := st.CreateShow("Show", []string{"Show"}, 0) // max unknown
	_ = st.UpsertEpisode(sh.ID, 1, episode.Wanted, "", "")
	_ = st.SetNextEpisode(sh.ID, 1, time.Now().AddDate(0, 0, -1))
	_ = st.ProjectAirDates(sh.ID)

	l := New(st)
	if !dueIDs(t, l, 5*time.Minute)[sh.ID] {
		t.Error("show with unknown max is not being polled")
	}
}
