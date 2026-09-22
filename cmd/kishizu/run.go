package main

import (
	"context"
	"errors"
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
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
)

// alert sends a notification, logging any failure. Best-effort: a missed
// ping must never stop a download, but a failure has to be visible.
func alert(n notify.Notifier, title, message string, priority notify.Priority) {
	if n == nil {
		return
	}
	if err := n.Send(title, message, priority); err != nil {
		log.Printf("%s: %s: %v", n.Name(), title, err)
	}
}

// runLoop polls the indexer on a schedule and hands grabs to the configured
// torrent client. Dry-run is the default: the loop logs every decision with
// its reason so behaviour can be reviewed before anything downloads.
//
// Two scheduled jobs run alongside the poller, each writing to its own store
// so the UI only ever reads:
//
//   - daily:   air times for the tracked shows, from each show's own page
//   - weekly:  the seasonal browse list, plus each entry's English title
//
// The loop exits when ctx is cancelled, so SIGINT/SIGTERM stop the pollers,
// the sweepers and the schedule refresh together with the HTTP server.
func runLoop(ctx context.Context, st *store.Store, artCache *art.Cache, dl download.Downloader, scheme *naming.Scheme, cfg *config.File, n notify.Notifier, ttCache *schedule.Cache, indexer *nyaa.Client) {
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

	l := listen.NewWithPolicy(st, indexer, buildQuality(s.Quality))
	w := watch.New(st, s.Library, keep)
	rec := grab.NewWithScheme(st, s.Staging, s.Library, scheme)
	rec.PruneUnselected = s.PruneUnselected
	// A stalled download pings once, not on every poll.
	stalled := map[string]bool{}
	rec.OnStall = func(show, title string, ep int) {
		key := fmt.Sprintf("%s:%d", show, ep)
		if stalled[key] {
			return
		}
		stalled[key] = true
		alert(n, "kishizu: download stalled",
			fmt.Sprintf("%s ep%d has been downloading for over 48h with no file — the swarm may be dead. Unlatch to retry.", show, ep),
			notify.PriorityHigh)
	}

	log.Printf("listener: polling every %s (dry-run=%v, downloader=%s)",
		interval, dryRun, dl.Name())

	// lastPolled honours per-show intervals: a hunting show polls every 3
	// minutes, an up-to-date one is never touched.
	lastPolled := map[int64]time.Time{}
	downloaderDown := false

	poll := func() {
		// Hunting episodes get the aggressive rate, no-release-found keeps a
		// slow safety net, shows without an air date stay on the legacy
		// interval, and everything else is dormant.
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
			decisions, err := l.PollShow(ctx, sh)
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
				// One staging directory per show, created here so the path
				// exists and is owned by our uid.
				dir := filepath.Join(s.Staging, release.Sanitise(d.Show))
				if err := os.MkdirAll(dir, 0o775); err != nil {
					log.Printf("staging mkdir %s: %v", dir, err)
					continue
				}
				if err := dl.Add(ctx, download.Magnet(d.Item.InfoHash, d.Item.Title), dir); err != nil {
					log.Printf("%s add: %v", dl.Name(), err)
					// Alert once per outage; at a 3-minute poll, pinging
					// every failure is ~480 a day.
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
		// Logged every tick: silence in the log is otherwise
		// indistinguishable from a stuck loop.
		log.Printf("listen: polled %d of %d due shows", polled, len(due))
	}

	// First sweep immediately, then on the interval.
	poll()

	tick := time.NewTicker(interval)
	defer tick.Stop()

	sweep := time.NewTicker(15 * time.Minute)
	defer sweep.Stop()

	// The schedule is re-checked daily: it is the source of the air times
	// that drive the hunt windows, and delays move them. One request per
	// show, straight from each show's own page.
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()

	// A show's page must be missing this many consecutive daily checks before
	// it is treated as finished: pages vanish transiently during a site
	// update, and one bad response would otherwise end a live season. The
	// cost of waiting is two days of polling a show that has ended, which is
	// harmless since nothing is due for it.
	const missingThreshold = 3

	// Consecutive not-found sightings per show, held outside the closure so
	// it survives between refreshes.
	misses := map[int64]int{}

	refreshSchedule := func() {
		shows, err := st.ListShows()
		if err != nil {
			// The refresh is what moves air dates when a show is delayed; a
			// silent no-op here means every hunt window drifts on stale
			// timing with no signal. Logged even though the caller retries
			// tomorrow, because a systemic failure should be visible.
			log.Printf("schedule refresh: list shows: %v", err)
			return
		}
		updated, finished := 0, 0
		for _, sh := range shows {
			if sh.Slug == "" {
				continue
			}
			info, err := schedule.FetchShow(nil, sh.Slug)
			if err != nil {
				// A 404 is evidence the page is gone; a timeout or a 5xx is
				// the absence of evidence. Even a 404 needs three sightings
				// before it is believed.
				if errors.Is(err, schedule.ErrNotFound) {
					misses[sh.ID]++
					log.Printf("schedule refresh: %s: page not found (%d of %d)",
						sh.CanonicalName, misses[sh.ID], missingThreshold)
					if misses[sh.ID] >= missingThreshold {
						log.Printf("schedule refresh: %s: page gone %d days running, treating as finished",
							sh.CanonicalName, misses[sh.ID])
						finished++
					}
					continue
				}
				log.Printf("schedule refresh: %s: %v", sh.CanonicalName, err)
				continue
			}
			// The page is back, so whatever the earlier misses meant, it was
			// not permanent.
			delete(misses, sh.ID)
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
				// weekly timetable — which is also true of breaks, premieres
				// and hiatuses, and stripping art for those would be wrong.
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

	// The seasonal browse list, on its own weekly schedule: it covers a whole
	// season (~110 shows) rather than the handful tracked, and English titles
	// do not change once a season is under way. It writes to the cache, which
	// the browse panel only reads, so browsing costs no network I/O.
	weekly := time.NewTicker(schedule.EnrichTTL)
	defer weekly.Stop()

	refreshTimetable := func() {
		if ttCache == nil {
			return
		}
		tt, err := ttCache.Update(nil)
		if err != nil {
			// Non-fatal: the existing snapshot stays and browsing keeps working
			// from it.
			log.Printf("timetable: refresh failed, keeping the cached list: %v", err)
			return
		}
		log.Printf("timetable: %d shows, %d with an English title",
			len(tt.Entries), countEnglish(tt))
	}

	// Warmed at startup so a fresh container has a browse list immediately.
	// This is the one pass that costs the full ~110 requests; later refreshes
	// reuse titles by slug.
	go refreshTimetable()

	for {
		select {
		case <-ctx.Done():
			log.Printf("listener: shutting down")
			return
		case <-tick.C:
			poll()
		case <-daily.C:
			refreshSchedule()
		case <-weekly.C:
			refreshTimetable()
		case <-sweep.C:
			// Finalise finished downloads and delete watched ones.
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

// countEnglish is how many entries carry an English title, reported after a
// refresh so the log says whether enrichment actually landed.
func countEnglish(tt *schedule.Timetable) int {
	n := 0
	for _, e := range tt.Entries {
		if e.EnglishTitle != "" {
			n++
		}
	}
	return n
}
