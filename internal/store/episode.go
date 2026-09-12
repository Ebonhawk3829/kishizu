package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// baseName extracts the filename from a path that may come from any OS.
//
// filepath.Base is not enough: the server runs on Linux and the watch signal
// arrives from the user's PC, which may send Windows paths whose separator is
// a backslash. Splitting on both keeps matching independent of where the file
// is mounted.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Episode is one (show, local episode number) pair and its lifecycle position.
type Episode struct {
	ShowID       int64
	Number       int
	State        episode.State
	InfoHash     string
	ReleaseTitle string
	FilePath     string
	// AirsAt is when this episode is expected to air, projected from the
	// schedule. Nil when unknown. Distinguishes "hasn't aired yet" from
	// "should have aired but untouched".
	AirsAt       *time.Time
	DownloadedAt *time.Time
	WatchedAt    *time.Time
}

// GetEpisode loads an episode. Returns (nil, nil) when it does not exist yet,
// which is the normal case for an episode we have never seen.
func (s *Store) GetEpisode(showID int64, number int) (*Episode, error) {
	row := s.db.QueryRow(`SELECT show_id, number, state, infohash, release_title,
		file_path, airs_at, downloaded_at, watched_at FROM episode WHERE show_id = ? AND number = ?`,
		showID, number)

	var e Episode
	var state, hash, title, path sql.NullString
	var airs, dl, watched sql.NullString
	err := row.Scan(&e.ShowID, &e.Number, &state, &hash, &title, &path, &airs, &dl, &watched)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.State = episode.ParseState(state.String)
	e.InfoHash = hash.String
	e.ReleaseTitle = title.String
	e.FilePath = path.String
	e.AirsAt = parseTime(airs)
	e.DownloadedAt = parseTime(dl)
	e.WatchedAt = parseTime(watched)
	return &e, nil
}

// UpsertEpisode creates the episode if absent, otherwise advances its state.
//
// The latch is enforced here rather than at the call site: it is not possible to
// move an episode backwards through the lifecycle, so a deleted episode cannot
// be resurrected by a late release. See episode.State.Advance.
func (s *Store) UpsertEpisode(showID int64, number int, next episode.State, infohash, releaseTitle string) error {
	existing, err := s.GetEpisode(showID, number)
	if err != nil {
		return err
	}

	if existing == nil {
		_, err := s.db.Exec(`INSERT INTO episode (show_id, number, state, infohash, release_title)
			VALUES (?, ?, ?, ?, ?)`, showID, number, string(next), infohash, releaseTitle)
		return err
	}

	advanced, ok := existing.State.Advance(next)
	if !ok {
		// Refusing is correct, not an error: a repeated or backwards event
		// should not fail the caller.
		return nil
	}

	q := `UPDATE episode SET state = ?`
	args := []any{string(advanced)}

	switch advanced {
	case episode.Downloaded:
		q += `, downloaded_at = datetime('now')`
	case episode.Watched:
		q += `, watched_at = datetime('now')`
	case episode.Deleted:
		// Clear the path: the file is gone, and a stale path would make the
		// UI show something that no longer exists.
		q += `, file_path = NULL`
	}
	if infohash != "" {
		q += `, infohash = ?`
		args = append(args, infohash)
	}
	if releaseTitle != "" {
		q += `, release_title = ?`
		args = append(args, releaseTitle)
	}
	q += ` WHERE show_id = ? AND number = ?`
	args = append(args, showID, number)

	_, err = s.db.Exec(q, args...)
	return err
}

// MarkWatchedUpTo latches episodes 1..n as watched.
//
// For first runs of a newly added show: the user has already seen earlier
// episodes, and without this the listener would grab everything from episode
// 1. Watched is terminal, so those episodes are never grabbed.
//
// Only episodes with no state yet are touched: an episode already downloading
// or downloaded is left alone, since deleting or re-latching it would be
// surprising.
func (s *Store) MarkWatchedUpTo(showID int64, n int) (int, error) {
	if n < 1 {
		return 0, nil
	}
	// Latch by STATE, not by row existence. Air-date projection creates a
	// "wanted" row for every episode up to the schedule point, so nearly every
	// episode already has a row — skipping those would make this a no-op for
	// exactly the common case.
	rows, err := s.db.Query(
		`SELECT number, state FROM episode WHERE show_id = ? AND number <= ?`, showID, n)
	if err != nil {
		return 0, err
	}
	state := map[int]episode.State{}
	for rows.Next() {
		var num int
		var st string
		if err := rows.Scan(&num, &st); err != nil {
			rows.Close()
			return 0, err
		}
		state[num] = episode.ParseState(st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	marked := 0
	for i := 1; i <= n; i++ {
		// Leave anything already in flight or consumed alone: re-latching a
		// downloading or downloaded episode would be surprising, and watched
		// or deleted are terminal anyway.
		if cur, ok := state[i]; ok && cur != episode.Wanted {
			continue
		}
		if err := s.UpsertEpisode(showID, i, episode.Watched, "", ""); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

// SetEpisodeState forces an episode into a state, bypassing the latch.
//
// Used for manual correction: an episode the user obtained outside kishizu
// should read as "downloaded" even though the tool never fetched it. The latch
// cannot express that, because it only moves forward through the lifecycle.
//
// Callers are responsible for refusing terminal rewinds; this writes what it
// is told.
func (s *Store) SetEpisodeState(showID int64, number int, next episode.State) error {
	existing, err := s.GetEpisode(showID, number)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err := s.db.Exec(`INSERT INTO episode (show_id, number, state)
			VALUES (?, ?, ?)`, showID, number, string(next))
		return err
	}
	q := `UPDATE episode SET state = ?`
	args := []any{string(next)}
	switch next {
	case episode.Downloaded:
		q += `, downloaded_at = datetime('now')`
	case episode.Watched:
		q += `, watched_at = datetime('now')`
	case episode.Deleted:
		q += `, file_path = NULL`
	}
	q += ` WHERE show_id = ? AND number = ?`
	args = append(args, showID, number)
	_, err = s.db.Exec(q, args...)
	return err
}

// SetFilePath records where an episode landed on disk.
func (s *Store) SetFilePath(showID int64, number int, path string) error {
	_, err := s.db.Exec(`UPDATE episode SET file_path = ? WHERE show_id = ? AND number = ?`,
		path, showID, number)
	return err
}

// Unlatch resets an episode to wanted, so the user can deliberately
// re-download something they already watched, deleted, or lost.
//
// This is the ONLY way an episode leaves a terminal state, and it is always
// user-initiated. Automatic paths must never call it.
//
// Missing episodes can also be unlatched: they are not terminal, but the user
// asking for a re-grab is exactly the "accident, redownload" case.
func (s *Store) Unlatch(showID int64, number int) error {
	e, err := s.GetEpisode(showID, number)
	if err != nil {
		return err
	}
	if e == nil {
		return fmt.Errorf("episode %d not found for show %d", number, showID)
	}
	if !e.State.Terminal() && e.State != episode.Missing {
		return nil // already grabbable; nothing to do
	}
	_, err = s.db.Exec(`UPDATE episode SET state = ?, file_path = NULL, infohash = NULL,
		release_title = NULL, downloaded_at = NULL, watched_at = NULL
		WHERE show_id = ? AND number = ?`, string(episode.Wanted), showID, number)
	return err
}

// EpisodesForShow returns every episode row for a show, ordered by number.
func (s *Store) EpisodesForShow(showID int64) ([]*Episode, error) {
	rows, err := s.db.Query(`SELECT show_id, number, state, infohash, release_title,
		file_path, airs_at, downloaded_at, watched_at FROM episode WHERE show_id = ? ORDER BY number`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Episode
	for rows.Next() {
		var e Episode
		var state, hash, title, path sql.NullString
		var airs, dl, watched sql.NullString
		if err := rows.Scan(&e.ShowID, &e.Number, &state, &hash, &title, &path, &airs, &dl, &watched); err != nil {
			return nil, err
		}
		e.State = episode.ParseState(state.String)
		e.InfoHash = hash.String
		e.ReleaseTitle = title.String
		e.FilePath = path.String
		e.AirsAt = parseTime(airs)
		e.DownloadedAt = parseTime(dl)
		e.WatchedAt = parseTime(watched)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// FindByFileName resolves a filename to the episode that owns it, by exact
// match on the base name of the stored path.
//
// This is the watch signal's primary path, and it is deliberately exact.
// kishizu named the file itself when the download completed, so the name is
// known — there is nothing to infer. Fuzzy matching here would be guessing
// at a question already answered, and a wrong guess deletes a file the user
// may still want.
//
// Comparison is on base name only, because the path mpv sees on the user's
// PC differs from the path the server stored: Syncthing moves the file, and
// the two machines mount it differently.
func (s *Store) FindByFileName(name string) (*Episode, error) {
	if name == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT show_id, number, state, infohash, release_title,
		file_path, airs_at, downloaded_at, watched_at FROM episode
		WHERE file_path IS NOT NULL AND file_path != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var e Episode
		var state, hash, title, path sql.NullString
		var airs, dl, watched sql.NullString
		if err := rows.Scan(&e.ShowID, &e.Number, &state, &hash, &title, &path, &airs, &dl, &watched); err != nil {
			return nil, err
		}
		if baseName(path.String) != name {
			continue
		}
		e.State = episode.ParseState(state.String)
		e.InfoHash = hash.String
		e.ReleaseTitle = title.String
		e.FilePath = path.String
		e.AirsAt = parseTime(airs)
		e.DownloadedAt = parseTime(dl)
		e.WatchedAt = parseTime(watched)
		return &e, nil
	}
	return nil, rows.Err()
}

// EpisodesByState returns every episode in a given state across all shows.
func (s *Store) EpisodesByState(st episode.State) ([]*Episode, error) {
	rows, err := s.db.Query(`SELECT show_id, number, state, infohash, release_title,
		file_path, airs_at, downloaded_at, watched_at FROM episode WHERE state = ? ORDER BY show_id, number`,
		string(st))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Episode
	for rows.Next() {
		var e Episode
		var state, hash, title, path sql.NullString
		var airs, dl, watched sql.NullString
		if err := rows.Scan(&e.ShowID, &e.Number, &state, &hash, &title, &path, &airs, &dl, &watched); err != nil {
			return nil, err
		}
		e.State = episode.ParseState(state.String)
		e.InfoHash = hash.String
		e.ReleaseTitle = title.String
		e.FilePath = path.String
		e.AirsAt = parseTime(airs)
		e.DownloadedAt = parseTime(dl)
		e.WatchedAt = parseTime(watched)
		out = append(out, &e)
	}
	return out, rows.Err()
}
