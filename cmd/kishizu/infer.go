package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
)

// inferOffsets derives group offsets from the schedule and feed for every
// show. Results are reviewable and resettable per show in the UI.
func inferOffsets(ctx context.Context, st *store.Store, indexer *nyaa.Client) error {
	shows, err := st.ListShows()
	if err != nil {
		return err
	}
	for _, sh := range shows {
		urls := indexer.FeedURLsFor(sh.CanonicalName, sh.Aliases)
		items, failed, err := indexer.FetchAll(ctx, urls)
		if err != nil {
			fmt.Printf("  %-50s feed unavailable: %v\n", truncate(sh.CanonicalName, 50), err)
			continue
		}
		if failed > 0 {
			fmt.Printf("  %-50s %d of %d feeds failed\n", truncate(sh.CanonicalName, 50), failed, len(urls))
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
