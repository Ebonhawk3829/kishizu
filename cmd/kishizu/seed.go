package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// seedShow is a show to bootstrap, with how much has been watched.
//
// Watched counts become terminal episode state, so the tool will only ever
// consider episodes AFTER that number. This is the guard that stops it
// re-downloading something already seen.
type seedShow struct {
	Name     string
	Aliases  []string
	Watched  int // episodes already watched; download starts at Watched+1
	Max      int // total episodes, 0 when unknown
	Upcoming bool
}

// currentSeason is the user's active list with watch progress as of 2026-09-08.
var currentSeason = []seedShow{
	{
		Name:    "BLEACH: Thousand-Year Blood War - The Calamity",
		Aliases: []string{"Bleach: Sennen Kessen Hen - Kashin Tan", "BLEACH Sennen Kessen-hen", "BLEACH Thousand Year Blood War"},
		Watched: 7, Max: 10,
	},
	{
		Name:    "Clevatess Season 2",
		Aliases: []string{"Clevatess II: Majuu no Ou to Itsuwari no Yuusha Denshou", "Clevatess II", "Clevatess 2nd Season"},
		Watched: 9, Max: 13,
	},
	{
		Name:    "Mushoku Tensei: Jobless Reincarnation Season 3",
		Aliases: []string{"Mushoku Tensei III: Isekai Ittara Honki Dasu", "Mushoku Tensei III", "Mushoku Tensei S3"},
		Watched: 11, Max: 14,
	},
	{
		Name:    "Re:ZERO -Starting Life in Another World- Season 4",
		Aliases: []string{"Re:Zero kara Hajimeru Isekai Seikatsu 4th Season", "Re:Zero kara Hajimeru Isekai Seikatsu 4", "Re ZERO Starting Life in Another World"},
		Watched: 15, Max: 19,
	},
	{
		Name:    "Smoking Behind the Supermarket with You",
		Aliases: []string{"Super no Ura de Yani Suu Futari"},
		Watched: 9, Max: 12,
	},
	{
		Name:    "Tomb Raider King",
		Aliases: []string{"Dogul Wang"},
		Watched: 9, Max: 12,
	},
	{
		Name:    "You and I Are Polar Opposites Season 2",
		Aliases: []string{"Kimikoi to Seiyaku", "You and I Are Polar Opposites 2nd Season"},
		Watched: 10, Max: 13,
	},
}

// upcomingSeason is the user's planned list. No watch progress, and air dates
// come from animeschedule where available.
var upcomingSeason = []seedShow{
	{Name: "Black Clover Season 2", Upcoming: true},
	{Name: "Chainsaw Man: Shikaku-hen", Upcoming: true},
	{Name: "Demon Slayer: Kimetsu no Yaiba Infinity Castle Part 2", Upcoming: true},
	{Name: "Diamond no Ace act II: Second Season Part 2", Upcoming: true},
	{Name: "Dragon Ball Super: Beerus", Upcoming: true},
	{Name: "Dungeon ni Deai wo Motomeru no wa Machigatteiru Darou ka VI", Upcoming: true},
	{Name: "Firefly Wedding", Upcoming: true},
	{Name: "Gachiakuta Season 2", Upcoming: true},
	{Name: "One Punch Man Season 3 Part 2", Upcoming: true},
	{Name: "Overgeared", Upcoming: true},
	{Name: "PSYREN", Upcoming: true},
	{Name: "Shangri-La Frontier Season 3", Upcoming: true},
	{Name: "The Ramparts of Ice Season 2", Upcoming: true},
	{Name: "The Vermilion Mask", Upcoming: true},
	{Name: "Witch on the Holy Night", Upcoming: true},
}

// seedShows populates the database from the hardcoded season lists.
//
// Watched episodes are marked watched (terminal), so only episodes after the
// watched count are ever considered. Upcoming shows are matched against
// animeschedule to show estimated air dates where known.
func seedShows(st *store.Store) error {
	// Fetch the schedule once for air-date estimates. Failure is not fatal:
	// the shows still get created, just without dates.
	var sched []schedule.Entry
	if entries, err := schedule.Fetch(nil); err != nil {
		fmt.Printf("note: could not fetch schedule (%v)\n", err)
	} else {
		sched = entries
		fmt.Printf("schedule: %d shows\n", len(sched))
	}

	fmt.Println("\n=== CURRENT")
	for _, sd := range currentSeason {
		if err := seedOne(st, sd, sched); err != nil {
			return err
		}
	}

	fmt.Println("\n=== UPCOMING")
	for _, sd := range upcomingSeason {
		if err := seedOne(st, sd, sched); err != nil {
			return err
		}
	}
	return nil
}

func seedOne(st *store.Store, sd seedShow, sched []schedule.Entry) error {
	sh, err := st.CreateShow(sd.Name, sd.Aliases, sd.Max)
	if err != nil {
		// Already present (unique constraint): report and continue rather than
		// aborting the whole seed.
		if existing, _ := st.GetShowByName(sd.Name); existing != nil {
			fmt.Printf("  %-52s already present\n", truncate(sd.Name, 50))
			return nil
		}
		return err
	}
	if err := st.SetSource(sh.ID, "manual"); err != nil {
		return err
	}

	// Mark everything up to the watched count as watched. Terminal state, so
	// those episodes are never re-grabbed.
	for i := 1; i <= sd.Watched; i++ {
		if err := st.UpsertEpisode(sh.ID, i, episode.Watched, "", ""); err != nil {
			return err
		}
	}

	line := fmt.Sprintf("  %-52s", truncate(sd.Name, 50))
	if sd.Watched > 0 {
		line += fmt.Sprintf(" watched %d", sd.Watched)
		if sd.Max > 0 {
			line += fmt.Sprintf("/%d", sd.Max)
		}
		line += fmt.Sprintf(" -> next %d", sd.Watched+1)
	}

	// Match on every alias, not just the canonical name: the schedule uses
	// romaji, so the English name alone often scores zero.
	aliases := append([]string{sd.Name}, sd.Aliases...)
	if e := schedule.FindWithAliases(sched, aliases); e != nil && !e.AirsAt.IsZero() {
		line += fmt.Sprintf("  | next ep %d airs %s", e.NextEp, e.AirsAt.Format("Mon 2 Jan 15:04"))
		// Store the cadence weekday for the "on break or broken?" diagnostic.
		if err := st.SetCadence(sh.ID, int(e.AirsAt.Weekday()), "animeschedule", e.AirsAt); err != nil {
			return err
		}
	} else if sd.Upcoming {
		line += "  | not on schedule yet"
	}
	fmt.Println(line)
	return nil
}

// listShowsWithSchedule prints tracked shows alongside any known air dates.
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
		}
		fmt.Println(line)
	}
	return nil
}

var _ = time.Now
