package episode

import "testing"

func TestTerminalStates(t *testing.T) {
	cases := map[State]bool{
		Wanted:      false,
		Downloading: false,
		Downloaded:  false,
		Watched:     true,
		Deleted:     true,
		Blocked:     false,
	}
	for s, want := range cases {
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
}

// TestDeletedEpisodeIsNeverRegrabbed is the regression test for the resurrection
// bug: an episode watched and deleted last week must not be re-grabbed when a
// late or re-release appears inside the RSS window (14-33 days).
func TestDeletedEpisodeIsNeverRegrabbed(t *testing.T) {
	if Deleted.MayAutoGrab() {
		t.Error("deleted episode must never be auto-grabbed")
	}
	if Watched.MayAutoGrab() {
		t.Error("watched episode must never be auto-grabbed")
	}
	if Blocked.MayAutoGrab() {
		t.Error("blocked episode must never be auto-grabbed")
	}
}

func TestMayAutoGrab(t *testing.T) {
	cases := map[State]bool{
		Wanted:      true,
		Downloading: true,
		Downloaded:  false, // on disk; remake is a manual decision
		Watched:     false,
		Deleted:     false,
		Blocked:     false,
	}
	for s, want := range cases {
		if got := s.MayAutoGrab(); got != want {
			t.Errorf("%s.MayAutoGrab() = %v, want %v", s, got, want)
		}
	}
}

func TestAdvanceOnlyMovesForward(t *testing.T) {
	flow := []State{Wanted, Downloading, Downloaded, Watched, Deleted}
	for i := 0; i < len(flow); i++ {
		for j := 0; j < len(flow); j++ {
			got, ok := flow[i].Advance(flow[j])
			// Same state is an idempotent no-op, not a rewind: a repeated
			// "downloaded" event must not be treated as a failure.
			if i == j {
				if !ok || got != flow[i] {
					t.Errorf("%s.Advance(%s) should be a no-op, got (%s, %v)", flow[i], flow[j], got, ok)
				}
				continue
			}
			wantOK := j > i
			if ok != wantOK {
				t.Errorf("%s.Advance(%s) ok = %v, want %v", flow[i], flow[j], ok, wantOK)
			}
			if wantOK && got != flow[j] {
				t.Errorf("%s.Advance(%s) = %s, want %s", flow[i], flow[j], got, flow[j])
			}
			if !wantOK && got != flow[i] {
				t.Errorf("%s.Advance(%s) should not change state, got %s", flow[i], flow[j], got)
			}
		}
	}
}

// TestCannotResurrectDeleted is the structural version of the guard: even if some
// caller tries to move a deleted episode back to wanted, the latch refuses.
func TestCannotResurrectDeleted(t *testing.T) {
	got, ok := Deleted.Advance(Wanted)
	if ok {
		t.Error("deleted must not advance back to wanted")
	}
	if got != Deleted {
		t.Errorf("state should remain deleted, got %s", got)
	}
}

func TestParseStateDefaultsToWanted(t *testing.T) {
	if got := ParseState(""); got != Wanted {
		t.Errorf("empty string should default to wanted, got %s", got)
	}
	if got := ParseState("garbage"); got != Wanted {
		t.Errorf("unknown value should default to wanted, got %s", got)
	}
	if got := ParseState("  DELETED  "); got != Deleted {
		t.Errorf("should parse case-insensitively, got %s", got)
	}
}
