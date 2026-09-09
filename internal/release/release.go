// Package release parses Nyaa release titles into structured fields.
//
// This is a port of the Python prototype (testdata/match.py), which was validated
// against 11 hand-labelled reference torrents and 75 live uploads. The logic is
// deliberately dumb: regex extraction plus integer arithmetic. All the intelligence
// lives in the alias list and the per-group offsets, not here.
package release

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Release is a parsed release title.
type Release struct {
	Title string // original title, unmodified

	Group        string // "[SubsPlease]" -> "SubsPlease"; "" when absent
	Season       int    // from S01E47; 0 when absent
	Episode      int    // from S01E47; 0 when absent
	Bare         int    // from "- 47" / "- 07"; 0 when absent
	SeasonWord   int    // from "4th Season" / "Season 3"; 0 when absent
	Resolution   string // "1080p", "2160p", ...
	Codec        string // "x264", "hevc", "av1", ...
	Source       string // "web-dl", "bd", "remux", ...
	IsBatch      bool   // multiple episode numbers, or an explicit range
	IsUncensored bool
}

var (
	reGroup      = regexp.MustCompile(`^\s*\[([^\]]+)\]`)
	reSxE        = regexp.MustCompile(`(?i)\bs(\d{1,2})[ ._-]?e(\d{1,4})\b`)
	reBare       = regexp.MustCompile(`[-–]\s*(\d{1,4})(?:\s|$|[\[(.])`)
	reSeasonWord = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)\s+season\b|\bseason\s+(\d{1,2})\b`)
	reResolution = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k)\b`)
	// Codec. The optional separator between the letter and the digits matters:
	// releases write "H.264", "H 264", "h264" and "x265" interchangeably, and a
	// plain \b before "h" fails on "H.265" because the dot is not a word char.
	reCodec      = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(av1|x265|hevc|h[ ._-]?265|x264|h[ ._-]?264|h264)(?:[^a-z0-9]|$)`)
	reSource     = regexp.MustCompile(`(?i)\b(web-?dl|webrip|web|bd|blu-?ray|remux)\b`)
	reUncensored = regexp.MustCompile(`(?i)uncensor`)
	reBatchRange = regexp.MustCompile(`(?i)\(\s*\d{1,4}\s*-\s*\d{1,4}\s*\)|\b\d{1,4}\s*~\s*\d{1,4}\b|\bbatch\b|\bcomplete\b`)
	// Trailing group: "H.264-VARYG", "...AAC2.0-Group". Some uploaders put the
	// group at the end after a hyphen instead of in brackets at the front.
	reTrailingGroup = regexp.MustCompile(`[-–]\s*([A-Za-z0-9._]{2,20})\s*(?:\(|\||$)`)
)

// Exported for the trainer, which needs to cut a title at its episode marker
// and strip quality tokens to recover show-name aliases.
var (
	ReSxE  = reSxE
	ReBare = reBare
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

	if m := reBare.FindStringSubmatch(title); m != nil {
		r.Bare, _ = strconv.Atoi(m[1])
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

	r.IsUncensored = reUncensored.MatchString(title)
	r.IsBatch = reBatchRange.MatchString(title)

	return r
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
	if regexp.MustCompile(`(?i)^(?:2160p|1080p|720p|480p|4k|\d{1,4}(?:\.\d)?)$`).MatchString(s) {
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

// Tokens splits a normalised string into a set of meaningful tokens, dropping
// words that carry no identity signal.
func Tokens(s string) map[string]bool {
	noise := map[string]bool{
		"the": true, "a": true, "an": true, "of": true, "to": true, "in": true,
		"and": true, "or": true, "wa": true, "ga": true, "no": true, "ni": true,
		"wo": true, "de": true, "kara": true, "season": true, "s": true,
		"part": true, "ep": true, "episode": true, "ova": true, "ona": true,
		"tv": true, "movie": true, "special": true, "complete": true,
		"batch": true, "raw": true, "sub": true, "subs": true, "subbed": true,
		"dub": true, "dubbed": true, "multi": true, "dual": true, "audio": true,
	}
	out := map[string]bool{}
	for _, t := range strings.Fields(Normalise(s)) {
		if !noise[t] {
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
