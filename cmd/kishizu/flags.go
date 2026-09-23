package main

import (
	"flag"
)

// flags holds every command-line flag kishizu accepts, in one struct so the
// definitions and the dispatch read the same names.
//
// Only what the config file cannot hold lives here: where the database is,
// where the config file is, which mode to run, and the arguments those modes
// take. Every server setting — library, staging, keep, delete, interval,
// dry-run, downloader, notifier — is configured in kishizu.yaml and editable
// from the web UI.
//
// A settings flag would duplicate its default in two places, and the two
// drift: a caller reading the flag gets a different answer than one reading
// the config. Keep settings out of here.
type flags struct {
	dbPath       string
	show         string
	seed         bool
	configPath   string
	list         bool
	trainName    string
	ep           int
	serve        string
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

// parseFlags defines and parses the command line.
//
// There are no settings flags: every server setting comes from the config
// file, which the web UI edits.
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
