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

// TestVocabularyReadsGluedBracketGroups: fanset titles glue their tag groups
// together ("[1080p][AVC]"), and a whitespace-only split leaves "1080p][AVC"
// as one field the vocabulary can never match. Brackets are packaging, not
// content, so each tag inside them is its own candidate.
func TestVocabularyReadsGluedBracketGroups(t *testing.T) {
	v := NewVocabulary()
	v.Learn(VocabCodec, "AVC", "h.264")

	for _, title := range []string{
		`[Group] Show - 03 [1080p][AVC]`,
		`Show E05 [AVC][1080p]`,
		`Show E04 1080p AVC-8BIT`,
	} {
		r := Parse(title)
		v.ApplyVocabulary(&r)
		if r.Codec != "h.264" {
			t.Errorf("%s: codec = %q, want h.264", title, r.Codec)
		}
	}
}

// TestVocabularyPrefersTheWholeField: hyphen is part of some tags (WEB-DL,
// B-Global), so the whole field is always tried before its hyphen segments —
// a lesson taught as "B-Global" must resolve, and must not be stolen by a
// segment like "global".
func TestVocabularyPrefersTheWholeField(t *testing.T) {
	v := NewVocabulary()
	v.Learn(VocabService, "B-Global", "bglobal")
	v.Learn(VocabSource, "WEB-DL", "webdl")
	v.Learn(VocabCodec, "Global", "hevc") // a segment that must never win

	for _, tt := range []struct {
		title string
		kind  string
		want  string
	}{
		{`Show E01 1080p B-Global`, "service", "bglobal"},
		{`Show E02 1080p WEB-DL`, "source", "webdl"},
	} {
		r := Parse(tt.title)
		v.ApplyVocabulary(&r)
		got := map[string]string{
			"service": r.Service,
			"source":  r.Source,
		}[tt.kind]
		if got != tt.want {
			t.Errorf("%s: %s = %q, want %q", tt.title, tt.kind, got, tt.want)
		}
	}
}

// TestVocabularyHyphenSegmentsNeedWords: a segment qualifies only when it is
// alphabetic and at least three characters. "AVC" in "AVC-8BIT" is a tag;
// "B" in "B-Global" is a fragment no one could meaningfully teach, and
// "8BIT" is a depth tag the parser already reads.
func TestVocabularyHyphenSegmentsNeedWords(t *testing.T) {
	v := NewVocabulary()
	v.Learn(VocabCodec, "AVC", "h.264")

	// "B" and "8BIT" are not candidates, so nothing mis-resolves; the AVC
	// segment still applies.
	r := Parse(`Show E01 1080p AVC-8BIT`)
	v.ApplyVocabulary(&r)
	if r.Codec != "h.264" {
		t.Errorf("codec = %q, want h.264", r.Codec)
	}
}
