// Command kishizu is the dry-run entry point: fetch each tracked show's Nyaa
// feed, match every release, and print what WOULD be downloaded.
//
// Nothing is downloaded. This exists to validate matching and the duplicate
// guards against live data before any of it is wired to Transmission.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/anilist"
	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
	"github.com/Ebonhawk3829/kishizu/internal/web"
)

func main() {
	dbPath := flag.String("db", "kishizu.db", "path to the SQLite database")
	show := flag.String("show", "", "only run this show (substring match on canonical name)")
	seed := flag.Bool("seed", false, "insert the shows from the seed file")
	configPath := flag.String("config", "shows.yaml", "show seed file used by -seed")
	list := flag.Bool("list", false, "list tracked shows with next episode and air dates")
	importUser := flag.String("import-anilist", "", "bootstrap from an AniList username (one-time)")
	statuses := flag.String("statuses", "CURRENT,PLANNING", "comma-separated AniList statuses to import")
	trainName := flag.String("train", "", "train a show (substring match on canonical name)")
	ep := flag.Int("ep", 0, "episode number to train against (0 = next unwatched)")
	serve := flag.String("serve", "", "start the web UI on this address (e.g. 127.0.0.1:8098)")
	rpc := flag.String("transmission", "http://100.64.0.1:9091/transmission/rpc", "Transmission RPC endpoint")
	library := flag.String("library", "/downloads/anime", "library root for downloaded episodes, as Transmission sees it")
	keep := flag.Int("keep", 2, "recently watched episodes to keep on disk")
	interval := flag.Duration("interval", 5*time.Minute, "RSS poll interval")
	dryRun := flag.Bool("dry-run", true, "poll and decide but do not hand off to Transmission")
	ntfyURL := flag.String("ntfy", "http://100.64.0.1:8085/kishizu", "ntfy topic URL for notifications (empty disables)")
	debugOn := flag.Bool("debug", false, "verbose logging of every decision (toggleable at runtime via POST /api/debug)")
	flag.Parse()

	// Debug can also be set with KISHIZU_DEBUG=1, which the package reads at
	// init; the flag wins when given.
	if *debugOn {
		debug.Set(true)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if *serve != "" {
		srv, err := web.New(st)
		if err != nil {
			fmt.Fprintf(os.Stderr, "web: %v\n", err)
			os.Exit(1)
		}
		// The watch handler lets /api/watched sweep files after marking. The
		// library root also bounds deletion: paths outside it are refused.
		srv.SetWatch(watch.New(st, *library, *keep))
		// The listener runs alongside the UI. It is dry-run by default: it
		// polls, matches and logs decisions, but hands nothing to Transmission
		// until -dry-run=false. The user switches it on deliberately.
		go runLoop(st, *rpc, *library, *keep, *interval, *dryRun, *ntfyURL)
		if err := srv.ListenAndServe(*serve); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *seed {
		shows, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			os.Exit(1)
		}
		if err := seedFromConfig(st, shows); err != nil {
			fmt.Fprintf(os.Stderr, "seed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("seeded")
		return
	}

	// One-time bootstrap. This is the ONLY place AniList is contacted; nothing
	// at runtime depends on it.
	if *importUser != "" {
		if err := importFromAniList(st, *importUser, *statuses); err != nil {
			fmt.Fprintf(os.Stderr, "import: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *list {
		if err := listShowsWithSchedule(st); err != nil {
			fmt.Fprintf(os.Stderr, "list: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *trainName != "" {
		shows, err := st.ListShows()
		if err != nil {
			fmt.Fprintf(os.Stderr, "list shows: %v\n", err)
			os.Exit(1)
		}
		var target *store.Show
		for _, sh := range shows {
			if strings.Contains(strings.ToLower(sh.CanonicalName), strings.ToLower(*trainName)) {
				target = sh
				break
			}
		}
		if target == nil {
			fmt.Fprintf(os.Stderr, "no show matched %q\n", *trainName)
			os.Exit(1)
		}

		targetEp := *ep
		if targetEp == 0 {
			targetEp = nextUnwatched(st, target)
		}
		if err := trainShowCmd(st, target, targetEp); err != nil {
			fmt.Fprintf(os.Stderr, "train: %v\n", err)
			os.Exit(1)
		}
		return
	}

	shows, err := st.ListShows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list shows: %v\n", err)
		os.Exit(1)
	}
	if len(shows) == 0 {
		fmt.Println("no shows tracked. run with -seed to insert the starting set.")
		return
	}

	any := false
	for _, sh := range shows {
		if *show != "" && !strings.Contains(strings.ToLower(sh.CanonicalName), strings.ToLower(*show)) {
			continue
		}
		any = true
		run(st, sh)
	}
	if !any {
		fmt.Fprintf(os.Stderr, "no show matched %q\n", *show)
		os.Exit(1)
	}
}

func run(st *store.Store, sh *store.Show) {
	m, err := st.NewMatcher(sh)
	if err != nil {
		fmt.Printf("\n=== %s\n    ERROR: %v\n", sh.CanonicalName, err)
		return
	}

	fmt.Printf("\n=== %s\n", sh.CanonicalName)
	fmt.Printf("    feed: %s\n", nyaa.FeedURL(sh.CanonicalName))

	items, err := nyaa.Fetch(nil, nyaa.FeedURL(sh.CanonicalName))
	if err != nil {
		fmt.Printf("    ERROR: %v\n", err)
		return
	}
	fmt.Printf("    %d items\n", len(items))

	type candidate struct {
		ep   int
		item nyaa.Item
		why  string
	}
	var grabbed, skipped []candidate

	for _, it := range items {
		// Guard 1: infohash identity. Same release seen before, whatever
		// happened to its episode row since.
		if seen, err := st.HasSeen(it.InfoHash); err == nil && seen {
			continue
		}

		res := match.Match(m, it.Title)
		if !res.Matched {
			continue
		}
		if res.Episode == 0 {
			// Matches the show but the number is unreadable. Not a download
			// candidate; this is what the training loop surfaces.
			continue
		}

		existing, err := st.GetEpisode(sh.ID, res.Episode)
		if err != nil {
			continue
		}

		c := candidate{ep: res.Episode, item: it, why: res.Reason}

		// Guard 2: the episode latch. Terminal states (watched/deleted) are
		// never re-grabbed, which is what stops a late release resurrecting
		// something already watched and deleted.
		if existing != nil && !existing.State.MayAutoGrab() {
			c.why = fmt.Sprintf("episode %d is %s (terminal)", res.Episode, existing.State)
			skipped = append(skipped, c)
			continue
		}
		grabbed = append(grabbed, c)
	}

	sort.Slice(grabbed, func(i, j int) bool { return grabbed[i].ep < grabbed[j].ep })

	if len(grabbed) == 0 {
		fmt.Printf("    nothing to grab\n")
	} else {
		fmt.Printf("    WOULD DOWNLOAD:\n")
		for _, c := range grabbed {
			r := release.Parse(c.item.Title)
			fmt.Printf("      ep%-3d seeders=%-5d %-9s %-6s %s\n",
				c.ep, c.item.Seeders, r.Resolution, r.Codec, truncate(c.item.Title, 70))
		}
	}

	if len(skipped) > 0 {
		fmt.Printf("    SKIPPED (already handled):\n")
		for _, c := range skipped {
			fmt.Printf("      ep%-3d %s\n", c.ep, truncate(c.item.Title, 70))
		}
	}
}

// importFromAniList bootstraps the database from a user's AniList list.
//
// It is safe to re-run: shows are matched on anilist_id, so a partial import
// (AniList is flaky) completes rather than duplicating.
func importFromAniList(st *store.Store, username, statusList string) error {
	var statuses []string
	for _, s := range strings.Split(statusList, ",") {
		if s = strings.TrimSpace(s); s != "" {
			statuses = append(statuses, s)
		}
	}

	fmt.Printf("fetching %v for %q from AniList...\n", statuses, username)
	entries, err := anilist.NewClient().FetchList(username, statuses)
	if err != nil {
		return err
	}
	fmt.Printf("got %d entries\n", len(entries))

	res, err := anilist.NewImporter(st).Import(entries)
	if err != nil {
		return err
	}

	fmt.Printf("created %d shows, updated %d, skipped %d\n", res.Created, res.Updated, res.Skipped)
	fmt.Printf("marked %d episodes as watched (terminal: never re-grabbed)\n", res.Episodes)
	for _, e := range res.Errors {
		fmt.Printf("  error: %s\n", e)
	}
	fmt.Println("\nnote: imported shows have no release-group offsets yet.")
	fmt.Println("run the trainer for each show before the listener will match anything.")
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
