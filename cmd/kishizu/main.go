// Command kishizu is the dry-run entry point: fetch each tracked show's Nyaa
// feed, match every release, and print what WOULD be downloaded.
//
// Nothing is downloaded. This exists to validate matching and the duplicate
// guards against live data before any of it is wired to Transmission.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/art"
	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/listen"
	"github.com/Ebonhawk3829/kishizu/internal/ntfy"
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
	trainName := flag.String("train", "", "train a show (substring match on canonical name)")
	ep := flag.Int("ep", 0, "episode number to train against (0 = next unwatched)")
	serve := flag.String("serve", "", "start the web UI on this address (e.g. 127.0.0.1:8098)")
	rpc := flag.String("transmission", "http://TAILNET_IP:9091/transmission/rpc", "Transmission RPC endpoint")
	library := flag.String("library", "/media/anime", "library root for finished episodes, as kishizu sees it")
	staging := flag.String("staging", "/downloads/anime", "staging root Transmission downloads into, as kishizu sees it")
	keep := flag.Int("keep", 2, "recently watched episodes to keep on disk")
	interval := flag.Duration("interval", 5*time.Minute, "RSS poll interval")
	dryRun := flag.Bool("dry-run", true, "poll and decide but do not hand off to Transmission")
	ntfyURL := flag.String("ntfy", "http://TAILNET_IP:8085/kishizu", "ntfy topic URL for notifications (empty disables)")
	debugOn := flag.Bool("debug", false, "verbose logging of every decision (toggleable at runtime via POST /api/debug)")
	infer := flag.Bool("infer", false, "derive group offsets from air dates instead of training by hand")
	backfill := flag.Bool("backfill-slugs", false, "attach animeschedule slugs from the mapping file and enrich from the schedule")
	slugFile := flag.String("slugs", "slugs.yaml", "name -> animeschedule slug mapping used by -backfill-slugs")
	preferred := flag.String("prefer", "VARYG,Erai-Raws,SubsPlease,ToonsHub", "preferred release groups, best first")
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
		if *ntfyURL != "" {
			srv.SetNotifier(ntfy.New(*ntfyURL))
		}
		// Cover art is cached next to the database, so the UI does not depend
		// on the schedule's CDN at page-load time.
		artCache, err := art.New(filepath.Join(filepath.Dir(*dbPath), "art"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "art cache: %v\n", err)
			os.Exit(1)
		}
		srv.SetArt(artCache)
		// The listener runs alongside the UI. It is dry-run by default: it
		// polls, matches and logs decisions, but hands nothing to Transmission
		// until -dry-run=false. The user switches it on deliberately.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go runLoop(ctx, st, artCache, *rpc, *staging, *library, *keep, *interval, *dryRun, *ntfyURL)
		if err := srv.ListenAndServe(ctx, *serve); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *infer {
		if err := inferOffsets(st, *preferred); err != nil {
			fmt.Fprintf(os.Stderr, "infer: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *backfill {
		if err := backfillSlugs(st, *slugFile); err != nil {
			fmt.Fprintf(os.Stderr, "backfill: %v\n", err)
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

// run prints what the listener WOULD grab for one show right now, with the
// reason for every decision. It is the real pipeline — the same PollShow and
// FilterPreferences the live loop runs — so the report cannot drift from
// what -dry-run=false would actually do. Nothing is handed to Transmission.
//
// Kept deliberately as a simulation tool: it answers "what would kishizu do
// and why" against live data without touching anything.
func run(st *store.Store, sh *store.Show) {
	l := listen.New(st)

	fmt.Printf("\n=== %s\n", sh.CanonicalName)

	decisions, err := l.PollShow(sh)
	if err != nil {
		fmt.Printf("    ERROR: %v\n", err)
		return
	}
	fmt.Printf("    %d releases evaluated\n", len(decisions))

	grabs := l.FilterPreferences(decisions)
	if len(grabs) == 0 {
		fmt.Printf("    nothing to grab\n")
	} else {
		fmt.Printf("    WOULD DOWNLOAD:\n")
		for _, d := range grabs {
			fmt.Printf("      ep%-3d seeders=%-5d %s\n      (%s)\n",
				d.Episode, d.Item.Seeders, truncate(d.Item.Title, 70), d.Reason)
		}
	}

	// The refusals are the interesting part: "why was this not grabbed" is the
	// question a dry run exists to answer.
	skipped := 0
	for _, d := range decisions {
		if !d.Grab {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Printf("    %d skipped:\n", skipped)
		for _, d := range decisions {
			if d.Grab {
				continue
			}
			fmt.Printf("      %-70s %s\n", truncate(d.Item.Title, 70), d.Reason)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
