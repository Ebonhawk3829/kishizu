package cycle

import (
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// ref is the test's reference "now".
var ref = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func ep(state episode.State) *store.Episode {
	return &store.Episode{ShowID: 1, Number: 1, State: state}
}

// at returns a pointer to a time offset from the reference now.
func at(offset time.Duration) *time.Time {
	t := ref.Add(offset)
	return &t
}

// The anchor is the show's effective next episode: watch-gated, so it never
// sits ahead of the user's progress. A wanted episode is the anchor itself
// or beyond it — there is no wanted-but-behind case to place.
func TestStateCycle(t *testing.T) {
	cases := []struct {
		name     string
		ep       *store.Episode
		anchorEp int
		anchor   *time.Time
		want     State
	}{
		{"watched is up to date", ep(episode.Watched), 1, at(-time.Hour), UpToDate},
		{"deleted is up to date", ep(episode.Deleted), 1, at(-time.Hour), UpToDate},
		{"downloaded is ready to watch", ep(episode.Downloaded), 1, at(-time.Hour), ReadyToWatch},
		{"anchor with future air time is up to date", ep(episode.Wanted), 1, at(time.Hour), UpToDate},
		{"anchor just after air time is hunting", ep(episode.Wanted), 1, at(-time.Hour), Hunting},
		{"anchor near window edge is hunting", ep(episode.Wanted), 1, at(-Window + time.Minute), Hunting},
		{"anchor past window is no release found", ep(episode.Wanted), 1, at(-Window - time.Minute), NoReleaseFound},
		{"anchor with no air time is hunting", ep(episode.Wanted), 1, nil, Hunting},
		// Downloading is its own state, not a form of hunting: the episode
		// is in flight and cannot be re-grabbed, so claiming the listener
		// is still hunting for it is wrong.
		{"downloading is downloading", ep(episode.Downloading), 1, at(-time.Hour), Downloading},
		{"downloading with no anchor is downloading", ep(episode.Downloading), 1, nil, Downloading},
	}
	for _, c := range cases {
		if got := StateOf(c.ep, c.anchorEp, c.anchor, ref); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestBeyondAnchorIsNotDue: an episode past the effective anchor has not been
// reached yet, whatever its state. The gate keeps the anchor at the user's
// progress, so "beyond" is exactly "the season has not got here".
func TestBeyondAnchorIsNotDue(t *testing.T) {
	future := ep(episode.Wanted)
	future.Number = 3
	// Anchor at ep2 with a past air time: ep3 is still not due.
	if got := StateOf(future, 2, at(-time.Hour), ref); got != UpToDate {
		t.Errorf("ep3 with anchor at ep2 = %q, want %q", got, UpToDate)
	}
}

func TestPollInterval(t *testing.T) {
	if d, ok := PollInterval([]State{UpToDate, Hunting}); !ok || d != 3*time.Minute {
		t.Errorf("hunting interval = %v, %v; want 3m, true", d, ok)
	}
	if d, ok := PollInterval([]State{NoReleaseFound}); !ok || d != time.Hour {
		t.Errorf("no-release interval = %v, %v; want 1h", d, ok)
	}
	if _, ok := PollInterval([]State{UpToDate}); ok {
		t.Error("up-to-date show should not be polled")
	}
	if _, ok := PollInterval(nil); ok {
		t.Error("empty state list should not be polled")
	}
}

func TestWindowIs72Hours(t *testing.T) {
	if Window != 72*time.Hour {
		t.Errorf("Window = %v; want 72h", Window)
	}
}
