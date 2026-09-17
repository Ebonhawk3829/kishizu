package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// backfillSlugs attaches animeschedule slugs to shows that predate them, and
// fills in whatever the schedule page knows that the database does not.
//
// The mapping file is hand-maintained (see slugs.yaml): resolving a show name
// to a slug needs a judgement about which season is meant, and the site's own
// search is not reliable enough to do it automatically. Doing it once by hand
// is cheaper than making the tool guess every day.
//
// For each mapped show this:
//   - records the slug, so the daily refresh matches exactly instead of
//     fuzzy-matching titles
//   - fills max_episode when the database has 0 and the site knows the count
//   - adds every alternative name as an alias, tagged with its provenance
//
// It never overwrites a max_episode the user set deliberately, and it never
// removes anything. Safe to re-run.
func backfillSlugs(st *store.Store, path string) error {
	mapping, err := loadSlugMapping(path)
	if err != nil {
		return err
	}

	shows, err := st.ListShows()
	if err != nil {
		return fmt.Errorf("list shows: %w", err)
	}

	slugged, enriched, skipped := 0, 0, 0
	for _, sh := range shows {
		slug, ok := mapping[sh.CanonicalName]
		if !ok {
			skipped++
			continue
		}
		if sh.Slug != slug {
			if err := st.SetSlug(sh.ID, slug); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: set slug: %v\n", sh.CanonicalName, err)
				continue
			}
			sh.Slug = slug
			slugged++
		}

		info, err := schedule.FetchShow(nil, slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: fetch %s: %v\n", sh.CanonicalName, slug, err)
			continue
		}

		// Only fill a season length we do not have. A non-zero max was either
		// set by the user or learned from a real release, and both outrank the
		// site's estimate. SeasonLength (not Episodes) because a film reports
		// "1", which would cap the season after one download.
		if n := info.SeasonLength(); n > 0 && sh.MaxEpisode == 0 {
			if err := st.SetMaxEpisode(sh.ID, n); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: set max: %v\n", sh.CanonicalName, err)
			} else {
				sh.MaxEpisode = n
			}
		}

		added := 0
		for _, a := range info.Aliases() {
			if err := st.AddAliasFrom(sh.ID, a, "schedule"); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: add alias %q: %v\n", sh.CanonicalName, a, err)
				continue
			}
			added++
		}

		// The page's Release Time is episode 1's air slot, and for an unaired
		// show it is the only air information available. Only fill it when the
		// show has no schedule point yet, so a live season's real next-episode
		// time is never overwritten with its premiere date.
		if !info.AirsAt.IsZero() {
			if _, at, _ := st.NextEpisode(sh.ID); at == nil {
				if err := st.SetNextEpisode(sh.ID, 1, info.AirsAt); err != nil {
					fmt.Fprintf(os.Stderr, "  %s: set air time: %v\n", sh.CanonicalName, err)
				} else {
					_ = st.ProjectAirDates(sh.ID)
				}
			}
		}

		if info.ImageURL != "" && sh.ImageURL == "" {
			_ = st.SetImageURL(sh.ID, info.ImageURL)
		}

		enriched++
		fmt.Printf("  %-52s %-46s max %-3d +%d aliases\n",
			truncate(sh.CanonicalName, 50), slug, sh.MaxEpisode, added)
	}

	fmt.Printf("\nbackfill: %d slugged, %d enriched, %d without a mapping\n",
		slugged, enriched, skipped)
	return nil
}

// splitMapping separates a "name: slug" line into its two halves.
//
// The split is on the LAST colon, not the first, because show names contain
// colons: "BLEACH: Thousand-Year Blood War - The Calamity" and
// "Re:ZERO -Starting Life in Another World- Season 4" both do. A quoted name is
// unquoted after splitting, which is why slugs.yaml quotes those entries.
func splitMapping(line string) (name, slug string) {
	i := strings.LastIndex(line, ":")
	if i <= 0 {
		return "", ""
	}
	name = strings.TrimSpace(line[:i])
	slug = strings.TrimSpace(line[i+1:])
	name = strings.Trim(name, `"'`)
	return name, slug
}

// loadSlugMapping reads the hand-maintained name -> slug file.
//
// The format is a flat "name: slug" map under a "shows:" key, parsed by hand to
// match the rest of the project's approach to its own config files. Quoted keys
// are supported because show names contain colons.
func loadSlugMapping(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	inShows := false
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.Contains(line, ": ") {
			// A bare key: either the "shows:" header or the start of another
			// section. Only the shows section is a name->slug map.
			inShows = strings.TrimSuffix(line, ":") == "shows"
			continue
		}
		if !inShows {
			continue
		}
		name, slug := splitMapping(line)
		if name != "" && slug != "" {
			out[name] = slug
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no name->slug entries found in %s", path)
	}
	return out, nil
}
