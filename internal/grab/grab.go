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
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/naming"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Reconciler matches finished downloads to episodes and finalises them.
type Reconciler struct {
	st *store.Store
	// Staging is where the downloader drops completed files, as kishizu sees
	// it. One directory per show, so a file's show is known from its location
	// and only its episode number has to be read from the name.
	Staging string
	// Library is the root files are moved into.
	Library string
	// Scheme decides the filename and directory layout. Never nil after New.
	Scheme *naming.Scheme
	// PruneUnselected, when set and true, deletes staged files that were not
	// selected for tracking once a pack is complete. Nil means off.
	//
	// Off by default because it deletes data. It is opt-in per deployment,
	// and only ever removes files inside staging that resolved to no
	// in-flight episode.
	PruneUnselected *bool
	// StallAfter is how long an episode may sit in "downloading" with no
	// file appearing in staging before it is reported as stalled. Zero means
	// the default of 48h.
	//
	// Without this a dead swarm is invisible forever: the episode is not
	// hunting (so it is never re-grabbed), it never goes "no release found"
	// (that is only for "wanted"), and nothing alerts. The staging directory
	// is the only witness to a download, so its silence is the signal.
	StallAfter time.Duration
	// OnStall, when set, is called for each stalled episode. The reconciler
	// does not alert itself: notifying is the caller's concern, and the
	// caller may want to batch or debounce.
	OnStall func(show, title string, ep int)
}

// New builds a Reconciler.
//
// staging and library are both required: staging is scanned for completed
// files, library is where they are moved to. No downloader client is
// needed — the done-script removes torrents on completion, so the staging
// directory is the durable record, not the torrent list.
func New(st *store.Store, staging, library string) *Reconciler {
	return NewWithScheme(st, staging, library, nil)
}

// NewWithScheme builds a Reconciler that files episodes using a specific
// naming scheme. A nil scheme means kishizu's own layout.
func NewWithScheme(st *store.Store, staging, library string, s *naming.Scheme) *Reconciler {
	if s == nil {
		s, _ = naming.Resolve(naming.PresetKishizu, "", nil)
	}
	return &Reconciler{st: st, Staging: staging, Library: library, Scheme: s, StallAfter: 48 * time.Hour}
}

// seasonOf is the season number to file an episode under.
//
// kishizu tracks one cour at a time, so a show is always its own season 1:
// there is no season number in the database because there is never more than
// one in play. The naming schemes that want one (sonarr, plex) get 1, which
// is what those tools call a show's first tracked season.
func seasonOf(_ *store.Show) int { return 1 }

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
		// Self-heal first: an episode whose file is already in the library
		// but whose database write never landed is stuck forever otherwise.
		r.healLibrary(sh, eps)
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
		dir := filepath.Join(r.Staging, release.Sanitise(sh.CanonicalName))
		files, err := mediaFiles(dir)
		if err != nil {
			// Missing directory is normal: nothing has completed for this
			// show yet.
			if os.IsNotExist(err) {
				r.reportStalled(sh, inFlight, nil)
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
		// The raw-number fallback is for adopted seasons only: the release was
		// chosen by hand, so the user confirmed which file is which episode.
		// It is keyed on the show's SOURCE, not on offsets being empty — an
		// airing show whose offsets were cleared (Reset in the UI) must not
		// have raw numbers trusted for it, or a file numbered 47 files against
		// a nonexistent row while the episode it should resolve to stays
		// downloading.
		trustRaw := sh.Source == store.SourceSeaDex
		var unresolved []string
		for _, f := range files {
			num, ok := episodeOf(f, offsets, trustRaw)
			if !ok {
				unresolved = append(unresolved, f)
				debug.Log("%s: cannot resolve episode from %q", sh.CanonicalName, filepath.Base(f))
				continue
			}
			ep, ok := inFlight[num]
			if !ok {
				// Completed, but not an episode we are waiting for.
				unresolved = append(unresolved, f)
				debug.Log("%s: %s resolves to ep%d, not downloading", sh.CanonicalName, filepath.Base(f), num)
				continue
			}
			debug.Log("%s ep%d: found %s, finalising", sh.CanonicalName, num, filepath.Base(f))
			if err := r.finalise(sh, ep, f); err != nil {
				log.Printf("grab: %s ep%d: %v", sh.CanonicalName, num, err)
			}
		}
		// Once every episode in flight has landed, the pack is complete and
		// anything left in staging was not selected. Prune it.
		r.pruneUnselected(sh, dir, unresolved, inFlight)
		// A download that has produced nothing for days is a dead swarm, and
		// without this check it is invisible: not hunting, never re-grabbed,
		// never alerted.
		r.reportStalled(sh, inFlight, files)
	}
	return nil
}

// pruneUnselected deletes staged files that were not selected for tracking,
// once every episode in flight has been finalised.
//
// A magnet link carries no file list, so an adoption downloads the whole
// release and the checkboxes in the review screen decide which files become
// episodes — not which files arrive. Without this, the unselected extras
// (NCOP, NCED, OVAs) sit in staging forever, holding disk for files nobody
// asked for.
//
// It only runs when the pack is COMPLETE. Deleting while episodes are still
// in flight would race the download: a file that has not finished writing
// yet resolves to nothing, and pruning it would destroy an episode the user
// is waiting for.
//
// The user's selection is the authority. If they ticked the wrong boxes, the
// wrong files are kept — that is their call, and guessing on their behalf
// would be worse.
func (r *Reconciler) pruneUnselected(sh *store.Show, dir string, unresolved []string, inFlight map[int]*store.Episode) {
	if len(unresolved) == 0 || r.PruneUnselected == nil || !*r.PruneUnselected {
		return
	}
	// Re-read: finalise has run since inFlight was built, so an episode that
	// was downloading may now be downloaded.
	eps, err := r.st.EpisodesForShow(sh.ID)
	if err != nil {
		// Logged rather than silently skipped: pruning deletes files, and a
		// database error here must not look like "nothing to prune".
		log.Printf("grab: prune re-read %s: %v", sh.CanonicalName, err)
		return
	}
	for _, ep := range eps {
		if _, was := inFlight[ep.Number]; was && ep.State == episode.Downloading {
			// Still waiting on something: not complete, do not prune.
			debug.Log("%s: ep%d still downloading, not pruning", sh.CanonicalName, ep.Number)
			return
		}
	}

	for _, f := range unresolved {
		// Never delete outside staging: a corrupted path must not take a
		// library file with it.
		abs, err := filepath.Abs(f)
		if err != nil {
			continue
		}
		root, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			log.Printf("grab: refusing to prune %q: outside staging %q", abs, root)
			continue
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			log.Printf("grab: prune %s: %v", abs, err)
			continue
		}
		log.Printf("grab: pruned unselected %s", filepath.Base(abs))
	}
	// Drop the directories the pack arrived in, now that they are empty.
	pruneEmptyDirs(dir)
}

// pruneEmptyDirs removes empty directories under root, deepest first.
//
// A pack arrives as its own directory, sometimes with an Extras subdirectory.
// Leaving them behind means the staging tree fills with empty folders that
// the daily cleanup has to find.
func pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return nil
		}
		dirs = append(dirs, path)
		return nil
	})
	// Deepest first, so a parent is empty by the time it is reached.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil || len(entries) > 0 {
			continue
		}
		_ = os.Remove(d)
	}
}

// reportStalled flags episodes that have been "downloading" for longer than
// the stall window with no file in staging to show for it.
//
// The state is deliberately left alone. Rewinding a downloading episode to
// wanted would re-grab it, and the first grab may still be seeding or
// slow rather than dead — the user decides, via unlatch, whether to retry.
// What was missing was the signal, not the state change: nothing anywhere
// looked at how long an episode had been in flight, so a dead swarm sat
// silently in "downloading" forever.
//
// The grab time is downloaded_at, which UpsertEpisode stamps when the magnet
// is handed off. An episode with no timestamp (adopted seasons write none)
// is skipped: there is no evidence of when it started, so there is nothing
// to measure.
func (r *Reconciler) reportStalled(sh *store.Show, inFlight map[int]*store.Episode, files []string) {
	if r.StallAfter <= 0 || len(inFlight) == 0 {
		return
	}
	window := r.StallAfter
	now := time.Now()
	for num, ep := range inFlight {
		if ep.DownloadedAt == nil || now.Sub(*ep.DownloadedAt) < window {
			continue
		}
		// A file for this episode may have landed but not yet resolved —
		// only report when nothing in staging could be it. Offsets are not
		// consulted here: a pending file is identified by its raw number,
		// which is a superset of what the offset-corrected resolution would
		// accept, so nothing pending is missed.
		pending := false
		for _, f := range files {
			if n, ok := episodeOf(f, nil, true); ok && n == ep.Number {
				pending = true
				break
			}
		}
		if pending {
			continue
		}
		log.Printf("grab: %s ep%d stalled: downloading since %s with no file in staging",
			sh.CanonicalName, num, ep.DownloadedAt.Format(time.DateOnly))
		if r.OnStall != nil {
			r.OnStall(sh.CanonicalName, ep.ReleaseTitle, num)
		}
	}
}

// episodeOf resolves a staged file to an episode number.
//
// The raw number comes from the filename; the group's offset corrects it to
// the local numbering. Everything else in the name — resolution, codec,
// service, subtitle tags — is noise and is ignored.
//
// trustRaw enables the raw-number fallback for shows with no offsets. It is
// the caller's decision because the condition is about the show, not the
// offsets: an adopted season (source seadex) had its mapping confirmed by the
// user at adoption time, so the raw number is right. An airing show whose
// offsets were cleared is NOT that case — trusting raw numbers there files
// against nonexistent rows and leaves the real episode stuck in flight.
func episodeOf(path string, offsets map[string]int, trustRaw bool) (int, bool) {
	// Strip the extension before parsing: the trailing-group regex otherwise
	// captures "ToonsHub.mkv" as the group name, which matches no offset.
	base := filepath.Base(path)
	rel := release.Parse(strings.TrimSuffix(base, filepath.Ext(base)))
	raw := rel.RawEpisode()
	if raw == 0 {
		return 0, false
	}
	if len(offsets) == 0 {
		// No offsets known. Only trust the raw number when the caller says the
		// mapping was confirmed by hand — see the note above.
		if !trustRaw || raw <= 0 {
			return 0, false
		}
		return raw, true
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

// mediaFiles returns the video files under dir, at any depth.
//
// Recursive because a torrent is not always a flat list of files: a season
// pack arrives as one directory named after the release, with the episodes
// inside it. Reading only the top level found nothing for such a torrent, so
// a completed download sat in staging forever and its episode stayed
// "downloading".
//
// Depth is not bounded, but the reconciler only ever acts on a file that
// resolves to an episode already in flight, so extra files are left alone
// rather than moved.
func mediaFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if isMedia(d.Name()) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// finalise renames the finished torrent into the library layout and marks the
// episode downloaded.
//
// The target comes from the naming scheme, not from a format string baked in
// here, so a configured layout is honoured everywhere rather than only on the
// write path. The extension comes from the source file.
//
// The two database writes are a transaction, so the episode can never be
// marked downloaded without its path (or vice versa) — a half-finalised
// episode would be invisible to both the reconciler and the missing-file
// check forever.
func (r *Reconciler) finalise(sh *store.Show, ep *store.Episode, src string) error {
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		return fmt.Errorf("no file extension on %q", src)
	}

	finalPath := r.Scheme.Path(r.Library, sh.CanonicalName, seasonOf(sh), ep.Number, ext)

	// The library folder is not assumed to exist: a daily cleanup job removes
	// show folders left with no media files, so it is recreated as needed.
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o775); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(finalPath), err)
	}

	// Same filesystem, so this is an atomic rename rather than a copy. The
	// file appears in the library complete and correctly named, or not at all.
	if err := os.Rename(src, finalPath); err != nil {
		// EXDEV means staging and the library are on different filesystems,
		// which the README warns about. The raw error name means nothing to
		// someone who did not write the code, so say what to do instead.
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("staging and library must be on the same filesystem (see the README note about one mount): %s -> %s", src, finalPath)
		}
		return fmt.Errorf("move %s -> %s: %w", src, finalPath, err)
	}

	if err := r.st.FinaliseEpisode(sh.ID, ep.Number, finalPath); err != nil {
		return err
	}
	log.Printf("grab: %s ep%d ready at %s", sh.CanonicalName, ep.Number, finalPath)
	return nil
}

// healLibrary finds episodes stuck in "downloading" whose final-form file is
// already in the library — the residue of a crash between the rename and the
// database write — and completes them. Without this, such an episode is
// invisible forever: the reconciler only scans staging, and the file is no
// longer there.
func (r *Reconciler) healLibrary(sh *store.Show, eps []*store.Episode) {
	for _, ep := range eps {
		if ep.State != episode.Downloading {
			continue
		}
		// Every extension the scheme could have written, since the exact one
		// is not recorded anywhere. Derived from the scheme rather than
		// hardcoded, so a custom layout heals as correctly as the default.
		finalPath := ""
		for _, candidate := range r.Scheme.Candidates(r.Library, sh.CanonicalName, seasonOf(sh), ep.Number) {
			if _, err := os.Stat(candidate); err == nil {
				finalPath = candidate
				break
			}
		}
		if finalPath == "" {
			continue
		}
		if err := r.st.FinaliseEpisode(sh.ID, ep.Number, finalPath); err != nil {
			log.Printf("grab: heal %s ep%d: %v", sh.CanonicalName, ep.Number, err)
			continue
		}
		log.Printf("grab: healed %s ep%d (found %s)", sh.CanonicalName, ep.Number, finalPath)
	}
}

// isMedia reports whether a filename looks like a video file.
func isMedia(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".mov", ".webm":
		return true
	}
	return false
}

// longest, since batch extras are usually small. Real sizes would need the
// "files" length field from Transmission; the name ordering is sufficient to
