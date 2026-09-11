// Package episode models the lifecycle of a single episode and the rules that
// govern whether a release for it may be grabbed automatically.
package episode

import "strings"

// State is the lifecycle position of one (show, episode) pair.
//
// The ordering matters: it is a one-way latch. An episode moves forward through
// these states and never back — except by explicit user action.
type State string

const (
	Wanted      State = "wanted"      // known, not yet grabbed
	Downloading State = "downloading" // handed to Transmission
	Downloaded  State = "downloaded"  // on disk, not yet watched
	// Missing: kishizu put a file here and it is gone before the watch signal
	// arrived. Only possible when file_path is set, so episodes marked
	// "downloaded" by hand (files on the user's PC) never reach this state.
	Missing State = "missing"
	Watched State = "watched" // user finished it
	Deleted State = "deleted" // file removed after watching
	Blocked State = "blocked" // user said never grab this
)

// ParseState maps a stored string to a State, defaulting to Wanted for anything
// unrecognised so a bad row degrades to "not yet handled" rather than "terminal".
func ParseState(s string) State {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "wanted":
		return Wanted
	case "downloading":
		return Downloading
	case "downloaded":
		return Downloaded
	case "missing":
		return Missing
	case "watched":
		return Watched
	case "deleted":
		return Deleted
	case "blocked":
		return Blocked
	default:
		return Wanted
	}
}

// Terminal reports whether the episode has been consumed: watched or deleted.
//
// This is the guard against the resurrection bug. Once an episode is watched and
// deleted, a late or re-release must not bring it back — the user finished with
// it, and re-downloading would push the file to the PC again via Syncthing.
func (s State) Terminal() bool {
	return s == Watched || s == Deleted
}

// MayAutoGrab reports whether a release for this episode may be grabbed
// automatically.
//
//   - Blocked: never, regardless of quality.
//   - Watched/Deleted: never. Terminal — see Terminal().
//   - Wanted/Downloading: yes, but only a better-ranked candidate should replace
//     an existing one, and only inside the delay window. That decision belongs to
//     the ranker, not here.
//   - Downloaded: no. The episode is on disk; a remake is surfaced for manual
//     action rather than auto-replacing a file that might be mid-watch.
func (s State) MayAutoGrab() bool {
	return s == Wanted || s == Downloading
}

// Advance moves the state forward, refusing to move backwards. Returns false when
// the transition would rewind the latch.
//
// The latch is what makes the consumed-episode guard structural rather than a
// check someone can forget to apply: it is not possible to put a deleted episode
// back into a grabbable state by accident.
func (s State) Advance(next State) (State, bool) {
	if next == s {
		return s, true
	}
	if rank(next) <= rank(s) {
		return s, false
	}
	return next, true
}

func rank(s State) int {
	switch s {
	case Wanted:
		return 0
	case Downloading:
		return 1
	case Downloaded:
		return 2
	case Missing:
		// Missing sits alongside Downloaded: the file was here and is not now.
		// It is not terminal — the user may re-grab or mark it watched.
		return 2
	case Watched:
		return 3
	case Deleted:
		return 4
	case Blocked:
		// Blocked sits outside the normal flow; treat it as terminal-high so
		// nothing advances out of it without an explicit unblock.
		return 5
	default:
		return 0
	}
}
