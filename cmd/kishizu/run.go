package main

import (
	"fmt"
	"log"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/grab"
	"github.com/Ebonhawk3829/kishizu/internal/listen"
	"github.com/Ebonhawk3829/kishizu/internal/ntfy"
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
func runLoop(st *store.Store, rpcURL, library string, keep int, interval time.Duration, dryRun bool, ntfyURL string) {
	l := listen.New(st)
	tc := transmission.New(rpcURL)
	w := watch.New(st, library, keep)
	rec := grab.New(st, tc, library)
	n := ntfy.New(ntfyURL)

	log.Printf("listener: polling every %s (dry-run=%v, transmission=%s)",
		interval, dryRun, rpcURL)

	poll := func() {
		decisions, err := l.Poll()
		if err != nil {
			log.Printf("listen: %v", err)
			return
		}

		// One grab per (show, episode): the best-ranked candidate wins. Without
		// this, every release for an episode would be handed off.
		grabs := l.FilterPreferences(decisions)
		for _, d := range grabs {
			if dryRun {
				log.Printf("WOULD GRAB %s ep%d %s (%s)", d.Show, d.Episode, d.Item.Title, d.Reason)
				continue
			}
			if err := tc.AddWithDir(magnetFor(d.Item.InfoHash, d.Item.Title), library); err != nil {
				log.Printf("transmission add: %v", err)
				n.Send("kishizu: download failed", d.Show+" ep"+itoa(d.Episode)+": "+err.Error(), ntfy.PriorityHigh)
				continue
			}
			if err := l.MarkGrabbed(d); err != nil {
				log.Printf("mark grabbed: %v", err)
				continue
			}
			log.Printf("GRABBED %s ep%d %s", d.Show, d.Episode, d.Item.Title)
			n.Send("kishizu: downloading", fmt.Sprintf("%s ep%d — %s", d.Show, d.Episode, d.Item.Title), ntfy.PriorityLow)
		}
	}

	// First sweep immediately, then on the interval.
	poll()

	tick := time.NewTicker(interval)
	defer tick.Stop()

	sweep := time.NewTicker(15 * time.Minute)
	defer sweep.Stop()

	for {
		select {
		case <-tick.C:
			poll()
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
