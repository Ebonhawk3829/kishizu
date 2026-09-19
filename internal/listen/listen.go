// Package listen polls Nyaa RSS feeds per show and decides what to grab.
//
// The design is deliberately simple: one feed per show, every poll, dedupe on
// infohash, match, filter, then hand the best candidate for each episode to the
// downloader. RSS covers 14-33 days per show, so backfill after downtime is
// free — just read the feed.
package listen

import (
	"fmt"
	"sort"
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
} // DueShows returns the shows whose RSS should be polled right now, with the
// interval each wants.
//
// A show is due when any of its episodes is hunting (aggressive rate) or
// no-release-found (slow safety net). Shows with no air date at all are polled
// on the legacy interval: without a schedule point there is no window to
// reason about, and silently dropping them would be worse than polling.
//
// An UNTRAINED show is never due. Training is what teaches the per-group
// episode offsets, and without them the matcher has nothing to work with:
// offsetAgreement is 0 and no group is known, so confidence cannot reach the
// grab threshold no matter how good the release is. Polling such a show just
// burns requests to conclude what was already known. The UI surfaces these as
// "needs training" so they are visible rather than silently idle.
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
		// A finished season adopted from SeaDex is never polled. It has no air
		// dates and no offsets, so every guard below would either skip it by
		// accident or, worse, poll it aggressively with nothing to anchor the
		// air-date check. Say so explicitly instead of relying on the absence
		// of offsets.
		if sh.Source == store.SourceSeaDex {
			debug.Log("%s: adopted from SeaDex, not polled", sh.CanonicalName)
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
		if sh.MaxEpisode > 0 && l.st.NextUnwatched(sh.ID) > sh.MaxEpisode {
			debug.Log("%s: season complete (next > max %d), not due",
				sh.CanonicalName, sh.MaxEpisode)
			continue
		}

		if d, ok := cycle.PollInterval(states); ok {
			// Only now does training matter: a dormant show costs nothing
			// either way, so there is no point looking up its offsets.
			if !l.isTrained(sh) {
				debug.Log("%s: untrained, not polling", sh.CanonicalName)
				continue
			}
			out[sh] = d
			continue
		}
		if !hasAirDate && l.isTrained(sh) {
			out[sh] = legacy
		}
	}
	return out
}

// isTrained reports whether a show has any learned group offsets.
//
// This is the same test the UI uses for its "untrained" badge, kept in one
// place so the two cannot drift: a show is trained once at least one release
// group's numbering convention is known.
func (l *Listener) isTrained(sh *store.Show) bool {
	offsets, err := l.st.GroupOffsets(sh.ID)
	if err != nil {
		// Fail closed. An untrained show polls and matches nothing, which is
		// wasteful but harmless; a show wrongly believed trained could grab
		// the wrong episode.
		debug.Log("%s: offsets lookup failed: %v", sh.CanonicalName, err)
		return false
	}
	return len(offsets) > 0
}

// pollShow fetches one show's feed and evaluates each item.
//
// Query on every alias, not just the canonical name: Nyaa's search is a plain
// substring match, so a long specific name misses groups that write the title
// differently. Results are merged and deduplicated on infohash.
func (l *Listener) pollShow(sh *store.Show) ([]Decision, error) {
	items, err := nyaa.FetchAll(nil, nyaa.FeedURLsFor(sh.CanonicalName, sh.Aliases))
	if err != nil {
		return nil, err
	}

	m, err := l.st.NewMatcher(sh)
	if err != nil {
		return nil, err
	}

	var out []Decision
	for _, it := range items {
		d := l.evaluate(sh, m, it)
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
// latch, global rules.
func (l *Listener) evaluate(sh *store.Show, m *store.Matcher, it nyaa.Item) Decision {
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

	// 4. Global rules, parsed through the learned vocabulary so a token the
	// parser does not know still reads. These hold for every show and are the
	// whole quality policy: batch and unreadable/sub-floor resolutions are
	// rejected; codec, dub and uncensored are ranked later.
	r := m.Parse(it.Title)
	if rejected, why := release.RuleReject(&r); rejected {
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
// The schedule's next-episode point is the anchor: episodes before it project
// back a week at a time, and a release claiming to be any episode up to n
// cannot predate a week before the oldest such episode. Shows with no
// schedule point are always accepted: the guard is diagnostic-quality data and
// must never block a good grab. Lower bound only — v2 re-uploads land LATE
// and are good candidates, so there is deliberately no upper bound.
func (l *Listener) airDateOK(sh *store.Show, it nyaa.Item) bool {
	if it.PubDate.IsZero() {
		return true
	}
	n, at, _ := l.st.NextEpisode(sh.ID)
	if at == nil || n <= 0 {
		return true
	}
	oldest := at.AddDate(0, 0, -7*(n-1))
	return !it.PubDate.Before(oldest.Add(-24 * time.Hour))
}

// Best picks the highest-ranked candidate per episode from a set of grab
// decisions.
//
// Ordering: group preference (global, set in advance), then the global rules,
// then seeders, then recency. Group comes first because which group posted a
// release says more about its quality than any attribute of the file does.
func Best(decisions []Decision) []Decision {
	// Only grabs, and only one per (show, episode).
	byEp := map[string]Decision{}
	for _, d := range decisions {
		if !d.Grab || d.Episode <= 0 {
			continue
		}
		key := fmt.Sprintf("%d:%d", d.ShowID, d.Episode)
		if cur, ok := byEp[key]; !ok || better(d, cur) {
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
//
// Ordering is: group preference (global order), then the global rules, then
// seeders, then recency. The group order is hardcoded in release/rules.go;
// training never writes it.
func better(a, b Decision) bool {
	ga := groupRankOf(a)
	gb := groupRankOf(b)
	if ga != gb {
		return ga < gb
	}
	ra, rb := ruleRank(a), ruleRank(b)
	if ra != rb {
		return ra < rb
	}
	if a.Item.Seeders != b.Item.Seeders {
		return a.Item.Seeders > b.Item.Seeders
	}
	return a.Item.PubDate.After(b.Item.PubDate)
}

// groupRankOf is where a release's group sits in the global preferred order.
// Unlisted groups sort last but are not excluded — the order is a ranking,
// not an allowlist.
func groupRankOf(d Decision) int {
	r := release.Parse(d.Item.Title)
	return release.GroupRank(r.Group)
}

// ruleRank scores a release against the global rules: codec, resolution, dub
// and uncensored. Lower is better.
//
// These are constants, not per-release grades. Nobody wants a batch or a dub,
// and x264 beats a re-encoded x265, so asking per release was wasted effort.
func ruleRank(d Decision) int {
	r := release.Parse(d.Item.Title)
	return release.RuleRank(&r)
}

// FilterPreferences reduces grab decisions to one per (show, episode), keeping
// the best-ranked candidate.
//
// Called by the run loop: without it, every release for an episode would be
// handed off instead of the best one.
func (l *Listener) FilterPreferences(decisions []Decision) []Decision {
	var out []Decision
	byShow := map[int64][]Decision{}
	for _, d := range decisions {
		if d.Grab {
			byShow[d.ShowID] = append(byShow[d.ShowID], d)
		}
	}
	for _, ds := range byShow {
		out = append(out, Best(ds)...)
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
