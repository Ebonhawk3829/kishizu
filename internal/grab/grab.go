// Package grab reconciles in-flight downloads with episode state.
//
// Handing a magnet to Transmission is not the end of a grab: the episode is
// only usable once the file is on disk under its final name. This package
// scans the staging directory for completed files, moves them into the library
// layout, records file_path, and advances the episode latch.
//
// The move is what makes the watch signal deterministic. A file called
// "<Show> - E09.mkv" is matched with certainty; an arbitrary release title
// needs the whole confidence model and can be refused.
//
// Staging is scanned rather than Transmission's torrent list because the
// done-script removes torrents on completion — often before this runs. The
// directory is the durable record, and it also says which show a file belongs
// to, so only the episode number has to be read from the name.
package grab

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/transmission"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
)

// Reconciler matches finished downloads to episodes and finalises them.
type Reconciler struct {
	st *store.Store
	tc *transmission.Client
	// Staging is where Transmission drops completed files, as kishizu sees it.
	// One directory per show, so a file's show is known from its location and
	// only its episode number has to be read from the name.
	Staging string
	// Library is the root files are moved into, laid out as
	// <Library>/<Show>/<Show> - E<NN>.<ext>.
	Library string
}

// New builds a Reconciler.
//
// staging and library are both required: staging is scanned for completed
// files, library is where they are moved to.
func New(st *store.Store, tc *transmission.Client, staging, library string) *Reconciler {
	return &Reconciler{st: st, tc: tc, Staging: staging, Library: library}
}

// Reconcile finalises every completed download that corresponds to a
// downloading episode.
//
// Matching is by directory, not by torrent: Transmission's done-script removes
// the torrent on completion, so by the time this runs the torrent is usually
// gone. The staging directory is the durable record instead — it is created
// per show at grab time and survives the torrent's removal.
//
// Within a show's staging directory the episode number is read from each
// filename and corrected by that release group's offset. No show-name matching
// is needed: the directory already says which show it is.
func (r *Reconciler) Reconcile() error {
	if r.Staging == "" || r.Library == "" {
		return nil
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
		// Index the episodes actually in flight. A file is only moved if it
		// resolves to one of these, so an unrecognised file is left alone
		// rather than guessed at.
		inFlight := map[int]*store.Episode{}
		for _, ep := range eps {
			if ep.State == episode.Downloading {
				inFlight[ep.Number] = ep
			}
		}
		if len(inFlight) == 0 {
			continue
		}
		dir := filepath.Join(r.Staging, watch.Sanitise(sh.CanonicalName))
		files, err := mediaFiles(dir)
		if err != nil {
			// Missing directory is normal: nothing has completed for this
			// show yet.
			if os.IsNotExist(err) {
				continue
			}
			log.Printf("grab: scan %s: %v", dir, err)
			continue
		}
		offsets, err := r.st.GroupOffsets(sh.ID)
		if err != nil {
			log.Printf("grab: offsets for %s: %v", sh.CanonicalName, err)
			continue
		}
		for _, f := range files {
			num, ok := episodeOf(f, offsets)
			if !ok {
				debug.Log("%s: cannot resolve episode from %q", sh.CanonicalName, filepath.Base(f))
				continue
			}
			ep, ok := inFlight[num]
			if !ok {
				// Completed, but not an episode we are waiting for. Left in
				// staging: deleting something unrecognised is worse than
				// leaving it.
				debug.Log("%s: %s resolves to ep%d, not downloading", sh.CanonicalName, filepath.Base(f), num)
				continue
			}
			debug.Log("%s ep%d: found %s, finalising", sh.CanonicalName, num, filepath.Base(f))
			if err := r.finalise(sh, ep, f); err != nil {
				log.Printf("grab: %s ep%d: %v", sh.CanonicalName, num, err)
			}
		}
	}
	return nil
}

// episodeOf resolves a staged file to an episode number.
//
// The raw number comes from the filename; the group's offset corrects it to
// the local numbering. Everything else in the name — resolution, codec,
// service, subtitle tags — is noise and is ignored.
func episodeOf(path string, offsets map[string]int) (int, bool) {
	// Strip the extension before parsing: the trailing-group regex otherwise
	// captures "ToonsHub.mkv" as the group name, which matches no offset.
	base := filepath.Base(path)
	rel := release.Parse(strings.TrimSuffix(base, filepath.Ext(base)))
	raw := rel.RawEpisode()
	if raw == 0 {
		return 0, false
	}
	group := rel.Group
	if group == "" {
		group = "(none)"
	}
	off, ok := lookupOffset(group, offsets)
	if !ok {
		return 0, false
	}
	ep := raw - off
	if ep <= 0 {
		return 0, false
	}
	return ep, true
}

// lookupOffset finds a group's offset, tolerating the punctuation differences
// release groups use interchangeably ("Erai-raws" vs "Erai_raws").
func lookupOffset(group string, offsets map[string]int) (int, bool) {
	if v, ok := offsets[group]; ok {
		return v, true
	}
	want := release.NormaliseGroup(group)
	if want == "" {
		return 0, false
	}
	for k, v := range offsets {
		if release.NormaliseGroup(k) == want {
			return v, true
		}
	}
	return 0, false
}

// mediaFiles returns the video files directly under dir.
func mediaFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isMedia(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

// finalise renames the finished torrent into the library layout and marks the
// episode downloaded.
//
// The target is <library>/<Show>/<Show> - E<NN>.<ext>. The extension comes
// from the largest file in the torrent, since a release may carry more than
// one file.
func (r *Reconciler) finalise(sh *store.Show, ep *store.Episode, src string) error {
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		return fmt.Errorf("no file extension on %q", src)
	}

	show := watch.Sanitise(sh.CanonicalName)
	newName := fmt.Sprintf("%s - E%02d%s", show, ep.Number, ext)

	// The library folder is not assumed to exist: a daily cleanup job removes
	// show folders left with no media files, so it is recreated as needed.
	destDir := filepath.Join(r.Library, show)
	if err := os.MkdirAll(destDir, 0o775); err != nil {
		return fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	finalPath := filepath.Join(destDir, newName)

	// Same filesystem, so this is an atomic rename rather than a copy. The
	// file appears in the library complete and correctly named, or not at all.
	if err := os.Rename(src, finalPath); err != nil {
		return fmt.Errorf("move %s -> %s: %w", src, finalPath, err)
	}

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
