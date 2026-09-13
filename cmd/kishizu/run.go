package main

import (
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

// itoa avoids importing strconv for one call site.
func itoa(n int) string { return fmt.Sprint(n) }

// runLoop polls Nyaa on a schedule and hands grabs to Transmission.
//
// dry-run is the default and matters: the machine is built and trained before
// it is switched on, and the user decides when. In dry-run the loop logs every
// decision with its reason, so the behaviour can be reviewed before anything
// downloads.
func runLoop(st *store.Store, artCache *art.Cache, rpcURL, staging, library string, keep int, interval time.Duration, dryRun bool, ntfyURL string) {
	l := listen.New(st)
	tc := transmission.New(rpcURL)
	w := watch.New(st, library, keep)
	rec := grab.New(st, tc, staging, library)
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
	// drive the windows, and delays move them. One request for the whole
	// timetable, so the cost is trivial.
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()

	refreshSchedule := func() {
		sched, err := schedule.Fetch(nil)
		if err != nil {
			log.Printf("schedule refresh: %v", err)
			return
		}
		shows, err := st.ListShows()
		if err != nil {
			return
		}
		updated := 0
		for _, sh := range shows {
			aliases := append([]string{sh.CanonicalName}, sh.Aliases...)
			if e := schedule.FindWithAliases(sched, aliases); e != nil && !e.AirsAt.IsZero() {
				if err := st.SetNextEpisode(sh.ID, e.NextEp, e.AirsAt); err != nil {
					continue
				}
				if err := st.ProjectAirDates(sh.ID); err != nil {
					continue
				}
				if e.ImageURL != sh.ImageURL {
					_ = st.SetImageURL(sh.ID, e.ImageURL)
				}
				// Cache the art now, so the first page load after a refresh is
				// served from disk rather than reaching out to the CDN.
				if artCache != nil {
					if _, err := artCache.Ensure(e.ImageURL); err != nil {
						log.Printf("art: cache %s: %v", sh.CanonicalName, err)
					}
				}
				updated++
			}
		}
		// Release art for finished seasons. The schedule drops a show once
		// it stops airing, so anything with art that is no longer on the
		// schedule is done — its cover is not coming back.
		if artCache != nil {
			onAir := map[string]bool{}
			for _, sh := range shows {
				aliases := append([]string{sh.CanonicalName}, sh.Aliases...)
				if e := schedule.FindWithAliases(sched, aliases); e != nil {
					onAir[sh.CanonicalName] = true
				}
			}
			for _, sh := range shows {
				if sh.ImageURL == "" || onAir[sh.CanonicalName] {
					continue
				}
				if err := artCache.Release(sh.ImageURL); err != nil {
					log.Printf("art: release %s: %v", sh.CanonicalName, err)
				} else {
					log.Printf("art: released %s (season over)", sh.CanonicalName)
					_ = st.SetImageURL(sh.ID, "")
				}
			}
		}
		log.Printf("schedule: refreshed, %d/%d shows have air dates", updated, len(shows))
	}

	// Refresh once at startup so the windows are current.
	refreshSchedule()

	for {
		select {
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
