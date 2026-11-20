package release

// IsDub reports whether a title indicates an English-dubbed release.
//
// Distinct from dual-audio, which carries both tracks and is fine. A dub-only
// release replaces the original vocal performance, which is what makes it
// unwanted.
//
// This lives in release rather than quality because it is a fact about a
// title, not a judgement about it: the parser decides whether a dub is
// present, the policy decides what one costs.
//
// The name is kept for compatibility. It now means specifically an English
// dub: a Japanese dub of a Korean show is not what a subs-only viewer objects
// to, so it is classified as other-dub and returns false.
func IsDub(title string) bool {
	return Parse(title).IsEnglishDub()
}

// IsEnglishDub reports whether this release is an English dub only.
func (r Release) IsEnglishDub() bool {
	return r.AudioKind == AudioEnglishDub
}
