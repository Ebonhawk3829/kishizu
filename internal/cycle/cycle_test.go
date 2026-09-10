package cycle

import (
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// ref is the test's reference "now".
var ref = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func ep(state episode.State, airs *time.Time) *store.Episode {
	return &store.Episode{ShowID: 1, Number: 1, State: state, AirsAt: airs}
}

// at returns a pointer to a time offset from the reference now.
func at(offset time.Duration) *time.Time {
	t := ref.Add(offset)
	return &t
}

func TestStateCycle(t *testing.T) {
	cases := []struct {
		name string
		ep   *store.Episode
		want State
	}{
		{"watched is up to date", ep(episode.Watched, nil), UpToDate},
		{"deleted is up to date", ep(episode.Deleted, nil), UpToDate},
		{"downloaded is ready to watch", ep(episode.Downloaded, nil), ReadyToWatch},
		{"wanted before air is up to date", ep(episode.Wanted, at(time.Hour)), UpToDate},
		{"wanted just after air is hunting", ep(episode.Wanted, at(-time.Hour)), Hunting},
		{"wanted near window edge is hunting", ep(episode.Wanted, at(-Window+time.Minute)), Hunting},
		{"wanted past window is no release found", ep(episode.Wanted, at(-Window-time.Minute)), NoReleaseFound},
		{"downloading is hunting", ep(episode.Downloading, at(-time.Hour)), Hunting},
	}
	for _, c := range cases {
		if got := StateOf(c.ep, ref); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
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
