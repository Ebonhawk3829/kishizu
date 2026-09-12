// Package watch handles the watch signal and the deletion that follows it.
//
// The signal comes from a bespoke mpv script that POSTs when an episode
// finishes. AniList is out of the loop entirely: mpv -> kishizu, one hop on the
// tailnet, no third party.
//
// Failure direction: if the signal never arrives, nothing is deleted. A missed
// watch signal leaves files on disk; a false one would remove something the
// user was still watching. Deletion also keeps the N most recent watched
// episodes so a mis-mark is not fatal.
package watch

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Handler processes watch signals.
type Handler struct {
	st *store.Store
	// Keep is how many recently watched episodes to leave on disk. A mis-marked
	// watch signal then costs the user a re-download rather than the file.
	Keep int
	// Library is the root the files live under. Empty disables deletion.
	Library string
}

// New builds a Handler. keep is the number of most-recent watched episodes to
// retain; files older than that are deleted.
func New(st *store.Store, library string, keep int) *Handler {
	if keep < 0 {
		keep = 0
	}
	return &Handler{st: st, Library: library, Keep: keep}
}

// Sweep deletes files for watched episodes beyond the Keep window.
//
// Deletion is deliberately separate from marking: a watch signal that arrives
// while the file is still open, or while the user changes their mind, should not
// race the file out from under them. Sweep runs on its own schedule.
//
// Returns what it deleted and what it left, for logging.
func (h *Handler) Sweep() (deleted []string, kept []string, err error) {
	if h.Library == "" {
		return nil, nil, nil
	}
	shows, err := h.st.ListShows()
	if err != nil {
		return nil, nil, err
	}
	byID := map[int64]string{}
	for _, sh := range shows {
		byID[sh.ID] = sh.CanonicalName
	}

	for _, sh := range shows {
		eps, err := h.st.EpisodesForShow(sh.ID)
		if err != nil {
			return deleted, kept, err
		}
		// Only watched episodes with a file on disk, newest first.
		var watched []*store.Episode
		for _, ep := range eps {
			if ep.State == episode.Watched && ep.FilePath != "" {
				watched = append(watched, ep)
			}
		}
		// Newest watched first, so the Keep window protects the latest.
		sort.Slice(watched, func(i, j int) bool {
			return watched[i].Number > watched[j].Number
		})
		for i, ep := range watched {
			if i < h.Keep {
				debug.Log("keep %s (within the %d most recent)", ep.FilePath, h.Keep)
				kept = append(kept, ep.FilePath)
				continue
			}
			debug.Log("deleting %s (beyond the keep window)", ep.FilePath)
			if err := h.deleteFile(sh.ID, ep); err != nil {
				log.Printf("watch: delete %s: %v", ep.FilePath, err)
				continue
			}
			deleted = append(deleted, ep.FilePath)
		}
	}
	return deleted, kept, nil
}

// CheckMissing finds episodes kishizu downloaded whose file has vanished
// before the watch signal arrived, and marks them missing.
//
// Scoped deliberately to episodes with a file_path. An episode marked
// "downloaded" by hand — the user has it on their PC, the server never had a
// copy — has no path and is not missing. Warning about files kishizu never
// created would be noise, and would need a "trust me" button to dismiss.
//
// Returns the episodes newly marked, so the caller can notify.
func (h *Handler) CheckMissing() ([]*store.Episode, error) {
	if h.Library == "" {
		return nil, nil
	}
	shows, err := h.st.ListShows()
	if err != nil {
		return nil, err
	}
	var out []*store.Episode
	for _, sh := range shows {
		eps, err := h.st.EpisodesForShow(sh.ID)
		if err != nil {
			continue
		}
		for _, ep := range eps {
			if ep.State != episode.Downloaded || ep.FilePath == "" {
				continue
			}
			if fileExists(ep.FilePath) {
				continue
			}
			if err := h.st.SetEpisodeState(sh.ID, ep.Number, episode.Missing); err != nil {
				log.Printf("watch: mark missing %s ep%d: %v", sh.CanonicalName, ep.Number, err)
				continue
			}
			log.Printf("watch: %s ep%d missing (expected %s)", sh.CanonicalName, ep.Number, ep.FilePath)
			out = append(out, ep)
		}
	}
	return out, nil
}

// fileExists reports whether a path is present on disk.
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// deleteFile removes one episode's file and marks the episode deleted.
//
// The path is checked against the library root before deleting: a corrupted
// file_path must never cause a delete outside the library.
func (h *Handler) deleteFile(showID int64, ep *store.Episode) error {
	path := ep.FilePath
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(h.Library)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return fmt.Errorf("refusing to delete %q: outside library %q", abs, root)
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return h.st.UpsertEpisode(showID, ep.Number, episode.Deleted, "", "")
}

// Sanitise makes a show name safe as a directory name on both Linux and
// Windows, since Syncthing moves files between them.
//
// Delegates to release.Sanitise so the writer and the matcher share one
// rule; see the note there.
func Sanitise(name string) string {
	return release.Sanitise(name)
}
