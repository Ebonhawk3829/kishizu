package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/art"
	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/grab"
	"github.com/Ebonhawk3829/kishizu/internal/listen"
	"github.com/Ebonhawk3829/kishizu/internal/naming"
	"github.com/Ebonhawk3829/kishizu/internal/notify"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
)

// notify sends an alert, logging any failure.
//
// Notifications are best-effort — a missed ping must never stop a download —
// but a failure has to be visible. Discarding the error is how a wrong topic
// URL went unnoticed: every ping failed silently and the only symptom was
// that no notification arrived.
func alert(n notify.Notifier, title, message string, priority notify.Priority) {
	if n == nil {
		return
	}
	if err := n.Send(title, message, priority); err != nil {
		log.Printf("%s: %s: %v", n.Name(), title, err)
	}
}

// runLoop polls Nyaa on a schedule and hands grabs to Transmission.
//
// dry-run is the default and matters: the machine is built and trained before
// it is switched on, and the user decides when. In dry-run the loop logs every
// decision with its reason, so the behaviour can be reviewed before anything
// downloads.
//
// The loop exits when ctx is cancelled, so SIGINT/SIGTERM stop the pollers,
// the sweepers and the schedule refresh together with the HTTP server.
func runLoop(ctx context.Context, st *store.Store, artCache *art.Cache, dl download.Downloader, scheme *naming.Scheme, cfg *config.File, n notify.Notifier) {
	s := cfg.Server
	interval, err := time.ParseDuration(s.Interval)
	if err != nil {
		log.Printf("listener: bad interval %q, using 5m: %v", s.Interval, err)
		interval = 5 * time.Minute
	}
	keep := 2
	if s.Keep != nil {
		keep = *s.Keep
	}
	dryRun := true
	if s.DryRun != nil {
		dryRun = *s.DryRun
	}

	// The quality policy is global and comes from configuration, so the
	// listener ranks releases by what the user actually asked for. Only the
	// fields that were set are applied, so a partial section keeps the
	// shipped defaults.
	l := listen.NewWithPolicy(st, buildQuality(s.Quality))
	w := watch.New(st, s.Library, keep)
	rec := grab.NewWithScheme(st, s.Staging, s.Library, scheme)
	// Pruning deletes data, so it is opt-in and comes from configuration
	// rather than being on by default.
	rec.PruneUnselected = s.PruneUnselected

	log.Printf("listener: polling every %s (dry-run=%v, downloader=%s)",
		interval, dryRun, dl.Name())

	// lastPolled tracks when each show was last fetched, so per-show intervals
	// are honoured: a hunting show polls every 3 minutes while an up-to-date
	// one is never touched.
	lastPolled := map[int64]time.Time{}
	// downloaderDown latches the failure alert so a sustained outage pings
	// once rather than on every poll.
	downloaderDown := false

	poll := func() {
		// Poll only the shows that are due: hunting episodes get the aggressive
		// rate, no-release-found keeps a slow safety net, and shows without any
		// air date stay on the legacy interval. Everything else is dormant —
		// zero requests.
		due := l.DueShows(interval)
		if len(due) == 0 {
			log.Printf("listen: nothing due")
			return
		}
		now := time.Now()
		polled := 0
		for sh, want := range due {
			if last, ok := lastPolled[sh.ID]; ok && now.Sub(last) < want {
				continue // not this show's turn yet
			}
			lastPolled[sh.ID] = now
			polled++
			decisions, err := l.PollShow(sh)
			if err != nil {
				log.Printf("listen: %s: %v", sh.CanonicalName, err)
				continue
			}

			// One grab per (show, episode): the best-ranked candidate wins.
			grabs := l.FilterPreferences(decisions)
			for _, d := range grabs {
				if dryRun {
					log.Printf("WOULD GRAB %s ep%d %s (%s)", d.Show, d.Episode, d.Item.Title, d.Reason)
					continue
				}
				// One staging directory per show. The downloader creates it
				// on add, but making it here means the path is known to exist
				// and is owned by our uid rather than the downloader's.
				dir := filepath.Join(s.Staging, release.Sanitise(d.Show))
				if err := os.MkdirAll(dir, 0o775); err != nil {
					log.Printf("staging mkdir %s: %v", dir, err)
					continue
				}
				if err := dl.Add(download.Magnet(d.Item.InfoHash, d.Item.Title), dir); err != nil {
					log.Printf("%s add: %v", dl.Name(), err)
					// Alert once, then stay quiet until it recovers. At a
					// 3-minute poll, pinging every failure is ~480 a day.
					if !downloaderDown {
						downloaderDown = true
						alert(n, "kishizu: "+dl.Name()+" unreachable",
							"grabs will be retried; "+err.Error(), notify.PriorityHigh)
					}
					continue
				}
				if downloaderDown {
					downloaderDown = false
					log.Printf("%s: reachable again", dl.Name())
					alert(n, "kishizu: "+dl.Name()+" reachable",
						"grabs resumed", notify.PriorityDefault)
				}
				if err := l.MarkGrabbed(d); err != nil {
					log.Printf("mark grabbed: %v", err)
					continue
				}
				log.Printf("GRABBED %s ep%d %s", d.Show, d.Episode, d.Item.Title)
				alert(n, "kishizu: downloading", fmt.Sprintf("%s ep%d — %s", d.Show, d.Episode, d.Item.Title), notify.PriorityLow)
			}
		}
		// Logged every tick, including when nothing was grabbed: silence in the
		// log is otherwise indistinguishable from a stuck loop.
		log.Printf("listen: polled %d of %d due shows", polled, len(due))
	}

	// First sweep immediately, then on the interval.
	poll()

	tick := time.NewTicker(interval)
	defer tick.Stop()

	sweep := time.NewTicker(15 * time.Minute)
	defer sweep.Stop()

	// The schedule is re-checked daily: it is the source of the air times that
	// drive the windows, and delays move them. One request per show, straight
	// from each show's own page — the site publishes the same facts on a
	// weekly timetable, but consulting a second view of the same data would
	// mean matching tiles to shows, an exact-identity problem the slug
	// otherwise makes unnecessary.
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()

	refreshSchedule := func() {
		shows, err := st.ListShows()
		if err != nil {
			return
		}
		updated, finished := 0, 0
		for _, sh := range shows {
			if sh.Slug == "" {
				continue
			}
			info, err := schedule.FetchShow(nil, sh.Slug)
			if err != nil {
				log.Printf("schedule refresh: %s: %v", sh.CanonicalName, err)
				continue
			}
			if info.LatestEpisode > 0 && !info.NextAirsAt.IsZero() {
				if err := st.SetNextEpisode(sh.ID, info.LatestEpisode, info.NextAirsAt); err != nil {
					log.Printf("schedule refresh: %s: %v", sh.CanonicalName, err)
					continue
				}
				if err := st.ProjectAirDates(sh.ID); err != nil {
					log.Printf("schedule refresh: %s: %v", sh.CanonicalName, err)
					continue
				}
				updated++
			} else {
				// No countdown on the page: the season has finished. This is
				// the page saying so directly, rather than absence from a
				// weekly timetable — which was also true of breaks, premieres
				// and hiatuses, and stripping art for those was wrong.
				finished++
			}
			if info.ImageURL != "" && info.ImageURL != sh.ImageURL {
				_ = st.SetImageURL(sh.ID, info.ImageURL)
			}
			// Cache the art now, so the first page load after a refresh is
			// served from disk rather than reaching out to the CDN.
			if artCache != nil {
				if _, err := artCache.Ensure(info.ImageURL); err != nil {
					log.Printf("art: cache %s: %v", sh.CanonicalName, err)
				}
			}
		}
		// Release art for finished seasons. The signal is the page's own
		// countdown being absent AND the next episode being past the season
		// length — either alone can be a hiatus or a late slot.
		if artCache != nil {
			for _, sh := range shows {
				if sh.ImageURL == "" || sh.MaxEpisode <= 0 {
					continue
				}
				n, _, err := st.NextEpisode(sh.ID)
				if err != nil || n <= sh.MaxEpisode {
					continue
				}
				if err := artCache.Release(sh.ImageURL); err != nil {
					log.Printf("art: release %s: %v", sh.CanonicalName, err)
				} else {
					log.Printf("art: released %s (season complete)", sh.CanonicalName)
					_ = st.SetImageURL(sh.ID, "")
				}
			}
		}
		log.Printf("schedule: refreshed, %d/%d shows have air dates, %d finished",
			updated, len(shows), finished)
	}

	// Refresh once at startup so the windows are current.
	refreshSchedule()

	for {
		select {
		case <-ctx.Done():
			log.Printf("listener: shutting down")
			return
		case <-tick.C:
			poll()
		case <-daily.C:
			refreshSchedule()
		case <-sweep.C:
			// Finalise finished downloads: rename into the library layout,
			// record file_path, advance the latch. This is what makes the
			// watch signal deterministic.
			if !dryRun {
				if err := rec.Reconcile(); err != nil {
					log.Printf("reconcile: %v", err)
				}
			}
			deleted, kept, err := w.Sweep()
			if err != nil {
				log.Printf("watch sweep: %v", err)
				continue
			}
			for _, f := range deleted {
				log.Printf("watch: deleted %s", f)
			}
			if len(deleted) > 0 {
				log.Printf("watch: %d deleted, %d kept", len(deleted), len(kept))
				alert(n, "kishizu: episodes deleted",
					fmt.Sprintf("%d deleted, %d kept", len(deleted), len(kept)), notify.PriorityDefault)
			}
		}
	}
}
