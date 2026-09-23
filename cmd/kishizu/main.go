// Command kishizu is a seasonal anime downloader.
//
// It watches an indexer for the shows you have declared, works out which
// release is the episode you are waiting for, hands it to a torrent client,
// files it in your library, and deletes it once you have watched it.
//
// Run with -serve for the web UI and the polling loop. The remaining flags are
// one-shot commands that act on the database and exit: -seed, -list, -train,
// -infer, -adopt, -backfill-slugs and -reconcile. With no command it prints
// what the listener would grab right now, which is the same pipeline the live
// loop runs, so the report cannot drift from what -dry-run=false would do.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Ebonhawk3829/kishizu/internal/art"
	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/grab"
	"github.com/Ebonhawk3829/kishizu/internal/listen"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/version"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
	"github.com/Ebonhawk3829/kishizu/internal/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	f := parseFlags()

	if f.showVersion {
		fmt.Printf("kishizu %s (%s)\n", version.String(), version.GoVersion())
		return
	}

	// KISHIZU_DEBUG=1 also enables debug; the flag wins.
	if f.debugOn {
		debug.Set(true)
	}

	// The config file is the source of truth; flags override it. Loading it
	// first means a pre-seeded library is filed the way the user's existing
	// one already is.
	cfg, err := config.Load(f.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	indexer, err := buildIndexer(cfg.Server.Indexer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// An empty endpoint must fail at startup rather than surface as a failed
	// grab later. Read dry-run from the resolved config: a flag default of
	// true would skip this guard for a config that set dry_run: false.
	if !cfg.Server.IsDryRun() && cfg.Server.Downloader.TransmissionRPC == "" &&
		cfg.Server.Downloader.Kind == string(download.KindTransmission) {
		fmt.Fprintf(os.Stderr, "a Transmission RPC endpoint is required unless dry_run is set\n")
		os.Exit(1)
	}

	st, err := store.Open(f.dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		os.Exit(1)
	}
	// os.Exit skips defers, so this close covers the paths that return
	// normally.
	defer func() { _ = st.Close() }()

	switch {
	case f.reconcile:
		runReconcile(st, cfg)
	case f.serve != "":
		runServe(st, cfg, f, indexer)
	case f.adopt != "":
		runAdopt(st, cfg, f)
	case f.infer:
		runInfer(st, indexer)
	case f.backfill:
		runBackfill(st, f)
	case f.seed:
		runSeed(st, cfg)
	case f.list:
		runList(st)
	case f.trainName != "":
		runTrain(st, f, indexer)
	default:
		runReport(st, f, indexer)
	}
}

// runReconcile files completed downloads once and exits.
func runReconcile(st *store.Store, cfg *config.File) {
	scheme, err := buildScheme(cfg.Server.Naming)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	rec := grab.NewWithScheme(st, cfg.Server.Staging, cfg.Server.Library, scheme)
	rec.PruneUnselected = cfg.Server.PruneUnselected
	if err := rec.Reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("reconciled")
}

// runServe starts the web UI and the polling loop together, and blocks until
// the context is cancelled.
func runServe(st *store.Store, cfg *config.File, f *flags, indexer *nyaa.Client) {
	srv, err := web.New(st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "web: %v\n", err)
		os.Exit(1)
	}
	// Training must query the same indexer the listener polls, or offsets
	// would be learned from releases the pipeline never sees.
	srv.SetIndexer(indexer)
	// The watch handler must read the resolved config, not the raw flags:
	// applyFlagOverrides has already merged the two, and a second handler
	// built from flag defaults would sweep with a different keep window
	// than the ticker's — the yaml value silently ignored on every
	// watch-triggered sweep.
	keep := 2
	if cfg.Server.Keep != nil {
		keep = *cfg.Server.Keep
	}
	after, err := cfg.Server.DeleteAfterDuration()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	srv.SetWatch(watch.New(st, cfg.Server.Library, keep, cfg.Server.Delete, after))

	// The notifier reads the resolved config, never a flag: buildNotifier
	// treats an empty ntfy URL as "notifications disabled", so a source that
	// can be empty by default silently turns every alert off.
	n, err := buildNotifier(cfg.Server.Notifier.Kind, cfg.Server.Notifier.NtfyTopic,
		cfg.Server.Notifier.GotifyURL, cfg.Server.Notifier.GotifyToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if n != nil {
		srv.SetNotifier(n)
	}

	// Cover art is cached next to the database, so page loads stay off the
	// schedule's CDN.
	artCache, err := art.New(filepath.Join(filepath.Dir(f.dbPath), "art"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "art cache: %v\n", err)
		os.Exit(1)
	}
	srv.SetArt(artCache)

	// One downloader shared by the listener and adoption, so an adopted
	// season lands where the reconciler looks.
	dl, err := buildDownloader(cfg.Server.Downloader.Kind,
		cfg.Server.Downloader.TransmissionRPC,
		cfg.Server.Downloader.QBittorrentURL,
		cfg.Server.Downloader.QBittorrentUser,
		cfg.Server.Downloader.QBittorrentPass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	srv.SetDownloaderURL(dl.URL())

	// The naming scheme must match the reconciler's, or the watch signal
	// cannot recognise kishizu's own filenames.
	scheme, err := buildScheme(cfg.Server.Naming)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	srv.SetNaming(scheme)

	// The timetable cache lives next to the database so it survives a
	// restart and browsing works when the schedule site is unreachable.
	ttCache, err := schedule.NewCache(filepath.Join(filepath.Dir(f.dbPath), "cache"), schedule.DefaultTimetableTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "timetable cache: %v\n", err)
		os.Exit(1)
	}
	srv.SetTimetable(ttCache)

	srv.SetConfigPath(f.configPath)
	srv.SetAdopt(cfg.Server.Staging, cfg.Server.Library, dl)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runLoop(ctx, st, artCache, dl, scheme, cfg, n, ttCache, indexer)
	if err := srv.ListenAndServe(ctx, f.serve); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// runAdopt adopts a finished season from a releases.moe entry. Dry run
// unless -adopt-confirm is given.
func runAdopt(st *store.Store, cfg *config.File, f *flags) {
	dl, err := buildDownloader(cfg.Server.Downloader.Kind,
		cfg.Server.Downloader.TransmissionRPC,
		cfg.Server.Downloader.QBittorrentURL,
		cfg.Server.Downloader.QBittorrentUser,
		cfg.Server.Downloader.QBittorrentPass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := adoptSeason(context.Background(), st, f.adopt, f.adoptEps, cfg.Server.Staging, cfg.Server.Library, dl, f.adoptConfirm); err != nil {
		fmt.Fprintf(os.Stderr, "adopt: %v\n", err)
		os.Exit(1)
	}
}

func runInfer(st *store.Store, indexer *nyaa.Client) {
	if err := inferOffsets(context.Background(), st, indexer); err != nil {
		fmt.Fprintf(os.Stderr, "infer: %v\n", err)
		os.Exit(1)
	}
}

func runBackfill(st *store.Store, f *flags) {
	if err := backfillSlugs(st, f.slugFile); err != nil {
		fmt.Fprintf(os.Stderr, "backfill: %v\n", err)
		os.Exit(1)
	}
}

func runSeed(st *store.Store, cfg *config.File) {
	if err := seedFromConfig(st, cfg.Shows); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("seeded")
}

func runList(st *store.Store) {
	if err := listShowsWithSchedule(st); err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		os.Exit(1)
	}
}

func runTrain(st *store.Store, f *flags, indexer *nyaa.Client) {
	shows, err := st.ListShows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list shows: %v\n", err)
		os.Exit(1)
	}
	var target *store.Show
	for _, sh := range shows {
		if strings.Contains(strings.ToLower(sh.CanonicalName), strings.ToLower(f.trainName)) {
			target = sh
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "no show matched %q\n", f.trainName)
		os.Exit(1)
	}

	targetEp := f.ep
	if targetEp == 0 {
		targetEp = nextUnwatched(st, target)
	}
	if err := trainShowCmd(context.Background(), st, target, targetEp, indexer); err != nil {
		fmt.Fprintf(os.Stderr, "train: %v\n", err)
		os.Exit(1)
	}
}

// runReport prints what the listener would grab right now, for every show or
// just the one named by -show.
func runReport(st *store.Store, f *flags, indexer *nyaa.Client) {
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
		if f.show != "" && !strings.Contains(strings.ToLower(sh.CanonicalName), strings.ToLower(f.show)) {
			continue
		}
		any = true
		reportShow(context.Background(), st, sh, indexer)
	}
	if !any {
		fmt.Fprintf(os.Stderr, "no show matched %q\n", f.show)
		os.Exit(1)
	}
}

// reportShow prints what the listener would grab for one show right now, with
// the reason for every decision. It runs the same pipeline as the live loop,
// so the report cannot drift from what -dry-run=false would do. Nothing is
// handed to the downloader.
func reportShow(ctx context.Context, st *store.Store, sh *store.Show, indexer *nyaa.Client) {
	l := listen.New(st, indexer)

	fmt.Printf("\n=== %s\n", sh.CanonicalName)

	decisions, err := l.PollShow(ctx, sh)
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

	// The refusals are the interesting part: "why was this not grabbed" is
	// what a dry run exists to answer.
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
