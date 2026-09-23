package web

// Webhook intake for media servers.
//
// Jellyfin, Plex and Emby can all report "the user finished an episode", but
// each speaks its own payload dialect and none of them speak kishizu's. This
// handler translates: it accepts the payload shapes those servers actually
// send, resolves the episode the same way /api/watched does, and records the
// watch through the same core. A media server therefore needs no kishizu
// plugin — pointing its webhook at this endpoint is the whole integration.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/notify"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// webhookPayload is a permissive envelope covering the three media servers'
// watch-event shapes. Every field is optional: the handler works out which
// identity the payload carries and refuses when it carries none.
//
// Jellyfin and Emby nest the episode under Item and name the event "Event";
// Plex sends "event" and nests it under Metadata. The generic path/show/episode
// fields accept a plain hand-written POST, so the endpoint is also usable
// without any media server at all.
type webhookPayload struct {
	// Event is the server's event name. Empty means the caller did not send
	// one, which is treated as deliberate: a hand-written POST has no event.
	Event string `json:"event"`
	// Jellyfin and Emby capitalise the event field.
	JFEvent string `json:"Event"`

	Item *struct {
		SeriesName  string `json:"SeriesName"`
		IndexNumber int    `json:"IndexNumber"`
		Path        string `json:"Path"`
	} `json:"Item"`

	Metadata *struct {
		GrandparentTitle string `json:"grandparentTitle"`
		Index            int    `json:"index"`
	} `json:"Metadata"`

	// Generic fallbacks, for a hand-written POST or an unrecognised server.
	Path    string `json:"path"`
	Show    string `json:"show"`
	Episode int    `json:"episode"`
}

// isWatchEvent reports whether the payload's event name means the user
// finished watching. Media servers fire many events — playback started,
// paused, resumed — and marking a watch on any of them would latch episodes
// the user has not finished. An absent event is accepted: a hand-written POST
// has no event, and the caller posting a path is asserting completion.
func (p *webhookPayload) isWatchEvent() bool {
	if p.Event == "" && p.JFEvent == "" {
		return true
	}
	for _, ev := range []string{p.Event, p.JFEvent} {
		switch {
		case ev == "":
			continue
		case strings.Contains(ev, "markplayed"),
			strings.Contains(ev, "markwatched"),
			strings.Contains(ev, "scrobble"):
			return true
		}
	}
	return false
}

// handleWebhook receives watch events from Jellyfin, Plex and Emby.
//
// Identity is resolved in the order the payloads supply it: a file path goes
// through the same server-side matcher as /api/watched; otherwise a show name
// plus episode number is matched against canonical names and aliases. Plex
// webhooks carry no file path at all, so the name path is not an optional
// extra — it is the only path Plex can take.
//
// An event that is not a watch event is answered 200 with marked:false rather
// than an error: a server that reports playback.start every few minutes must
// not fill kishizu's log with failures for behaving normally.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := readWebhookBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("webhook: %w", err))
		return
	}

	if !p.isWatchEvent() {
		writeJSON(w, map[string]any{"marked": false,
			"reason": "event is not a watch completion"})
		return
	}

	var showID int64
	var epNum int

	switch {
	case p.Path != "":
		// A file path: resolve it exactly as /api/watched would, so both
		// intakes agree on what a given filename means.
		matched, ep, err := s.matchFile(baseName(p.Path))
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		showID, epNum = matched, ep

	case p.Item != nil && p.Item.SeriesName != "" && p.Item.IndexNumber > 0:
		sh, err := s.showByName(p.Item.SeriesName)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		showID, epNum = sh.ID, p.Item.IndexNumber

	case p.Metadata != nil && p.Metadata.GrandparentTitle != "" && p.Metadata.Index > 0:
		sh, err := s.showByName(p.Metadata.GrandparentTitle)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		showID, epNum = sh.ID, p.Metadata.Index

	case p.Show != "" && p.Episode > 0:
		sh, err := s.showByName(p.Show)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		showID, epNum = sh.ID, p.Episode

	default:
		writeErr(w, http.StatusUnprocessableEntity,
			fmt.Errorf("webhook: no usable identity — need a path, or a show name plus episode number"))
		return
	}

	if err := s.recordWatched(showID, epNum, "webhook"); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{
		"marked": true, "show_id": showID, "episode": epNum, "source": "webhook",
	})
}

// readWebhookBody returns the JSON body of a webhook request.
//
// Plex does not send JSON: it posts multipart/form-data with the event in a
// single field named "payload". Everything else sends JSON directly. Reading
// here rather than in the handler keeps the payload parsing one shape.
func readWebhookBody(r *http.Request) ([]byte, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, fmt.Errorf("webhook: parse form: %w", err)
		}
		payload := r.FormValue("payload")
		if payload == "" {
			return nil, fmt.Errorf("webhook: multipart form has no payload field")
		}
		return []byte(payload), nil
	}
	return io.ReadAll(r.Body)
}

// showByName resolves a show by its canonical name or any alias,
// case-insensitively. Media servers send the name as their library spells it,
// which is usually one of the aliases training already taught kishizu.
func (s *Server) showByName(name string) (*store.Show, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	shows, err := s.st.ListShows()
	if err != nil {
		return nil, err
	}
	for _, sh := range shows {
		if strings.EqualFold(sh.CanonicalName, want) {
			return sh, nil
		}
		for _, a := range sh.Aliases {
			if strings.EqualFold(a, want) {
				return sh, nil
			}
		}
	}
	return nil, fmt.Errorf("no tracked show named %q", name)
}

// sweepAfter runs the missing-file check and the sweep in the background.
// Every path that records a watch fires this: the user's intent when marking
// something watched is that the file goes away now, not at the next tick.
// It runs in the background rather than in the handler because it scans
// every show's episodes and touches the filesystem; making the caller wait
// delayed the response by however long the sweep took. The watch itself is
// recorded before this fires, so a slow sweep cannot lose it.
func (s *Server) sweepAfter() {
	if s.watch == nil {
		return
	}
	go func() {
		if missing, err := s.watch.CheckMissing(); err != nil {
			log.Printf("watch: check missing: %v", err)
		} else if len(missing) > 0 {
			log.Printf("watch: %d episode(s) missing from disk", len(missing))
			if s.notifier != nil {
				if err := s.notifier.Send("kishizu: file missing",
					fmt.Sprintf("%d episode(s) vanished before the watch signal", len(missing)),
					notify.PriorityHigh); err != nil {
					log.Printf("notify: %v", err)
				}
			}
		}
		if deleted, kept, err := s.watch.Sweep(); err != nil {
			log.Printf("watch sweep: %v", err)
		} else if len(deleted) > 0 {
			log.Printf("watch: %d deleted, %d kept", len(deleted), len(kept))
		}
	}()
}

// recordWatched latches an episode watched and runs everything a watch
// implies. It is the shared core of /api/watched and /api/webhook: both
// intakes must mean exactly the same thing, or a watch recorded by one path
// and not the other would leave the library in a state neither agrees with.
func (s *Server) recordWatched(showID int64, epNum int, source string) error {
	if err := s.st.UpsertEpisode(showID, epNum, episode.Watched, "", ""); err != nil {
		return err
	}
	log.Printf("watched: show %d ep %d (%s)", showID, epNum, source)
	// Watching is progress just as a download is: if the watch point has
	// reached the schedule's pointer, the pointer must move, or the UI
	// keeps announcing an air date that is already in the past. Best-effort:
	// a failed advance leaves the pointer where it was, and the daily
	// schedule refresh corrects it anyway.
	if err := s.st.AdvanceSchedule(showID, epNum); err != nil {
		log.Printf("%s: advance schedule: %v", source, err)
	}
	if err := s.st.ProjectAirDates(showID); err != nil {
		log.Printf("%s: project air dates: %v", source, err)
	}
	s.sweepAfter()
	return nil
}
