package release

import (
	"regexp"
	"strings"
	"sync"
)

// Vocabulary maps the words release groups actually use onto the canonical
// values kishizu reasons about.
//
// The parser is deliberately dumb — regex extraction over a fixed word list.
// That is the right call, because the alternative is widening regexes forever
// to chase every spelling every group invents. But it means a release written
// in an unexpected vocabulary reads as *nothing*: no codec, no resolution, no
// source. Such a release cannot be ranked or filtered, so it silently loses to
// a worse release that happened to use a recognised spelling.
//
// Training closes that gap. When the user corrects a parse they supply both
// halves — the canonical value and the token in the title that meant it — and
// the pair is recorded here. One correction makes every future release using
// that token readable, for every group and every show.
//
// This is why training is worth doing. Offsets are per-group and saturate
// after one example; vocabulary compounds.
type Vocabulary struct {
	mu       sync.RWMutex
	byKind   map[string]map[string]string // kind -> normalised token -> canonical
	canonFor map[string]func(string) string
}

// Vocabulary kinds. Each corresponds to one parsed field.
const (
	VocabResolution = "resolution"
	VocabCodec      = "codec"
	VocabSource     = "source"
	VocabService    = "service"
	VocabAudio      = "audio"
)

// NewVocabulary builds an empty vocabulary.
func NewVocabulary() *Vocabulary {
	return &Vocabulary{
		byKind: map[string]map[string]string{
			VocabResolution: {},
			VocabCodec:      {},
			VocabSource:     {},
			VocabService:    {},
			VocabAudio:      {},
		},
	}
}

// Learn records that a title token means a canonical value.
//
// Both halves are required. The canonical value alone is not enough: knowing a
// release "should be h.264" tells us the answer but not which word in the title
// produced it, so there is nothing to apply to the next release.
func (v *Vocabulary) Learn(kind, token, canonical string) {
	token = strings.TrimSpace(token)
	canonical = strings.TrimSpace(canonical)
	if token == "" || canonical == "" {
		return
	}
	if _, ok := v.byKind[kind]; !ok {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.byKind[kind][vocabKey(token)] = strings.ToLower(canonical)
}

// Lookup resolves a token to its canonical value. Returns "" when unknown.
func (v *Vocabulary) Lookup(kind, token string) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	m, ok := v.byKind[kind]
	if !ok {
		return ""
	}
	return m[vocabKey(token)]
}

// All returns every learned synonym for a kind, for display and export.
func (v *Vocabulary) All(kind string) map[string]string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := map[string]string{}
	for k, val := range v.byKind[kind] {
		out[k] = val
	}
	return out
}

// Load seeds the vocabulary, used when opening the database.
func (v *Vocabulary) Load(entries map[string]map[string]string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for kind, m := range entries {
		if _, ok := v.byKind[kind]; !ok {
			continue
		}
		for tok, canon := range m {
			v.byKind[kind][vocabKey(tok)] = canon
		}
	}
}

// vocabKey normalises a token for lookup: lowercase, and collapse the
// separators groups use interchangeably (H.264 / H 264 / h264).
func vocabKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile(`[ ._+-]+`)
	return re.ReplaceAllString(s, "")
}

// ApplyVocabulary re-reads a title, filling any field the parser missed by
// checking each token against the learned vocabulary.
//
// Only fills gaps. A value the parser already read is left alone: the parser is
// authoritative for spellings it knows, and the vocabulary exists solely for the
// ones it does not.
func (v *Vocabulary) ApplyVocabulary(r *Release) {
	if r == nil || v == nil {
		return
	}
	tokens := tokenise(r.Title)
	for _, tok := range tokens {
		key := vocabKey(tok)
		if key == "" {
			continue
		}
		if r.Resolution == "" {
			if c := v.Lookup(VocabResolution, key); c != "" {
				r.Resolution = c
				continue
			}
		}
		if r.Codec == "" {
			if c := v.Lookup(VocabCodec, key); c != "" {
				r.Codec = c
				continue
			}
		}
		if r.Source == "" {
			if c := v.Lookup(VocabSource, key); c != "" {
				r.Source = c
				continue
			}
		}
		if r.Service == "" {
			if c := v.Lookup(VocabService, key); c != "" {
				r.Service = c
				continue
			}
		}
		if r.Audio == "" {
			if c := v.Lookup(VocabAudio, key); c != "" {
				r.Audio = c
				continue
			}
		}
	}
}

// tokenise splits a title into candidate vocabulary tokens.
//
// Split on whitespace first, then keep the internal separators that matter
// (H.264 stays one token). Splitting only on a letter/digit run would merge
// "1080p CR WEB-DL AVC AAC" into a single token, burying the very word we are
// trying to match.
func tokenise(title string) []string {
	var out []string
	for _, field := range strings.Fields(title) {
		// Trim bracket and punctuation edges: "[1080p" -> "1080p", "AAC]" -> "AAC".
		field = strings.Trim(field, "[](){}<>|/\\.,;:_-")
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}
