package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/art"
	"github.com/Ebonhawk3829/kishizu/internal/grab"
	"github.com/Ebonhawk3829/kishizu/internal/listen"
	"github.com/Ebonhawk3829/kishizu/internal/ntfy"
	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/transmission"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
)

// magnetFor builds a magnet link from an RSS item. The display name is kept so
// the torrent has a readable name in Transmission.
func magnetFor(infohash, title string) string {
	return "magnet:?xt=urn:btih:" + infohash + "&dn=" + title
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
func runLoop(ctx context.Context, st *store.Store, artCache *art.Cache, rpcURL, staging, library string, keep int, interval time.Duration, dryRun bool, ntfyURL string) {
	l := listen.New(st)
	tc := transmission.New(rpcURL)
	w := watch.New(st, library, keep)
	rec := grab.New(st, staging, library)
	n := ntfy.New(ntfyURL)

	log.Printf("listener: polling every %s (dry-run=%v, transmission=%s)",
		interval, dryRun, rpcURL)

	// lastPolled tracks when each show was last fetched, so per-show intervals
	// are honoured: a hunting show polls every 3 minutes while an up-to-date
	// one is never touched.
	lastPolled := map[int64]time.Time{}
	// transmissionDown latches the failure alert so a sustained outage pings
	// once rather than on every poll.
	transmissionDown := false

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
				// One staging directory per show. Transmission creates it on
				// add, but making it here means the path is known to exist
				// and is owned by our uid rather than Transmission's.
				dir := filepath.Join(staging, watch.Sanitise(d.Show))
				if err := os.MkdirAll(dir, 0o775); err != nil {
					log.Printf("staging mkdir %s: %v", dir, err)
					continue
				}
				if err := tc.AddWithDir(magnetFor(d.Item.InfoHash, d.Item.Title), dir); err != nil {
					log.Printf("transmission add: %v", err)
					// Alert once, then stay quiet until it recovers. At a
					// 3-minute poll, pinging every failure is ~480 a day.
					if !transmissionDown {
						transmissionDown = true
						n.Send("kishizu: Transmission unreachable",
							"grabs will be retried; "+err.Error(), ntfy.PriorityHigh)
					}
					continue
				}
				if transmissionDown {
					transmissionDown = false
					log.Printf("transmission: reachable again")
					n.Send("kishizu: Transmission reachable",
						"grabs resumed", ntfy.PriorityDefault)
				}
				if err := l.MarkGrabbed(d); err != nil {
					log.Printf("mark grabbed: %v", err)
					continue
				}
				log.Printf("GRABBED %s ep%d %s", d.Show, d.Episode, d.Item.Title)
				n.Send("kishizu: downloading", fmt.Sprintf("%s ep%d — %s", d.Show, d.Episode, d.Item.Title), ntfy.PriorityLow)
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
				n.Send("kishizu: episodes deleted",
					fmt.Sprintf("%d deleted, %d kept", len(deleted), len(kept)), ntfy.PriorityDefault)
			}
		}
	}
}
