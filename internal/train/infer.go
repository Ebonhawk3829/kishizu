package train

import (
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// InferOffsets derives a per-group episode offset from the group's numbering.
//
// A season is episodes 1..max. Most groups number 1:1, so their raw numbers
// already sit in that range and the offset is 0. A group continuing a longer
// run numbers absolutely — BLEACH's Calamity arc is episodes 41..50 of the
// season, so those groups need offset 40.
//
// So there are really only two cases, and the group's own numbers say which.
// If most of what a group has posted sits above max, it is numbering
// absolutely, and we anchor its newest release to the episode we are up to.
// Otherwise it is numbering 1:1 and the offset is 0.
//
// With nothing watched yet there is no position to anchor to, so we align the
// group's earliest release to episode 1 instead.
//
// This is inference, not certainty. It is reviewable per show and resettable
// with the Reset button.
func InferOffsets(st *store.Store, sh *store.Show, items []nyaa.Item) (map[string]int, error) {
	eps, err := st.EpisodesForShow(sh.ID)
	if err != nil {
		return nil, err
	}
	// Anchor to the last episode actually consumed, not the next one. A group's
	// newest release is the latest episode that has aired, which sits one behind
	// our position whenever the next episode has not come out yet.
	lastSeen, watched := 0, false
	for _, e := range eps {
		if e.State == episode.Watched || e.State == episode.Deleted {
			watched = true
			if e.Number > lastSeen {
				lastSeen = e.Number
			}
		}
	}
	max := sh.MaxEpisode
	if max <= 0 {
		max = lastSeen + 13 // unknown season length; allow a generous window
	}

	// Each group's distinct raw numbers.
	raws := map[string]map[int]bool{}
	for _, it := range items {
		r := release.Parse(it.Title)
		raw := r.RawEpisode()
		if raw == 0 {
			continue
		}
		g := r.Group
		if g == "" {
			g = "(none)"
		}
		if raws[g] == nil {
			raws[g] = map[int]bool{}
		}
		raws[g][raw] = true
	}

	out := map[string]int{}
	for g, set := range raws {
		top, low, above := 0, 0, 0
		for r := range set {
			if r > top {
				top = r
			}
			if low == 0 || r < low {
				low = r
			}
			if r > max {
				above++
			}
		}
		// Absolute numbering only if most of the group's releases sit above the
		// season range. A stray high number is noise, not a convention.
		if above*2 <= len(set) {
			out[g] = 0
			continue
		}
		if watched {
			out[g] = top - lastSeen // newest release is the last episode we saw
		} else {
			out[g] = low - 1 // nothing watched: earliest release is episode 1
		}
	}
	return out, nil
}
