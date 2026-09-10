package main

import (
	"log"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/listen"
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
func runLoop(st *store.Store, rpcURL, library string, keep int, interval time.Duration, dryRun bool) {
	l := listen.New(st)
	tc := transmission.New(rpcURL)
	w := watch.New(st, library, keep)

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
			if err := tc.Add(magnetFor(d.Item.InfoHash, d.Item.Title)); err != nil {
				log.Printf("transmission add: %v", err)
				continue
			}
			if err := l.MarkGrabbed(d); err != nil {
				log.Printf("mark grabbed: %v", err)
				continue
			}
			log.Printf("GRABBED %s ep%d %s", d.Show, d.Episode, d.Item.Title)
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
			deleted, kept, err := w.Sweep()
			if err != nil {
				log.Printf("watch sweep: %v", err)
				continue
			}
			for _, f := range deleted {
				log.Printf("watch: deleted %s", f)
			}
			if len(deleted) > 0 || len(kept) > 0 {
				log.Printf("watch: %d deleted, %d kept", len(deleted), len(kept))
			}
		}
	}
}
