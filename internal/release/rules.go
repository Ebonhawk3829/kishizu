package release

import (
	"regexp"
	"strings"
)

// IsDub reports whether a title indicates a dubbed release.
//
// Distinct from dual-audio, which carries both tracks and is fine. A dub-only
// release replaces the original vocal performance, which is what makes it
// unwanted.
//
// This lives in release rather than quality because it is a fact about a
// title, not a judgement about it: the parser decides whether a dub is
// present, the policy decides what one costs.
func IsDub(title string) bool {
	t := strings.ToLower(title)
	if strings.Contains(t, "dual") || strings.Contains(t, "multi") {
		return false // both tracks present; not a dub-only release
	}
	return reDubWord.MatchString(t)
}

var reDubWord = regexp.MustCompile(`(?i)(?:^|[^a-z])(dub|dubbed)(?:[^a-z]|$)`)
