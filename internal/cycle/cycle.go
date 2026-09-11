// Package cycle derives the user-facing state of an episode from what is
// stored.
//
// The four states are the user's mental model of where each episode is in its
// weekly cycle. They are derived, not stored: three values (lifecycle state,
// expected air time, now) fully determine which state an episode is in, so
// storing a fifth would only risk disagreeing with the others.
package cycle

import (
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Window is how long after air time a release is actively hunted before the
// episode is considered to have never appeared.
const Window = 72 * time.Hour

// State is the user-facing position of one episode in its weekly cycle.
type State string

const (
	// UpToDate: the episode is watched/deleted and the next release is not due
	// yet. Nothing to do.
	UpToDate State = "up-to-date"
	// Hunting: air time passed, within the window, no release grabbed yet.
	// The listener is actively polling for this episode.
	//
	// Named "hunting" rather than "watching" deliberately: "watching" is what
	// the user does with their eyes. This is the machine searching Nyaa.
	Hunting State = "hunting"
	// ReadyToWatch: on disk, waiting for the user.
	ReadyToWatch State = "ready to watch"
	// Missing: kishizu downloaded this and the file is gone. Needs a decision:
	// re-grab it, or accept the watch signal and mark it watched.
	Missing State = "missing"
	// NoReleaseFound: the window closed with nothing grabbed. Troubleshoot.
	NoReleaseFound State = "no release found"
)

// StateOf derives the state of one episode.
//
//   - downloaded -> ready to watch (the user's queue)
//   - wanted + before air time -> up to date (nothing to do yet)
//   - wanted + within window -> hunting
//   - wanted + window closed -> no release found
//   - watched/deleted -> up to date (this episode's cycle is complete)
func StateOf(ep *store.Episode, now time.Time) State {
	switch episode.ParseState(string(ep.State)) {
	case episode.Downloaded:
		return ReadyToWatch
	case episode.Missing:
		return Missing
	case episode.Watched, episode.Deleted:
		return UpToDate
	}

	// wanted (or downloading, which is a transient form of hunting).
	if ep.AirsAt == nil {
		// No air date: cannot place it in time. Treat as hunting so the show
		// is not silently dropped; the UI flags it as needing attention.
		return Hunting
	}
	switch {
	case now.Before(*ep.AirsAt):
		return UpToDate
	case now.Before(ep.AirsAt.Add(Window)):
		return Hunting
	default:
		return NoReleaseFound
	}
}

// PollInterval is how often a show's RSS should be polled given its episodes'
// states. Zero means do not poll at all.
//
// Any episode hunting gets the aggressive rate; a no-release-found episode
// keeps a slow safety-net poll, because late re-uploads are normal in this
// workflow. Everything else needs nothing.
func PollInterval(states []State) (time.Duration, bool) {
	for _, s := range states {
		if s == Hunting {
			return 3 * time.Minute, true
		}
	}
	for _, s := range states {
		if s == NoReleaseFound {
			return time.Hour, true
		}
	}
	return 0, false
}
