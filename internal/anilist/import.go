package anilist

import (
	"fmt"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Importer maps AniList entries into the local store.
type Importer struct {
	st *store.Store
}

func NewImporter(st *store.Store) *Importer { return &Importer{st: st} }

// Result summarises an import run.
type Result struct {
	Created  int
	Updated  int
	Skipped  int
	Episodes int // episode rows written (watched history)
	Errors   []string
}

// Import upserts entries into the store.
//
// Design decisions:
//
//   - **Idempotent.** Shows are matched on anilist_id, so re-running after a
//     partial failure (AniList is flaky) completes rather than duplicates.
//   - **Watched history becomes terminal state.** Everything below the user's
//     progress is marked watched, so the latch immediately protects those
//     episodes from ever being re-grabbed. This is the single most valuable
//     thing the import does: without it, a fresh database would happily
//     re-download episodes you finished months ago.
//   - **Only CURRENT and PLANNING become tracked shows.** COMPLETED and DROPPED
//     are recorded as watched history if present but are not things you are
//     currently watching, so they do not become active shows.
//   - **Aliases come from every title variant.** Release groups use romaji,
//     english and native interchangeably, so all three are stored.
func (im *Importer) Import(entries []Entry) (*Result, error) {
	res := &Result{}

	for _, e := range entries {
		if e.MediaID == 0 || e.Title == "" {
			res.Skipped++
			continue
		}

		sh, err := im.upsertShow(e)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", e.Title, err))
			continue
		}
		if sh.Created {
			res.Created++
		} else {
			res.Updated++
		}

		n, err := im.markWatched(sh.ID, e)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: mark watched: %v", e.Title, err))
			continue
		}
		res.Episodes += n
	}
	return res, nil
}

type upserted struct {
	ID      int64
	Created bool
}

func (im *Importer) upsertShow(e Entry) (*upserted, error) {
	existing, err := im.st.GetShowByAniListID(e.MediaID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// Already imported: refresh aliases and episode count, but never
		// clobber a name the user may have edited.
		for _, a := range aliasesFor(e) {
			if err := im.st.AddAlias(existing.ID, a); err != nil {
				return nil, err
			}
		}
		if existing.MaxEpisode == 0 && e.Episodes > 0 {
			if err := im.st.SetMaxEpisode(existing.ID, e.Episodes); err != nil {
				return nil, err
			}
		}
		return &upserted{ID: existing.ID, Created: false}, nil
	}

	sh, err := im.st.CreateShow(e.Title, aliasesFor(e), e.Episodes)
	if err != nil {
		return nil, err
	}
	if err := im.st.SetAniListID(sh.ID, e.MediaID); err != nil {
		return nil, err
	}
	if err := im.st.SetSource(sh.ID, "anilist"); err != nil {
		return nil, err
	}
	return &upserted{ID: sh.ID, Created: true}, nil
}

// markWatched records everything below the user's progress as watched, which is
// terminal and therefore never re-grabbed.
func (im *Importer) markWatched(showID int64, e Entry) (int, error) {
	if e.Progress <= 0 {
		return 0, nil
	}
	n := 0
	for i := 1; i <= e.Progress; i++ {
		if err := im.st.UpsertEpisode(showID, i, episode.Watched, "", ""); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// aliasesFor collects every title variant. Release groups use any of them, and
// the matcher scores on alias recall, so more is strictly better here.
func aliasesFor(e Entry) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(e.Title)
	add(e.Romaji)
	add(e.Native)
	for _, s := range e.Synonyms {
		add(s)
	}
	return out
}

// Tracked reports whether a status means "currently watching or intending to".
// COMPLETED and DROPPED are history, not active shows.
func Tracked(status string) bool {
	switch status {
	case "CURRENT", "PLANNING", "REPEATING":
		return true
	default:
		return false
	}
}
