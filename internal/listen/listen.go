// Package listen polls Nyaa RSS feeds per show and decides what to grab.
//
// The design is deliberately simple: one feed per show, every poll, dedupe on
// infohash, match, filter, then hand the best candidate for each episode to the
// downloader. RSS covers 14-33 days per show, so backfill after downtime is
// free — just read the feed.
package listen

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/debug"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/match"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Decision is what the listener decided about one release, and why.
//
// Every decision carries its reason. When something goes wrong the log is the
// only witness, so "skipped" without a why is not debuggable.
type Decision struct {
	Item       nyaa.Item
	ShowID     int64
	Show       string
	Episode    int
	Grab       bool
	Reason     string
	Confidence float64
}

// Listener polls feeds and produces grab decisions.
type Listener struct {
	st *store.Store
	// Now is overridable in tests.
	Now func() time.Time
}

// New builds a Listener.
func New(st *store.Store) *Listener {
	return &Listener{st: st, Now: time.Now}
}

// PollShow fetches one show's feed and evaluates each item.
func (l *Listener) PollShow(sh *store.Show) ([]Decision, error) {
	return l.pollShow(sh)
}

// DueShows returns the shows whose RSS should be polled right now, with the
// interval each wants.
//
// A show is due when any of its episodes is hunting (aggressive rate) or
// no-release-found (slow safety net). Shows with no air date at all are polled
// on the legacy interval: without a schedule point there is no window to
// reason about, and silently dropping them would be worse than polling.
func (l *Listener) DueShows(legacy time.Duration) map[*store.Show]time.Duration {
	shows, err := l.st.ListShows()
	if err != nil {
		return nil
	}
	out := map[*store.Show]time.Duration{}
	now := l.Now()
	for _, sh := range shows {
		eps, err := l.st.EpisodesForShow(sh.ID)
		if err != nil {
			continue
		}
		var states []cycle.State
		hasAirDate := false
		for _, ep := range eps {
			if ep.AirsAt != nil {
				hasAirDate = true
			}
			states = append(states, cycle.StateOf(ep, now))
		}
		// A show whose next episode is past its season length has finished.
		// Without this it would poll weekly forever, hunting an episode that
		// will never exist.
		if sh.MaxEpisode > 0 && l.nextEpisode(sh) > sh.MaxEpisode {
			debug.Log("%s: season complete (next %d > max %d), not due",
				sh.CanonicalName, l.nextEpisode(sh), sh.MaxEpisode)
			continue
		}

		if d, ok := cycle.PollInterval(states); ok {
			out[sh] = d
			continue
		}
		if !hasAirDate {
			out[sh] = legacy
		}
	}
	return out
}

// nextEpisode is the first episode not yet watched or deleted — the one the
// listener would hunt for next.
func (l *Listener) nextEpisode(sh *store.Show) int {
	eps, err := l.st.EpisodesForShow(sh.ID)
	if err != nil {
		return 1
	}
	next := 1
	for _, ep := range eps {
		if ep.Number >= next &&
			(ep.State == episode.Watched || ep.State == episode.Deleted) {
			next = ep.Number + 1
		}
	}
	return next
}

// Poll fetches every tracked show's feed and decides what to grab.
//
// It returns the decisions rather than performing the handoff, so the caller
// (the web server or a CLI command) stays in control of what actually happens.
func (l *Listener) Poll() ([]Decision, error) {
	shows, err := l.st.ListShows()
	if err != nil {
		return nil, fmt.Errorf("list shows: %w", err)
	}

	var out []Decision
	for _, sh := range shows {
		ds, err := l.pollShow(sh)
		if err != nil {
			log.Printf("listen: %s: %v", sh.CanonicalName, err)
			continue
		}
		out = append(out, ds...)
	}
	return out, nil
}

// pollShow fetches one show's feed and evaluates each item.
func (l *Listener) pollShow(sh *store.Show) ([]Decision, error) {
	// Use the canonical name for the feed query. Aliases are matched by the
	// matcher, not the feed: one query per show keeps the request count low.
	// Query on every alias, not just the canonical name: Nyaa's search is a
	// plain substring match, so a long specific name misses groups that write
	// the title differently. Results are merged and deduplicated on infohash.
	items, err := nyaa.FetchAll(nil, nyaa.FeedURLsFor(sh.CanonicalName, sh.Aliases))
	if err != nil {
		return nil, err
	}

	m, err := l.st.NewMatcher(sh)
	if err != nil {
		return nil, err
	}
	filters, err := l.st.Filters(sh.ID)
	if err != nil {
		return nil, err
	}

	var out []Decision
	for _, it := range items {
		d := l.evaluate(sh, m, filters, it)
		// Every decision is logged in debug mode, including the rejections.
		// "Why was this not grabbed" is the question a live run always asks,
		// and the reason is already computed — it just needs surfacing.
		debug.Trace(fmt.Sprintf("%s ep%d %s", sh.CanonicalName, d.Episode, truncate(it.Title, 60)),
			map[bool]string{true: "GRAB", false: "skip"}[d.Grab]+": "+d.Reason)
		out = append(out, d)
	}
	return out, nil
}

// truncate shortens a title for logging.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// evaluate applies the full pipeline to one release: dedupe, match, episode
// latch, hard filters.
func (l *Listener) evaluate(sh *store.Show, m *store.Matcher, filters []store.Filter, it nyaa.Item) Decision {
	d := Decision{Item: it, ShowID: sh.ID, Show: sh.CanonicalName}

	// 1. Dedupe on infohash. The identity of a release is its infohash, and it
	// is in the RSS, so this works without downloading anything.
	if seen, err := l.st.HasSeen(it.InfoHash); err != nil {
		d.Reason = fmt.Sprintf("seen check: %v", err)
		return d
	} else if seen {
		d.Reason = "already seen"
		return d
	}

	// 2. Match against the show.
	res := match.Match(m, it.Title)
	if !res.Matched {
		d.Reason = "not this show: " + res.Reason
		return d
	}
	d.Episode = res.Episode
	d.Confidence = res.Confidence

	// 3. Episode state guards. This is the resurrection guard: a watched or
	// deleted episode is never re-grabbed, no matter how good the release is.
	if res.Episode > 0 {
		ep, err := l.st.GetEpisode(sh.ID, res.Episode)
		if err != nil {
			d.Reason = fmt.Sprintf("episode lookup: %v", err)
			return d
		}
		if ep != nil && !episode.ParseState(string(ep.State)).MayAutoGrab() {
			d.Reason = fmt.Sprintf("episode %d is %s, not grabbable", res.Episode, ep.State)
			return d
		}
	}

	// 4. Hard filters.
	if why := applyFilters(filters, it.Title); why != "" {
		d.Reason = why
		return d
	}

	// 5. Episode must be readable. A release whose episode number cannot be
	// read cannot be grabbed: there is nothing to record it against, and it
	// would bypass every per-episode guard above.
	if res.Episode <= 0 {
		d.Reason = "episode unreadable, cannot grab"
		return d
	}

	// 6. Air-date guard. If the show's cadence is known, a release published
	// well before this week's air date is for an older episode — either a
	// mis-numbered back-catalogue upload or a batch. Rejecting it prevents
	// grabbing the wrong episode during a show's first run.
	//
	// Lower bound only: v2 re-uploads and remakes land LATE and are good
	// candidates, so there is deliberately no upper bound.
	if !l.airDateOK(sh, it) {
		d.Reason = "published before this week's air date"
		return d
	}

	// 7. Confidence gate. Below this the model is guessing, and a wrong guess
	// downloads the wrong episode — worse than downloading nothing.
	if res.Confidence < match.ConfidentThreshold {
		d.Reason = fmt.Sprintf("confidence %.2f below %.2f", res.Confidence, match.ConfidentThreshold)
		return d
	}

	d.Grab = true
	d.Reason = res.Reason
	return d
}

// airDateOK reports whether a release's publication date is consistent with
// the episode being current.
//
// Two sources of truth, in order of preference:
//
//   - The schedule's exact next-episode air time (next_airs_at). Episodes
//     before it are projected back a week at a time. Authoritative when the
//     show is on the schedule.
//   - The cadence weekday, as a fallback: the most recent occurrence of that
//     weekday, minus a 24h margin for timezone and early-upload slop.
//
// Shows with neither are always accepted: the guard is diagnostic-quality data
// and must never block a good grab. Lower bound only — v2 re-uploads land LATE
// and are good candidates, so there is deliberately no upper bound.
func (l *Listener) airDateOK(sh *store.Show, it nyaa.Item) bool {
	if it.PubDate.IsZero() {
		return true
	}
	now := l.Now()

	// Authoritative: the schedule's next-episode point. Episode n airs at *at;
	// earlier episodes project back a week each. A release claiming to be any
	// episode up to n cannot predate a week before the oldest such episode.
	if n, at, _ := l.st.NextEpisode(sh.ID); at != nil && n > 0 {
		oldest := at.AddDate(0, 0, -7*(n-1))
		return !it.PubDate.Before(oldest.Add(-24 * time.Hour))
	}

	// Fallback: cadence weekday only.
	if sh.CadenceWeekday == nil {
		return true
	}
	daysSince := (int(now.Weekday()) - *sh.CadenceWeekday + 7) % 7
	anchor := now.AddDate(0, 0, -daysSince)
	anchor = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, now.Location())
	return !it.PubDate.Before(anchor.Add(-24 * time.Hour))
}

// applyFilters returns a rejection reason, or "" when the release passes.
//
// Resolution is a floor, not a ladder: >= the minimum passes, below fails, and
// there is no fallback to a lower resolution.
func applyFilters(filters []store.Filter, title string) string {
	r := release.Parse(title)
	for _, f := range filters {
		switch f.Kind {
		case "batch":
			if f.Op == "exclude" && r.IsBatch {
				return "batch excluded"
			}
		case "resolution":
			switch f.Op {
			case "min":
				if r.Resolution == "" {
					return "no resolution tag, floor is " + f.Value
				}
				if release.ResRank(r.Resolution) < release.ResRank(f.Value) {
					return fmt.Sprintf("resolution %s below floor %s", r.Resolution, f.Value)
				}
			case "exclude":
				if strings.EqualFold(r.Resolution, f.Value) {
					return fmt.Sprintf("resolution %s excluded", r.Resolution)
				}
			}
		case "group":
			if f.Op == "exclude" && strings.EqualFold(r.Group, f.Value) {
				return fmt.Sprintf("group %s excluded", r.Group)
			}
		case "source":
			if f.Op == "exclude" && strings.EqualFold(r.Source, f.Value) {
				return fmt.Sprintf("source %s excluded", f.Value)
			}
		case "codec":
			// Codec is a preference, never a hard filter. A wrong codec is
			// still watchable. It is handled by the ranker, not here.
			continue
		}
	}
	return ""
}

// Best picks the highest-ranked candidate per episode from a set of grab
// decisions, applying the preference order.
//
// Lower rank wins. Ties break on seeders, then on more recent publication,
// because a fresher upload is less likely to be dead.
func Best(decisions []Decision, prefs []store.Preference) []Decision {
	// Only grabs, and only one per (show, episode).
	byEp := map[string]Decision{}
	for _, d := range decisions {
		if !d.Grab || d.Episode <= 0 {
			continue
		}
		key := fmt.Sprintf("%d:%d", d.ShowID, d.Episode)
		if cur, ok := byEp[key]; !ok || better(d, cur, prefs) {
			byEp[key] = d
		}
	}

	out := make([]Decision, 0, len(byEp))
	for _, d := range byEp {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ShowID != out[j].ShowID {
			return out[i].ShowID < out[j].ShowID
		}
		return out[i].Episode < out[j].Episode
	})
	return out
}

// better reports whether candidate a beats the current pick.
func better(a, b Decision, prefs []store.Preference) bool {
	ra, rb := rankOf(a, prefs), rankOf(b, prefs)
	if ra != rb {
		return ra < rb
	}
	if a.Item.Seeders != b.Item.Seeders {
		return a.Item.Seeders > b.Item.Seeders
	}
	return a.Item.PubDate.After(b.Item.PubDate)
}

// rankOf sums the preference ranks a release incurs. A release matching a
// preferred codec, group, service and audio accumulates 0; one matching
// demoted values accumulates more.
func rankOf(d Decision, prefs []store.Preference) int {
	r := release.Parse(d.Item.Title)
	sum := 0
	for _, p := range prefs {
		var match bool
		switch p.Kind {
		case "group":
			match = strings.EqualFold(r.Group, p.Value)
		case "codec":
			match = strings.EqualFold(r.Codec, p.Value)
		case "service":
			match = strings.EqualFold(r.Service, p.Value)
		case "audio":
			match = strings.EqualFold(r.Audio, p.Value)
		case "source":
			match = strings.EqualFold(r.Source, p.Value)
		}
		if match {
			sum += p.Rank
		}
	}
	return sum
}

// FilterPreferences reduces grab decisions to one per (show, episode), keeping
// the best-ranked candidate by the show's preferences.
//
// Called by the run loop: without it, every release for an episode would be
// handed off instead of the best one.
func (l *Listener) FilterPreferences(decisions []Decision) []Decision {
	// Group by show so each show's own preferences are applied to it.
	byShow := map[int64][]Decision{}
	for _, d := range decisions {
		if d.Grab {
			byShow[d.ShowID] = append(byShow[d.ShowID], d)
		}
	}
	var out []Decision
	for showID, ds := range byShow {
		prefs, err := l.st.Preferences(showID)
		if err != nil {
			log.Printf("listen: preferences for show %d: %v", showID, err)
			continue
		}
		out = append(out, Best(ds, prefs)...)
	}
	return out
}

// MarkGrabbed records that a release was handed off, so it is not re-grabbed,
// and advances the schedule pointer: the schedule's next-episode point is held
// until a download confirms that episode is real.
func (l *Listener) MarkGrabbed(d Decision) error {
	if err := l.st.MarkSeen(d.Item.InfoHash, d.ShowID, d.Episode); err != nil {
		return err
	}
	if err := l.st.UpsertEpisode(d.ShowID, d.Episode, episode.Downloading, d.Item.InfoHash, d.Item.Title); err != nil {
		return err
	}
	return l.st.AdvanceSchedule(d.ShowID, d.Episode)
}
