// Package release parses Nyaa release titles into structured fields.
//
// This is a port of the Python prototype (testdata/match.py), which was
// validated against 11 hand-labelled reference torrents and 75 live uploads.
// The logic is deliberately dumb: regex extraction plus integer arithmetic. All
// the intelligence lives in the alias list and the per-group offsets, not here.
package release

import "strings"

// ResolutionRank orders resolutions so a floor can be compared numerically.
//
// Shared by the trainer (which writes the floor) and the listener (which
// enforces it). They must agree, so the ordering lives here rather than being
// duplicated in each.
var ResolutionRank = map[string]int{
	"480p": 1, "720p": 2, "1080p": 3, "2160p": 4, "4k": 4,
}

// ResRank returns the ordering of a resolution string, or 0 when unknown.
func ResRank(s string) int {
	return ResolutionRank[strings.ToLower(strings.TrimSpace(s))]
}

// NormaliseGroup lowercases and strips the punctuation release groups use
// interchangeably, so "Erai-raws", "Erai_raws" and "Erai raws" are one group.
//
// Shared by the matcher and the store's adapter, which must resolve groups
// identically or an offset learned in one would not apply in the other.
func NormaliseGroup(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(s)
}
