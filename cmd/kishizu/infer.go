package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
)

// inferOffsets derives group offsets from the schedule and feed for every
// show.
//
// This is the shortcut for a fresh season: instead of confirming parses by
// hand, the air times already in the database do the work. Results are
// reviewable per show and resettable from the training panel.
//
// The group order is global and set in advance, so it is read from the
// quality policy rather than taken as an argument. It used to be overridable
// with -prefer, but that flag only ever reached this function: the listener
// reads the policy directly, so the flag silently did nothing for the
// pipeline that actually grabs.
func inferOffsets(st *store.Store) error {
	shows, err := st.ListShows()
	if err != nil {
		return err
	}
	for _, sh := range shows {
		items, err := nyaa.FetchAll(nil, nyaa.FeedURLsFor(sh.CanonicalName, sh.Aliases))
		if err != nil {
			fmt.Printf("  %-50s feed unavailable: %v\n", truncate(sh.CanonicalName, 50), err)
			continue
		}
		offsets, err := train.InferOffsets(st, sh, items)
		if err != nil {
			fmt.Printf("  %-50s %v\n", truncate(sh.CanonicalName, 50), err)
			continue
		}
		if len(offsets) == 0 {
			fmt.Printf("  %-50s nothing to infer\n", truncate(sh.CanonicalName, 50))
			continue
		}
		for g, off := range offsets {
			if err := st.SetGroupOffset(sh.ID, g, off, "inferred"); err != nil {
				return err
			}
		}
		fmt.Printf("  %-50s %s\n", truncate(sh.CanonicalName, 50), formatOffsets(offsets))
	}
	return nil
}

func formatOffsets(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}
