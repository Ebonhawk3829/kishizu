package web

import (
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
)

// TestUnairedShowIsUpcomingNotNeedsTraining: training needs a release to train
// on, and there is no release for an episode that has not aired. Asking the
// user to train a show premiering in four months is asking for something that
// cannot be done.
func TestUnairedShowIsUpcomingNotNeedsTraining(t *testing.T) {
	states := []cycle.State{cycle.UpToDate}
	got, attention := showState(states, false, false)
	if got != Upcoming {
		t.Errorf("untrained + unaired = %q, want %q", got, Upcoming)
	}
	if attention {
		t.Error("an unaired show needs no attention; there is nothing to do yet")
	}
}

// TestAiredUntrainedShowNeedsTraining: once episode 1 exists, training becomes
// possible and the show should say so.
func TestAiredUntrainedShowNeedsTraining(t *testing.T) {
	states := []cycle.State{cycle.UpToDate}
	got, attention := showState(states, false, true)
	if got != NeedsTraining {
		t.Errorf("untrained + aired = %q, want %q", got, NeedsTraining)
	}
	if !attention {
		t.Error("an aired untrained show needs attention; it will never download")
	}
}

// TestAiredTrainedShowUsesCycleState: training status stops mattering once the
// show is trained — the episode cycle decides.
func TestAiredTrainedShowUsesCycleState(t *testing.T) {
	cases := []struct {
		states []cycle.State
		want   string
	}{
		{[]cycle.State{cycle.UpToDate}, string(cycle.UpToDate)},
		{[]cycle.State{cycle.Hunting}, string(cycle.Hunting)},
		{[]cycle.State{cycle.ReadyToWatch}, string(cycle.ReadyToWatch)},
		{[]cycle.State{cycle.Missing}, string(cycle.Missing)},
	}
	for _, c := range cases {
		got, _ := showState(c.states, true, true)
		if got != c.want {
			t.Errorf("trained + aired with %v = %q, want %q", c.states, got, c.want)
		}
	}
}

// TestMidSeasonShowIsNotUpcoming: "aired" means episode 1 has happened, not
// the next unwatched episode. Mid-way through a season the next episode is
// always in the future, and using it made every airing show read as unaired —
// BLEACH at episode 9 was reported as "upcoming".
func TestMidSeasonShowIsNotUpcoming(t *testing.T) {
	// Aired + trained + hunting: normal mid-season state.
	got, _ := showState([]cycle.State{cycle.Hunting}, true, true)
	if got != string(cycle.Hunting) {
		t.Errorf("mid-season hunting = %q, want %q", got, cycle.Hunting)
	}
	// Aired + untrained: needs training, not upcoming.
	got, _ = showState([]cycle.State{cycle.UpToDate}, false, true)
	if got != NeedsTraining {
		t.Errorf("aired + untrained = %q, want %q", got, NeedsTraining)
	}
}

// TestUpcomingBeatsNeedsTraining: a show can be both unaired and untrained.
// Upcoming wins, because there is nothing to train on yet.
func TestUpcomingBeatsNeedsTraining(t *testing.T) {
	// Even with a hunting episode recorded, an unaired show is still waiting.
	got, _ := showState([]cycle.State{cycle.Hunting}, false, false)
	if got != Upcoming {
		t.Errorf("unaired + hunting = %q, want %q", got, Upcoming)
	}
}
