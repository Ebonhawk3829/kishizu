// Package release parses Nyaa release titles into structured fields.
//
// The logic is intentionally simple: regex extraction plus integer
// arithmetic. All the intelligence lives in the alias list and the per-group
// offsets, not here.
package release

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// AudioKind is the language track a title advertises.
//
// It is deliberately separate from Audio, which is the audio CODEC. The two
// answer different questions: Audio says how the sound is encoded, AudioKind
// says whose voices are on it. A release can be x264 with Japanese audio, or
// x264 with an English dub, and the policy cares about the latter.
type AudioKind string

const (
	// AudioUnknown means the title does not say. Most releases are unknown:
	// they carry no audio marker at all. Unknown is not "sub" or "original" —
	// it is the absence of a claim, and the policy decides what to do with it.
	AudioUnknown AudioKind = ""
	// AudioOriginal is the show's original audio with subtitles: Japanese for
	// an anime, Korean for a manhwa adaptation, and so on.
	AudioOriginal AudioKind = "original"
	// AudioDual carries both the original track and a dub. It includes the
	// original, so a subs-only viewer can watch it.
	AudioDual AudioKind = "dual"
	// AudioEnglishDub is an English dub only. This is the one a subs-only
	// viewer rejects: it replaces the original vocal performance.
	AudioEnglishDub AudioKind = "english-dub"
	// AudioOtherDub is a dub in a language other than English, such as a
	// Japanese dub of a Korean show. The user's rule only rejects English
	// dubs, so this stays acceptable.
	AudioOtherDub AudioKind = "other-dub"
)

// Release is a parsed release title.
type Release struct {
	Title string // original title, unmodified

	Group      string // "[SubsPlease]" -> "SubsPlease"; "" when absent
	Season     int    // from S01E47; 0 when absent
	Episode    int    // from S01E47; 0 when absent
	Bare       int    // from "- 47" / "- 07"; 0 when absent
	SeasonWord int    // from "4th Season" / "Season 3"; 0 when absent
	Resolution string // "1080p", "2160p", ...
	Codec      string // "x264", "hevc", "av1", ...
	Source     string // "web-dl", "bd", "remux", ...
	// Service is the streaming platform the release came from: NF, AMZN, CR,
	// DSNP, BILI. Two releases can be identical in every other field and differ
	// only here, so it has to be captured to tell them apart.
	Service string
	// Audio is the audio codec: AAC, DDP, EAC3, FLAC, Opus. Same reasoning as
	// Service: it is often the only difference between two otherwise identical
	// releases, and it affects whether the file plays on the user's setup.
	Audio string
	// AudioKind is the language track: original, dual, english-dub or
	// other-dub. Empty when the title does not say.
	AudioKind    AudioKind
	IsBatch      bool // multiple episode numbers, or an explicit range
	IsUncensored bool
}

var (
	reGroup = regexp.MustCompile(`^\s*\[([^\]]+)\]`)
	reSxE   = regexp.MustCompile(`(?i)\bs(\d{1,2})[ ._-]?e(\d{1,4})\b`)
	// The optional vN suffix matters: BD packs name files "01v2", and without
	// it the number is not read at all, so a batch resolves to no episode.
	reBare = regexp.MustCompile(`[-–]\s*(\d{1,4})(?:v\d+)?(?:\s|$|[\[(.])`)
	// The form kishizu itself writes on completion: "<Show> - E09.mkv".
	// Without it the tool cannot read the episode number from its own renamed
	// files, which is exactly what the watch signal sends back.
	reLibrary = regexp.MustCompile(`(?i)\s-\sE(\d{1,4})(?:\s|\.|$)`)
	// Bilibili's Chinese episode marker, e.g. "第43话". Bilibili-sourced
	// releases (Doomdos and similar) number episodes this way, and without
	// it the release reads as having no episode and can never be grabbed.
	//
	// A pattern, not a vocabulary entry: the vocabulary maps one token to one
	// value, and 第N话 is a different token for every episode. A pattern
	// generalises across every episode, which is what a numbering convention
	// needs.
	reChineseEpisode = regexp.MustCompile(`第\s*(\d{1,4})\s*话`)
	reSeasonWord     = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)\s+season\b|\bseason\s+(\d{1,2})\b`)
	reResolution     = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k)\b`)
	// Codec. The optional separator between the letter and the digits matters:
	// releases write "H.264", "H 264", "h264" and "x265" interchangeably, and a
	// plain \b before "h" fails on "H.265" because the dot is not a word char.
	reCodec  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(av1|x265|hevc|h[ ._-]?265|x264|h[ ._-]?264|h264)(?:[^a-z0-9]|$)`)
	reSource = regexp.MustCompile(`(?i)\b(web-?dl|webrip|web|bd|blu-?ray|remux)\b`)
	// Streaming service. Matched as a standalone token: a bare \b is not enough
	// because CR appears inside words, and NF/AMZN are short enough to collide
	// with ordinary text if matched loosely.
	reService = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(netflix|nf|amzn|amazon|crunchyroll|cr|dsnp|disney|hulu|bili|bilibili|iqiyi|adn|hidive|funimation|wakanim|animeonegai|max|hbo|paramount|peacock)(?:[^a-z0-9]|$)`)
	// Audio codec. DDP2.0 / DDP5.1 / AAC2.0 all normalise to their base codec.
	reAudio = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(e-?ac-?3|eac3|ddp|dd\+?|dolby|aac|flac|opus|mp3|ac3|truehd|dtshd|dts)(?:[ ._-]?\d(?:\.\d)?)?(?:[^a-z0-9]|$)`)
	// Language track markers. Checked in this order: dual first because it
	// includes the original, then a non-English dub, then explicit original
	// audio, then the generic "dub" that Nyaa's English-translated category
	// uses for English dubs.
	//
	// The dual pattern accepts bare "DUAL" (common in titles like
	// "1080p DUAL AAC2.0") as well as "Dual Audio" and "Multi-Audio". It must
	// NOT match "Multi-Subs": that says there are many subtitle languages, not
	// that the audio has both tracks.
	reDualAudio = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:dual(?:[ ._-]*audio)?|multi[ ._-]*audio)(?:[^a-z0-9]|$)`)
	// A non-English dub. "Japanese Dub" on a Korean show is still a dub, but
	// the user only rejects English dubs, so this is kept separate.
	reOtherDub = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:japanese|jpn|jp|korean|kor|kr|chinese|chi|zh|mandarin|cantonese|taiwanese|thai|vietnamese|indonesian|spanish|french|german|italian|portuguese|russian|arabic|hindi)[ ._-]*(?:dub|dubbed)(?:[^a-z0-9]|$)`)
	// Explicit original audio: "Japanese Audio", "Korean Audio", "Original
	// Audio". Language names alone are too ambiguous (they can mean subtitle
	// languages), so "audio" is required.
	reOriginalAudio = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:original|japanese|jpn|jp|korean|kor|kr|chinese|chi|zh|mandarin|cantonese|taiwanese)[ ._-]*audio(?:[^a-z0-9]|$)`)
	// English dub, either named explicitly or written as a bare "DUB". A bare
	// dub on Nyaa's English-translated category is an English dub by
	// convention; non-English dubs are caught above first.
	reEnglishDub = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:english|eng|en)[ ._-]*(?:dub|dubbed|audio)(?:[^a-z0-9]|$)|(?:^|[^a-z0-9])(?:dub|dubbed)(?:[^a-z0-9]|$)`)
	reUncensored = regexp.MustCompile(`(?i)uncensor`)
	reBatchRange = regexp.MustCompile(`(?i)\(\s*\d{1,4}\s*-\s*\d{1,4}\s*\)|\b\d{1,4}\s*~\s*\d{1,4}\b|\bbatch\b|\bcomplete\b`)
	// Trailing group: "H.264-VARYG", "...AAC2.0-Group". Some uploaders put the
	// group at the end after a hyphen instead of in brackets at the front.
	reTrailingGroup = regexp.MustCompile(`[-–]\s*([A-Za-z0-9._]{2,20})\s*(?:\(|\||$)`)
	// Bare resolutions and episode-ish numbers are not groups either. Package
	// level rather than compiled per call: looksLikeQuality runs for every
	// candidate group in every title, and MustCompile inside a function body
	// recompiles on each one.
	reQualityToken = regexp.MustCompile(`(?i)^(?:2160p|1080p|720p|480p|4k|\d{1,4}(?:\.\d)?)$`)
)

// Exported for the trainer, which needs to cut a title at its episode marker
// and strip quality tokens to recover show-name aliases.
var (
	ReSxE  = reSxE
	ReBare = reBare
	// ReLibrary matches the form kishizu writes on completion, exported so
	// callers can recognise their own output without duplicating the pattern.
	ReLibrary = reLibrary
)

// Parse extracts structured fields from a release title.
func Parse(title string) Release {
	r := Release{Title: title}

	if m := reGroup.FindStringSubmatch(title); m != nil {
		r.Group = strings.TrimSpace(m[1])
	}
	if r.Group == "" {
		if m := reTrailingGroup.FindStringSubmatch(title); m != nil {
			if g := strings.TrimSpace(m[1]); !looksLikeQuality(g) {
				r.Group = g
			}
		}
	}

	if m := reSxE.FindStringSubmatch(title); m != nil {
		r.Season, _ = strconv.Atoi(m[1])
		r.Episode, _ = strconv.Atoi(m[2])
	}

	// Library form, e.g. "Clevatess Season 2 - E09.mkv". Checked after SxE so
	// a real release title keeps its own reading, and only fills in when
	// nothing else found a number.
	if r.Episode == 0 {
		if m := reLibrary.FindStringSubmatch(title); m != nil {
			r.Episode, _ = strconv.Atoi(m[1])
		}
	}

	if m := reBare.FindStringSubmatch(title); m != nil {
		r.Bare, _ = strconv.Atoi(m[1])
	}

	// Bilibili's 第N话. Runs after reBare and fills in only when nothing
	// else found a number, so a title carrying both this and a conventional
	// marker keeps the conventional reading — the characters can also appear
	// in ordinary text, and a bare "- 47" is stronger evidence than a 第
	// that may be part of a sentence.
	if r.Episode == 0 && r.Bare == 0 {
		if m := reChineseEpisode.FindStringSubmatch(title); m != nil {
			r.Episode, _ = strconv.Atoi(m[1])
		}
	}

	if m := reSeasonWord.FindStringSubmatch(title); m != nil {
		if m[1] != "" {
			r.SeasonWord, _ = strconv.Atoi(m[1])
		} else {
			r.SeasonWord, _ = strconv.Atoi(m[2])
		}
	}

	if m := reResolution.FindStringSubmatch(title); m != nil {
		r.Resolution = strings.ToLower(m[1])
	}

	if m := reCodec.FindStringSubmatch(title); m != nil {
		r.Codec = strings.ToLower(strings.ReplaceAll(m[1], " ", ""))
	}

	if m := reSource.FindStringSubmatch(title); m != nil {
		r.Source = strings.ToLower(strings.ReplaceAll(m[1], "-", ""))
	}

	if m := reService.FindStringSubmatch(title); m != nil {
		r.Service = normaliseService(m[1])
	}

	if m := reAudio.FindStringSubmatch(title); m != nil {
		r.Audio = normaliseAudio(m[1])
	}

	r.AudioKind = parseAudioKind(title)

	r.IsUncensored = reUncensored.MatchString(title)
	r.IsBatch = reBatchRange.MatchString(title)

	return r
}

// parseAudioKind classifies the language track from explicit title markers.
//
// Order is the rule. Dual wins because it includes the original track; a
// non-English dub wins over the generic "Dub" so "Japanese Dub" is not treated
// as an English dub; explicit original audio wins over nothing. Unknown stays
// empty: absence of a claim is not a claim of subs.
func parseAudioKind(title string) AudioKind {
	switch {
	case reDualAudio.MatchString(title):
		return AudioDual
	case reOtherDub.MatchString(title):
		return AudioOtherDub
	case reOriginalAudio.MatchString(title):
		return AudioOriginal
	case reEnglishDub.MatchString(title):
		return AudioEnglishDub
	}
	return AudioUnknown
}

// normaliseService maps the many spellings of a platform to one token, so
// "NF", "Netflix" and "netflix" are the same service.
func normaliseService(s string) string {
	switch strings.ToLower(s) {
	case "nf", "netflix":
		return "nf"
	case "amzn", "amazon":
		return "amzn"
	case "cr", "crunchyroll":
		return "cr"
	case "dsnp", "disney":
		return "dsnp"
	case "bili", "bilibili":
		return "bili"
	case "iqiyi":
		return "iqiyi"
	case "adn":
		return "adn"
	case "hidive":
		return "hidive"
	case "funimation":
		return "funimation"
	case "wakanim":
		return "wakanim"
	case "max", "hbo":
		return "max"
	}
	return strings.ToLower(s)
}

// normaliseAudio maps audio spellings to one token. DDP, DD+ and Dolby all mean
// the same thing here, and the channel count (2.0, 5.1) is dropped since it is
// not what distinguishes releases the user cares about.
func normaliseAudio(s string) string {
	switch strings.ToLower(s) {
	case "eac3", "e-ac-3", "e-ac3", "eac-3", "ddp", "dd+", "dd", "dolby":
		return "ddp"
	case "aac":
		return "aac"
	case "flac":
		return "flac"
	case "opus":
		return "opus"
	case "mp3":
		return "mp3"
	case "ac3":
		return "ac3"
	case "truehd":
		return "truehd"
	case "dtshd", "dts":
		return "dts"
	}
	return strings.ToLower(s)
}

// looksLikeQuality rejects trailing tokens that are quality tags rather than
// group names, so "1080p-AAC" does not become the group "AAC".
func looksLikeQuality(s string) bool {
	switch strings.ToLower(s) {
	case "aac", "aac2.0", "aac2", "ac3", "eac3", "ddp", "ddp5.1", "flac", "opus",
		"mp3", "mp4", "mkv", "avi", "x264", "x265", "h264", "h265", "hevc", "av1",
		"10bit", "8bit", "hi10", "dual", "multi", "raw", "sub", "subs", "subbed",
		"dub", "dubbed", "repack", "proper", "v2", "v3", "web", "webdl", "webrip",
		"bd", "bluray", "remux", "nf", "cr", "amzn", "dsnp", "adn", "iqiyi", "bili":
		return true
	}
	// Bare resolutions and episode-ish numbers are not groups either.
	if reQualityToken.MatchString(s) {
		return true
	}
	return false
}

// RawEpisode is the episode number as written in the title, before any offset is
// applied. It is absolute for some groups and local for others — that is exactly
// what the per-group offset resolves.
//
// Returns 0 when no episode number could be read.
func (r Release) RawEpisode() int {
	if r.Episode != 0 {
		return r.Episode
	}
	return r.Bare
}

// SeasonNumber is the season the title explicitly claims, 0 when it makes no
// claim.
//
// SxxEyy wins over a "Season N" word. A title like
//
//	Show S03E15 ... (Show 2nd Season Part 2)
//
// is season 3 by its numbering; the parenthetical is an alternate name, not a
// second claim about the file.
//
// A zero is not "season 0": it means the title did not say, which is common and
// must stay distinguishable from an explicit season.
func (r Release) SeasonNumber() int {
	if r.Season != 0 {
		return r.Season
	}
	return r.SeasonWord
}

// SeasonOf reports the explicit season claim in a title. It is used for show
// aliases, where only the text matters and the rest of a parsed release would
// be noise.
func SeasonOf(title string) int {
	return Parse(title).SeasonNumber()
}

// Normalise lowercases, strips accents and punctuation, and collapses whitespace.
// Used for both alias storage and title comparison so the two always agree.
func Normalise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// noiseWords are the words that carry no identity signal. Package level so
// the map is built once rather than on every call — Tokens runs per alias per
// title in the matcher's hot path.
var noiseWords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "to": true, "in": true,
	"and": true, "or": true, "wa": true, "ga": true, "no": true, "ni": true,
	"wo": true, "de": true, "kara": true, "season": true, "s": true,
	"part": true, "ep": true, "episode": true, "ova": true, "ona": true,
	"tv": true, "movie": true, "special": true, "complete": true,
	"batch": true, "raw": true, "sub": true, "subs": true, "subbed": true,
	"dub": true, "dubbed": true, "multi": true, "dual": true, "audio": true,
}

// Tokens splits a normalised string into a set of meaningful tokens, dropping
// words that carry no identity signal.
func Tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Fields(Normalise(s)) {
		if !noiseWords[t] {
			out[t] = true
		}
	}
	return out
}

// TitleScore is the best recall of any alias against the release title: the
// fraction of the alias's tokens that appear in the title.
//
// Recall rather than precision, because an alias should be *contained* in the
// release title — the title carries extra junk (group, quality, episode) that
// should not penalise the match.
//
// Measured separation on real data: accepted releases scored 1.00, the best
// non-match scored 0.33.
func TitleScore(aliases []string, title string) float64 {
	rt := Tokens(title)
	best := 0.0
	for _, a := range aliases {
		at := Tokens(a)
		if len(at) == 0 || len(rt) == 0 {
			continue
		}
		hit := 0
		for t := range at {
			if rt[t] {
				hit++
			}
		}
		if s := float64(hit) / float64(len(at)); s > best {
			best = s
		}
	}
	return best
}
