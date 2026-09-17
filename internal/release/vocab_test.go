package release

import "testing"

// TestVocabularyFillsGaps: a release written in an unexpected vocabulary reads
// as nothing, so it cannot be ranked or filtered. Learning the spelling fixes
// every future release that uses it.
func TestVocabularyFillsGaps(t *testing.T) {
	title := `[Erai-raws] Show - 10 [1080p CR WEB-DL AVC AAC][MultiSub]`

	before := Parse(title)
	if before.Codec != "" {
		t.Fatalf("setup: parser already knows AVC, got %q", before.Codec)
	}

	v := NewVocabulary()
	v.Learn(VocabCodec, "AVC", "h.264")

	after := Parse(title)
	v.ApplyVocabulary(&after)
	if after.Codec != "h.264" {
		t.Errorf("codec = %q, want h.264", after.Codec)
	}
}

// TestVocabularyOnlyFillsGaps: a value the parser already read is left alone.
// The parser is authoritative for spellings it knows; the vocabulary exists
// solely for the ones it does not.
func TestVocabularyOnlyFillsGaps(t *testing.T) {
	v := NewVocabulary()
	v.Learn(VocabCodec, "x264", "av1") // deliberately wrong mapping

	r := Parse(`[Group] Show - 01 [1080p x264]`)
	v.ApplyVocabulary(&r)
	if r.Codec != "x264" {
		t.Errorf("codec = %q, want x264; known values must not be overridden", r.Codec)
	}
}

// TestVocabularyNormalisesSeparators: groups write H.264, H 264 and h264
// interchangeably. A learned synonym must match regardless of separator.
func TestVocabularyNormalisesSeparators(t *testing.T) {
	v := NewVocabulary()
	v.Learn(VocabCodec, "AVC", "h.264")

	for _, title := range []string{
		`[Group] Show - 01 [1080p AVC]`,
		`[Group] Show - 01 [1080p A.V.C]`,
		`[Group] Show - 01 [1080p avc]`,
	} {
		r := Parse(title)
		v.ApplyVocabulary(&r)
		if r.Codec != "h.264" {
			t.Errorf("%s: codec = %q, want h.264", title, r.Codec)
		}
	}
}

// TestVocabularyNeedsBothHalves: the canonical value alone is not enough.
// Knowing a release "should be h.264" gives the answer but not which word
// produced it, so there is nothing to apply to the next release.
func TestVocabularyNeedsBothHalves(t *testing.T) {
	v := NewVocabulary()
	v.Learn(VocabCodec, "", "h.264")   // no token
	v.Learn(VocabCodec, "AVC", "")     // no canonical
	v.Learn(VocabCodec, "  ", "h.264") // blank token

	r := Parse(`[Group] Show - 01 [1080p AVC]`)
	v.ApplyVocabulary(&r)
	if r.Codec != "" {
		t.Errorf("codec = %q, want empty; incomplete pairs must not be recorded", r.Codec)
	}
}

// TestVocabularyIsGlobal: one correction applies to every group and every show,
// which is what makes training compound rather than saturate per group.
func TestVocabularyIsGlobal(t *testing.T) {
	v := NewVocabulary()
	v.Learn(VocabResolution, "FHD", "1080p")

	for _, title := range []string{
		`[GroupA] Show One - 01 [FHD]`,
		`[GroupB] Show Two - 02 [FHD AAC]`,
	} {
		r := Parse(title)
		v.ApplyVocabulary(&r)
		if r.Resolution != "1080p" {
			t.Errorf("%s: resolution = %q, want 1080p", title, r.Resolution)
		}
	}
}

// TestVocabularyRejectsUnknownKind: a typo in the kind must not silently create
// a new category that nothing reads.
func TestVocabularyRejectsUnknownKind(t *testing.T) {
	v := NewVocabulary()
	v.Learn("banana", "AVC", "h.264")
	if got := v.Lookup("banana", "AVC"); got != "" {
		t.Errorf("unknown kind returned %q", got)
	}
}
