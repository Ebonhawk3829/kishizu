// Package groups holds the researched baseline of English-anime release
// groups: the default preference order offered by the settings UI, and the
// recommended flag shown beside each name.
//
// This list is data, not policy: the user's own group_order in kishizu.yaml
// replaces it entirely once set. It exists so a fresh deployment's drag-drop
// editor starts from something sensible instead of an empty box.
package groups

// Group is one entry in the baseline list.
type Group struct {
	// Name is how the group appears in release titles.
	Name string `json:"name"`
	// Recommended marks groups with a solid reputation for consistent
	// quality. Shown as a badge in the editor; it is information, not
	// ranking — the order is the ranking.
	Recommended bool `json:"recommended"`
}

// Baseline is the default preference order, best first. Groups below the
// acceptable line (the editor shows the split) rank as unlisted: still
// eligible, never prioritized.
//
// Sourced from community consensus as of 2026: the big seasonal groups
// first, respected quality-focused groups next, fast-but-rushed and
// re-encode groups last. A user who disagrees drags.
var Baseline = []Group{
	{Name: "SubsPlease", Recommended: true},
	{Name: "Erai-raws", Recommended: true},
	{Name: "Commie", Recommended: true},
	{Name: "Kaleido-subs", Recommended: true},
	{Name: "MTBB", Recommended: true},
	{Name: "DameDesuYo", Recommended: true},
	{Name: "LostYears", Recommended: true},
	{Name: "Chyuu", Recommended: true},
	{Name: "EMBER", Recommended: true},
	{Name: "Kaizoku"},
	{Name: "Asakura"},
	{Name: "9volt"},
	{Name: "Vodes"},
	{Name: "GoodJob!Media"},
	{Name: "ReinForce"},
	{Name: "Beatrice-Raws"},
	{Name: "VARYG"},
	{Name: "Yameii"},
	{Name: "Judas"},
	{Name: "LoliHouse"},
	{Name: "Chihiro"},
	{Name: "AnimeRG"},
}

// Recommended reports whether a group carries the recommended flag.
func Recommended(name string) bool {
	for _, g := range Baseline {
		if g.Name == name {
			return g.Recommended
		}
	}
	return false
}
