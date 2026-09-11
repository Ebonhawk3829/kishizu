package train

import (
	"fmt"
	"sort"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// airing is one episode's expected air time.
type airing struct {
	number int
	at     time.Time
}

// InferOffsets derives per-group episode offsets without asking the user.
//
// The schedule gives each episode an air time; the feed gives each release a
// publication date. A release for episode N appears shortly after episode N
// airs, so the episode is the most recent one aired at publication time, and
// the offset is raw - episode.
//
// Only releases published within freshWindow of their episode's air time vote.
// Groups often backfill several episodes at once — VARYG posted raw 7, 8 and
// 10 on a single day — and treating those as current maps them all to the
// newest episode, producing confidently wrong offsets. Requiring freshness
// keeps the vote to genuine same-week uploads.
//
// This is inference, not certainty, but the result is reviewable per show and
// resettable with the Reset button.
func InferOffsets(st *store.Store, sh *store.Show, items []nyaa.Item) (map[string]int, error) {
	eps, err := st.EpisodesForShow(sh.ID)
	if err != nil {
		return nil, err
	}
	var airs []airing
	for _, e := range eps {
		if e.AirsAt != nil {
			airs = append(airs, airing{e.Number, *e.AirsAt})
		}
	}
	if len(airs) == 0 {
		return nil, fmt.Errorf("no air dates for %s", sh.CanonicalName)
	}
	sort.Slice(airs, func(i, j int) bool { return airs[i].number < airs[j].number })

	votes := map[string]map[int]int{}
	for _, it := range items {
		if it.PubDate.IsZero() {
			continue
		}
		r := release.Parse(it.Title)
		raw := r.RawEpisode()
		if raw == 0 {
			continue
		}
		g := r.Group
		if g == "" {
			g = "(none)"
		}
		ep, aired := episodeAt(airs, it.PubDate)
		if ep <= 0 {
			continue
		}
		// Skip backfill: only a release published soon after its episode aired
		// is evidence of that episode's numbering.
		if it.PubDate.Sub(aired) > freshWindow {
			continue
		}
		off := raw - ep
		if votes[g] == nil {
			votes[g] = map[int]int{}
		}
		votes[g][off]++
	}

	out := map[string]int{}
	for g, counts := range votes {
		// Take the HIGHEST offset, not the most common.
		//
		// Groups routinely post the current episode alongside older ones —
		// VARYG publishes three at a time, all within an hour of airing, so a
		// freshness window cannot separate them. The back catalogue is always
		// lower-numbered, so its offsets are lower: the current episode is the
		// maximum. Majority voting picked the backfill and was confidently
		// wrong (VARYG inferred -3 instead of 0).
		best := 0
		first := true
		for off := range counts {
			if first || off > best {
				best = off
				first = false
			}
		}
		if !first {
			out[g] = best
		}
	}
	return out, nil
}

// freshWindow is how soon after airing a release must appear to count as
// evidence. Generous enough for slow groups, tight enough to exclude backfill.
const freshWindow = 36 * time.Hour

// episodeAt returns the episode most recently aired at time t and its air
// time, or 0 when none has aired yet.
func episodeAt(airs []airing, t time.Time) (int, time.Time) {
	cur, at := 0, time.Time{}
	for _, a := range airs {
		if !a.at.After(t) {
			cur, at = a.number, a.at
		}
	}
	return cur, at
}
