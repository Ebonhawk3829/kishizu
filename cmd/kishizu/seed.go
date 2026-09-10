package main

import (
	"fmt"
	"sort"
	"time"

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
	// Fetch the schedule once for air-date estimates. Failure is not fatal:
	// the shows still get created, just without dates.
	var sched []schedule.Entry
	if entries, err := schedule.Fetch(nil); err != nil {
		fmt.Printf("note: could not fetch schedule (%v)\n", err)
	} else {
		sched = entries
		fmt.Printf("schedule: %d shows\n", len(sched))
	}

	for _, cs := range shows {
		sh, err := st.CreateShow(cs.Name, cs.Aliases, cs.Max)
		if err != nil {
			if existing, _ := st.GetShowByName(cs.Name); existing != nil {
				fmt.Printf("  %-52s already present\n", truncate(cs.Name, 50))
				continue
			}
			return err
		}
		// Mark everything up to the watched count as watched. Terminal state,
		// so those episodes are never re-grabbed.
		for i := 1; i <= cs.Watched; i++ {
			if err := st.UpsertEpisode(sh.ID, i, episode.Watched, "", ""); err != nil {
				return err
			}
		}
		// Match on every alias, not just the canonical name: the schedule uses
		// romaji, so the English name alone often scores zero.
		aliases := append([]string{cs.Name}, cs.Aliases...)
		if e := schedule.FindWithAliases(sched, aliases); e != nil && !e.AirsAt.IsZero() {
			if err := st.SetNextEpisode(sh.ID, e.NextEp, e.AirsAt); err != nil {
				return err
			}
			if err := st.ProjectAirDates(sh.ID); err != nil {
				return err
			}
			fmt.Printf("  %-52s seeded, ep %d airs %s\n",
				truncate(cs.Name, 50), e.NextEp, e.AirsAt.Format("Mon 2 Jan 15:04"))
		} else {
			fmt.Printf("  %-52s seeded (no air date on schedule)\n", truncate(cs.Name, 50))
		}
	}
	return nil
}

func listShowsWithSchedule(st *store.Store) error {
	shows, err := st.ListShows()
	if err != nil {
		return err
	}
	sched, err := schedule.Fetch(nil)
	if err != nil {
		fmt.Printf("(schedule unavailable: %v)\n\n", err)
	}

	sort.Slice(shows, func(i, j int) bool { return shows[i].CanonicalName < shows[j].CanonicalName })

	for _, sh := range shows {
		next := 1
		eps, err := st.EpisodesForShow(sh.ID)
		if err == nil {
			for _, e := range eps {
				if e.Number >= next && (e.State == episode.Watched || e.State == episode.Deleted) {
					next = e.Number + 1
				}
			}
		}
		line := fmt.Sprintf("  %-52s next %d", truncate(sh.CanonicalName, 50), next)
		if e := schedule.FindWithAliases(sched, sh.Aliases); e != nil && !e.AirsAt.IsZero() {
			line += fmt.Sprintf("  | ep %d airs %s", e.NextEp, e.AirsAt.Format("Mon 2 Jan 15:04"))
			if err := st.SetNextEpisode(sh.ID, e.NextEp, e.AirsAt); err != nil {
				return err
			}
			if err := st.ProjectAirDates(sh.ID); err != nil {
				return err
			}
		}
		fmt.Println(line)
	}
	return nil
}

var _ = time.Now
