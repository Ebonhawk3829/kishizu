package main

import (
	"flag"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/notify"
)

// flags holds every command-line flag kishizu accepts, in one struct so the
// definitions, the override application and the dispatch all read the same
// names.
type flags struct {
	dbPath       string
	show         string
	seed         bool
	configPath   string
	list         bool
	trainName    string
	ep           int
	serve        string
	rpc          string
	downloader   string
	qbitURL      string
	qbitUser     string
	qbitPass     string
	library      string
	staging      string
	keep         int
	interval     time.Duration
	dryRun       bool
	ntfyURL      string
	notifier     string
	gotifyURL    string
	gotifyToken  string
	debugOn      bool
	infer        bool
	backfill     bool
	slugFile     string
	adopt        string
	adoptEps     string
	adoptConfirm bool
	showVersion  bool
	reconcile    bool
}

// parseFlags defines and parses the command line. Defaults here are the
// shipped defaults; a flag overrides the config file only when the user
// actually typed it, which applyFlagOverrides enforces with flag.Visit.
func parseFlags() *flags {
	f := &flags{}

	flag.StringVar(&f.dbPath, "db", "kishizu.db", "path to the SQLite database")
	flag.StringVar(&f.show, "show", "", "only run this show (substring match on canonical name)")
	flag.BoolVar(&f.seed, "seed", false, "insert the shows from the seed file")
	flag.StringVar(&f.configPath, "config", "kishizu.yaml", "configuration file (server settings and the show list)")
	flag.BoolVar(&f.list, "list", false, "list tracked shows with next episode and air dates")
	flag.StringVar(&f.trainName, "train", "", "train a show (substring match on canonical name)")
	flag.IntVar(&f.ep, "ep", 0, "episode number to train against (0 = next unwatched)")
	flag.StringVar(&f.serve, "serve", "", "start the web UI on this address (e.g. 127.0.0.1:8098)")
	// Empty by default: an empty value fails loudly at startup rather than
	// surfacing as a failed grab later.
	flag.StringVar(&f.rpc, "transmission", "", "Transmission RPC endpoint (required to download)")
	flag.StringVar(&f.downloader, "downloader", string(download.KindTransmission), "torrent client: transmission or qbittorrent")
	flag.StringVar(&f.qbitURL, "qbittorrent", "", "qBittorrent WebUI URL, e.g. http://localhost:8080")
	flag.StringVar(&f.qbitUser, "qbittorrent-user", "", "qBittorrent WebUI username (optional)")
	flag.StringVar(&f.qbitPass, "qbittorrent-pass", "", "qBittorrent WebUI password (optional)")
	flag.StringVar(&f.library, "library", "/media/anime", "library root for finished episodes, as kishizu sees it")
	flag.StringVar(&f.staging, "staging", "/downloads/anime", "staging root the downloader puts completed files in, as kishizu sees it")
	flag.IntVar(&f.keep, "keep", 2, "recently watched episodes to keep on disk")
	flag.DurationVar(&f.interval, "interval", 5*time.Minute, "RSS poll interval")
	flag.BoolVar(&f.dryRun, "dry-run", true, "poll and decide but do not hand off to the downloader")
	flag.StringVar(&f.ntfyURL, "ntfy", "", "ntfy topic URL for notifications (empty disables)")
	flag.StringVar(&f.notifier, "notifier", string(notify.KindNtfy), "notification backend: ntfy, gotify or none")
	flag.StringVar(&f.gotifyURL, "gotify", "", "Gotify server URL, e.g. https://gotify.example.com")
	flag.StringVar(&f.gotifyToken, "gotify-token", "", "Gotify app token")
	flag.BoolVar(&f.debugOn, "debug", false, "verbose logging of every decision (toggleable at runtime via POST /api/debug)")
	flag.BoolVar(&f.infer, "infer", false, "derive group offsets from air dates instead of training by hand")
	flag.BoolVar(&f.backfill, "backfill-slugs", false, "attach animeschedule slugs from the mapping file and enrich from the schedule")
	flag.StringVar(&f.slugFile, "slugs", "slugs.yaml", "name -> animeschedule slug mapping used by -backfill-slugs")
	flag.StringVar(&f.adopt, "adopt", "", "adopt a finished season from a releases.moe entry URL (dry-run: prints the plan)")
	flag.StringVar(&f.adoptEps, "adopt-episodes", "", "episode numbers to adopt, comma separated (default: every file the classifier proposed)")
	flag.BoolVar(&f.adoptConfirm, "adopt-confirm", false, "with -adopt: perform the adoption instead of printing the plan")
	flag.BoolVar(&f.showVersion, "version", false, "print the version and exit")
	flag.BoolVar(&f.reconcile, "reconcile", false, "file completed downloads in staging once, then exit")

	flag.Parse()
	return f
}

// overrides projects the flags that can override the config file.
func (f *flags) overrides() flagOverrides {
	return flagOverrides{
		library:     f.library,
		staging:     f.staging,
		keep:        f.keep,
		interval:    f.interval,
		dryRun:      f.dryRun,
		rpc:         f.rpc,
		downloader:  f.downloader,
		qbitURL:     f.qbitURL,
		qbitUser:    f.qbitUser,
		qbitPass:    f.qbitPass,
		ntfyURL:     f.ntfyURL,
		notifier:    f.notifier,
		gotifyURL:   f.gotifyURL,
		gotifyToken: f.gotifyToken,
	}
}
