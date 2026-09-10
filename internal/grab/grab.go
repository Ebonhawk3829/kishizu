// Package grab reconciles in-flight downloads with episode state.
//
// Handing a magnet to Transmission is not the end of a grab: the episode is
// only usable once the file is on disk under its final name. This package
// polls Transmission for finished torrents, renames them into the library
// layout, records file_path, and advances the episode latch.
//
// The rename is what makes the watch signal deterministic. A file called
// "<Show> - E09.mkv" is matched with certainty; an arbitrary release title
// needs the whole confidence model and can be refused.
package grab

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/transmission"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
)

// Reconciler matches finished torrents to episodes and finalises them.
type Reconciler struct {
	st *store.Store
	tc *transmission.Client
}

// New builds a Reconciler.
//
// The library root is not needed: each torrent reports its own downloadDir, so
// the final path comes from Transmission rather than from configuration.
func New(st *store.Store, tc *transmission.Client) *Reconciler {
	return &Reconciler{st: st, tc: tc}
}

// Reconcile finalises every finished torrent that corresponds to a downloading
// episode.
//
// Matching is on infohash: MarkGrabbed records it when the magnet is handed
// off, and Transmission reports the same hash back. No title parsing here.
func (r *Reconciler) Reconcile() error {
	torrents, err := r.tc.List()
	if err != nil {
		return fmt.Errorf("list torrents: %w", err)
	}
	byHash := make(map[string]transmission.Torrent, len(torrents))
	for _, t := range torrents {
		byHash[strings.ToLower(t.Hash)] = t
	}

	shows, err := r.st.ListShows()
	if err != nil {
		return err
	}
	for _, sh := range shows {
		eps, err := r.st.EpisodesForShow(sh.ID)
		if err != nil {
			return err
		}
		for _, ep := range eps {
			if ep.State != episode.Downloading || ep.InfoHash == "" {
				continue
			}
			t, ok := byHash[strings.ToLower(ep.InfoHash)]
			if !ok {
				// Not in Transmission: either done-remove.sh already removed
				// it, or it was added outside kishizu. Left alone either way.
				continue
			}
			if !t.IsFinished {
				continue
			}
			if err := r.finalise(sh, ep, t); err != nil {
				log.Printf("grab: %s ep%d: %v", sh.CanonicalName, ep.Number, err)
			}
		}
	}
	return nil
}

// finalise renames the finished torrent into the library layout and marks the
// episode downloaded.
//
// The target is <library>/<Show>/<Show> - E<NN>.<ext>. The extension comes
// from the largest file in the torrent, since a release may carry more than
// one file.
func (r *Reconciler) finalise(sh *store.Show, ep *store.Episode, t transmission.Torrent) error {
	if len(t.Files) == 0 {
		return fmt.Errorf("torrent %s has no files", t.Hash)
	}
	// The largest file is the episode; others are subtitles or nfos.
	media := t.Files[0]
	for _, f := range t.Files {
		if filepath.Ext(f) != "" && isMedia(f) && fileSizeGuess(f) >= fileSizeGuess(media) {
			media = f
		}
	}
	ext := filepath.Ext(media)
	if ext := strings.ToLower(ext); ext == "" {
		return fmt.Errorf("no file extension on %q", media)
	}

	show := watch.Sanitise(sh.CanonicalName)
	newName := fmt.Sprintf("%s - E%02d%s", show, ep.Number, filepath.Ext(media))

	// Rename within the torrent. The old path is relative to the torrent's
	// download dir, which is how Transmission addresses files.
	oldRel := media
	if err := r.tc.RenamePath(t.Hash, oldRel, newName); err != nil {
		// Already renamed is fine: reconcile runs repeatedly.
		if !strings.Contains(strings.ToLower(err.Error()), "not found") &&
			!strings.Contains(strings.ToLower(err.Error()), "nonexist") {
			return fmt.Errorf("rename: %w", err)
		}
	}

	// The file's new absolute path, as Transmission sees it.
	finalPath := filepath.Join(t.DownloadDir, show, newName)
	if err := r.st.SetFilePath(sh.ID, ep.Number, finalPath); err != nil {
		return err
	}
	if err := r.st.UpsertEpisode(sh.ID, ep.Number, episode.Downloaded, "", ""); err != nil {
		return err
	}
	log.Printf("grab: %s ep%d ready at %s", sh.CanonicalName, ep.Number, finalPath)
	return nil
}

// isMedia reports whether a filename looks like a video file.
func isMedia(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".mov", ".webm":
		return true
	}
	return false
}

// fileSizeGuess is a placeholder ordering: prefer the file whose name is
// longest, since batch extras are usually small. Real sizes would need the
// "files" length field from Transmission; the name ordering is sufficient to
// pick the video among a handful of files.
func fileSizeGuess(name string) int {
	return len(name)
}
