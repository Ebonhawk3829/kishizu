package main

import (
	"fmt"
	"sort"

	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// seedShows populates the database from the hardcoded season lists.
//
// Watched episodes are marked watched (terminal), so only episodes after the
// watched count are ever considered. Upcoming shows are matched against
// animeschedule to show estimated air dates where known.
// seedFromConfig populates the database from the user's seed file.
//
// Watched episodes are marked watched (terminal), so only episodes after the
// watched count are ever considered. Every show is then matched against
// animeschedule to hold its next-episode air point where known.
func seedFromConfig(st *store.Store, shows []config.Show) error {
	for _, cs := range shows {
		sh, err := st.CreateShow(cs.Name, cs.Aliases, cs.Max)
		if err != nil {
			// Already present: update its aliases rather than skipping, so
			// editing shows.yaml actually takes effect on an existing
			// database. Skipping silently meant alias fixes never landed.
			existing, _ := st.GetShowByName(cs.Name)
			if existing == nil {
				return err
			}
			sh = existing
			for _, a := range cs.Aliases {
				if err := st.AddAlias(sh.ID, a); err != nil {
					return err
				}
			}
			if cs.Max > 0 {
				if err := st.SetMaxEpisode(sh.ID, cs.Max); err != nil {
					return err
				}
			}
		}
		// Mark everything up to the watched count as watched. Terminal state,
		// so those episodes are never re-grabbed.
		for i := 1; i <= cs.Watched; i++ {
			if err := st.UpsertEpisode(sh.ID, i, episode.Watched, "", ""); err != nil {
				return err
			}
		}
		// Matched by slug, straight from the show's own page. A seed entry
		// without one gets no air date, which is correct: there is no page
		// to read it from.
		if sh.Slug != "" {
			if info, err := schedule.FetchShow(nil, sh.Slug); err == nil &&
				info.LatestEpisode > 0 && !info.NextAirsAt.IsZero() {
				if err := st.SetNextEpisode(sh.ID, info.LatestEpisode, info.NextAirsAt); err != nil {
					return err
				}
				if err := st.ProjectAirDates(sh.ID); err != nil {
					return err
				}
				fmt.Printf("  %-52s seeded, ep %d airs %s\n",
					truncate(cs.Name, 50), info.LatestEpisode, info.NextAirsAt.Format("Mon 2 Jan 15:04"))
			} else {
				fmt.Printf("  %-52s seeded (no countdown on schedule)\n", truncate(cs.Name, 50))
			}
		} else {
			fmt.Printf("  %-52s seeded (no slug, no air date)\n", truncate(cs.Name, 50))
		}
	}
	return nil
}

func listShowsWithSchedule(st *store.Store) error {
	shows, err := st.ListShows()
	if err != nil {
		return err
	}

	sort.Slice(shows, func(i, j int) bool { return shows[i].CanonicalName < shows[j].CanonicalName })

	for _, sh := range shows {
		next := st.NextUnwatched(sh.ID)
		line := fmt.Sprintf("  %-52s next %d", truncate(sh.CanonicalName, 50), next)
		if sh.Slug != "" {
			if info, err := schedule.FetchShow(nil, sh.Slug); err == nil &&
				info.LatestEpisode > 0 && !info.NextAirsAt.IsZero() {
				line += fmt.Sprintf("  | ep %d airs %s", info.LatestEpisode, info.NextAirsAt.Format("Mon 2 Jan 15:04"))
				if err := st.SetNextEpisode(sh.ID, info.LatestEpisode, info.NextAirsAt); err != nil {
					return err
				}
				if err := st.ProjectAirDates(sh.ID); err != nil {
					return err
				}
			}
		}
		fmt.Println(line)
	}
	return nil
}

