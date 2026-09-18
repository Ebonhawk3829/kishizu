package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
)

// inferOffsets derives group offsets from the schedule and feed for every
// show, and applies the user's preferred group order.
//
// This is the shortcut for a fresh season: instead of confirming parses by
// hand, the air times already in the database do the work. Results are
// reviewable per show and resettable from the training panel.
func inferOffsets(st *store.Store, preferred string) error {
	groups := splitGroups(preferred)
	// The group order is global and set in advance; the -prefer flag overrides
	// the compiled-in default for this run.
	release.DefaultGroupOrder = groups
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

func splitGroups(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if g := strings.TrimSpace(p); g != "" {
			out = append(out, g)
		}
	}
	return out
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
