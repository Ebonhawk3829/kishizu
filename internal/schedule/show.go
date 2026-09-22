package schedule

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Show is the detail animeschedule.net publishes about one anime, at
// https://animeschedule.net/anime/<slug>.
//
// This is the authoritative record for a season: it carries the episode count
// and every name the season is known by, which are exactly the two things that
// are otherwise typed by hand and get out of date.
type Show struct {
	// Slug is the animeschedule identifier, e.g.
	// "re-zero-kara-hajimeru-isekai-seikatsu-4".
	Slug string
	// Title is the schedule's own display name (usually romaji).
	Title string
	// Episodes is the season length. 0 when the site does not say, which is
	// common for shows announced before their run is confirmed.
	Episodes int
	// Type is the media type: TV, Movie, TV Short, OVA, ONA.
	//
	// It matters because a Movie reports "Episodes: 1", which is a true
	// statement about a film and a disastrous one about a season. Callers
	// filling in a season length must check this first.
	Type string
	// Status is the airing status: "Ongoing", "Finished", "Upcoming".
	Status string
	// AirsAt is when episode 1 airs, from the page's Release Time block — a
	// full timestamp with a UTC offset, e.g. 2027-01-10T19:00+11:00.
	//
	// This is the show's first air time, and for a show that has not premiered
	// it is the only air information available.
	//
	// It is the RAW broadcast time — the earliest native airing. Subbed uploads
	// follow it, so it is the right anchor for "when might a release appear".
	// Starting the hunt a few hours early is harmless; the training is what
	// decides which release is actually correct.
	//
	// Zero when the page has no Release Time block. A schedule site does not
	// list a show without a slot, so in practice that means no usable page.
	AirsAt time.Time
	// LatestEpisode is the episode the page's countdown refers to: the next
	// one to air. 0 when the page carries no countdown, which is how a
	// finished season reads — cleaner than inferring "over" from absence
	// on a weekly timetable.
	LatestEpisode int
	// NextAirsAt is when LatestEpisode airs, from the RAW countdown target
	// (the earliest native airing). Zero when there is no countdown.
	NextAirsAt time.Time
	// Season is the broadcast season, e.g. "Spring 2026".
	Season string
	// Names are the alternative titles, keyed by kind: Romaji, English,
	// Japanese, Abbreviation, Synonyms.
	Names map[string][]string
	// ImageURL is the season's cover art on their CDN.
	ImageURL string
	// MyAnimeListID is the cross-reference the page links to. 0 when absent.
	MyAnimeListID int
}

// Aliases returns the names worth matching a release against.
//
// Only Romaji, English and Synonyms. Two kinds are deliberately excluded:
//
//   - Japanese. Nyaa release titles are romanised; Japanese names never appear
//     in them, so they are dead weight in the alias set. Worse, they are what
//     produced a false match against an unrelated show: a short Japanese
//     abbreviation scored above the threshold on token overlap alone.
//
//   - Abbreviation. Too short to carry identity — "ReZero 4" matches any
//     release containing those tokens.
func (s *Show) Aliases() []string {
	return s.namesOf("Romaji", "English", "Synonyms")
}

// SeasonLength returns the episode count to use as a season length, or 0 when
// the page does not give one.
//
// A Movie reports "Episodes: 1", and that is the correct answer, not a special
// case: a film IS one download. The season-complete check is `next > max`, so
// max=1 still allows episode 1 to be hunted and stops cleanly once it is
// watched. Suppressing it would be worse than using it — with max=0 the
// plausibility bound is lost, and a mis-numbered release could be accepted as
// episode 5 of a film.
func (s *Show) SeasonLength() int {
	if s.Episodes <= 0 {
		return 0
	}
	return s.Episodes
}

func (s *Show) namesOf(kinds ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range kinds {
		for _, n := range s.Names[k] {
			n = strings.TrimSpace(n)
			if n == "" || seen[n] {
				continue
			}
			// Filter by script, not by which field the name came from. The
			// site files Japanese names under "Japanese" but also slips them
			// into "Synonyms", so trusting the field label lets them through.
			if hasJapanese(n) {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// hasJapanese reports whether a string contains kana or kanji.
//
// Nyaa release titles are romanised, so a name in Japanese script can never
// appear in one. Including it only adds noise — and a short Japanese name is
// actively dangerous, since it can clear the alias threshold against an
// unrelated show on token overlap alone.
func hasJapanese(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x3040 && r <= 0x309F, // hiragana
			r >= 0x30A0 && r <= 0x30FF, // katakana
			r >= 0x4E00 && r <= 0x9FFF, // kanji
			r >= 0xFF66 && r <= 0xFF9F: // halfwidth katakana
			return true
		}
	}
	return false
}

// URL is the site root. A variable rather than a constant so tests can point
// it at a stub, the same way PinTimetableURL does for the season list.
var URL = "https://animeschedule.net"

// ShowURL is the canonical page for a slug.
func ShowURL(slug string) string { return URL + "/anime/" + slug }

// PinShowURL fixes the site root, for tests. It returns a function that
// restores the real one.
func PinShowURL(u string) func() {
	old := URL
	URL = u
	return func() { URL = old }
}

// FetchShow retrieves one show's page by slug.
// ErrNotFound is a show page that does not exist.
//
// Distinct from a transport failure, because the two mean opposite things. A
// 404 is evidence: the site is up and says this slug is not a show. A timeout
// or a 500 is the absence of evidence — the site may be down, the network may
// be broken, and nothing about the show has been established either way.
//
// Callers use this to decide whether a failed lookup is a finding or a retry.
var ErrNotFound = errors.New("no such show page")

// FetchShow retrieves one show's page by slug.
//
// Returns ErrNotFound (wrapped) when the page is genuinely absent, so callers
// can tell "this show is not there" from "we could not check". Conflating them
// is how a transient outage gets reported as a show having finished airing.
func FetchShow(client *http.Client, slug string) (*Show, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(ShowURL(slug))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, slug)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("animeschedule returned %d for %q", resp.StatusCode, slug)
	}
	return ParseShow(resp.Body, slug)
}

var (
	reShowTitle = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	reEpisodes  = regexp.MustCompile(
		`(?s)<h3>Episodes</h3>\s*<div[^>]*>(\d+)</div>`)
	reStatus = regexp.MustCompile(
		`(?s)<h3>Status</h3>\s*<div[^>]*>([^<]+)</div>`)
	reType = regexp.MustCompile(
		`(?s)<h3>Type</h3>\s*<a[^>]*>([^<]+)</a>`)
	// Release Time carries the full slot with an offset. The rendered text is
	// localised to the viewer ("07:00 PM" plus a "converted to your timezone"
	// note), so only the datetime attribute is trustworthy.
	//
	// The offset is HTML-escaped: "2027-01-10T19:00&#43;11:00".
	reRelease = regexp.MustCompile(
		`(?s)<time id="release-time-raw"[^>]*datetime="([^"]+)"`)
	// The canonical poster. Preferred over scraping an /anime/jpg/ URL because
	// the page contains many of those (related shows, similar-anime thumbs)
	// and picking one by document order is a guess.
	reOGImage = regexp.MustCompile(
		`<meta[^>]+property="og:image"[^>]+content="([^"]+)"`)
	reSeason = regexp.MustCompile(
		`(?s)<h3>Season</h3>\s*<a[^>]*>([^<]+)</a>`)
	// Alternative names are a flat sequence of headings and values in document
	// order: a heading applies to every value that follows it until the next
	// heading. Synonyms legitimately carry several values.
	//
	// Matching "block" shapes does not work here: the value's own closing
	// </div> is indistinguishable from the wrapper's, so a non-greedy block
	// regex eats the terminator it needs to match on. Scanning both tags in
	// order and attributing each value to the last heading seen avoids the
	// nesting problem entirely.
	reAltTag = regexp.MustCompile(
		`(?s)<span class="alternative-name-heading">([^<]+)</span>` +
			`|<div class="alternative-name"[^>]*>([^<]+)</div>`)
	reMAL     = regexp.MustCompile(`myanimelist\.net/anime/(\d+)`)
	reShowImg = regexp.MustCompile(`https://img\.animeschedule\.net/[^"'\s]+/anime/jpg/[^"'\s]+`)

	// The page's data blob carries the countdown's episode:
	// latestEpisode="18" latestEpisodeBaseline="17" ...
	reLatestEp = regexp.MustCompile(`latestEpisode="(\d+)"`)
	// The RAW countdown target is the earliest native airing of that
	// episode. The Subs/Dub variants sit in their own <time> elements with
	// their own targets; the raw one is in the element carrying the
	// countdown-time-raw class.
	reRawCountdown = regexp.MustCompile(
		`(?s)<time[^>]*class="countdown-time countdown-time-raw"[^>]*data-countdown-target="([^"]+)"`)
)

// ParseShow extracts a Show from a show page.
//
// The page is server-rendered, so a plain fetch is enough. Everything here is
// optional: a missing field yields its zero value rather than an error, because
// the page layout varies by how complete the site's record is and a partial
// record is still more than we had before.
func ParseShow(r io.Reader, slug string) (*Show, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	s := string(raw)

	sh := &Show{Slug: slug, Names: map[string][]string{}}

	if m := reShowTitle.FindStringSubmatch(s); m != nil {
		// "Re:Zero kara Hajimeru Isekai Seikatsu 4 | AnimeSchedule"
		t := strings.TrimSpace(m[1])
		if i := strings.LastIndex(t, "|"); i > 0 {
			t = strings.TrimSpace(t[:i])
		}
		sh.Title = html.UnescapeString(t)
	}
	if m := reEpisodes.FindStringSubmatch(s); m != nil {
		sh.Episodes, _ = strconv.Atoi(m[1])
	}
	if m := reStatus.FindStringSubmatch(s); m != nil {
		sh.Status = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := reType.FindStringSubmatch(s); m != nil {
		sh.Type = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := reRelease.FindStringSubmatch(s); m != nil {
		sh.AirsAt = parseAirsAt(m[1])
	}
	if m := reLatestEp.FindStringSubmatch(s); m != nil {
		sh.LatestEpisode, _ = strconv.Atoi(m[1])
	}
	if m := reRawCountdown.FindStringSubmatch(s); m != nil {
		sh.NextAirsAt = parseAirsAt(m[1])
	}
	if m := reOGImage.FindStringSubmatch(s); m != nil {
		sh.ImageURL = strings.ReplaceAll(html.UnescapeString(m[1]), "&amp;", "&")
	}
	if m := reSeason.FindStringSubmatch(s); m != nil {
		sh.Season = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := reMAL.FindStringSubmatch(s); m != nil {
		sh.MyAnimeListID, _ = strconv.Atoi(m[1])
	}
	// Fallback only: og:image is authoritative, but if it is ever absent the
	// poster is still findable by its path. Last resort, not the main path.
	if sh.ImageURL == "" {
		if m := reShowImg.FindString(s); m != "" {
			sh.ImageURL = strings.ReplaceAll(html.UnescapeString(m), "&amp;", "&")
		}
	}

	// Walk headings and values in document order. Each value belongs to the
	// most recent heading. The page renders the block twice (mobile and
	// desktop variants), so duplicates are filtered as they are added.
	kind := ""
	for _, m := range reAltTag.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			kind = strings.TrimSpace(html.UnescapeString(m[1]))
			continue
		}
		name := strings.TrimSpace(html.UnescapeString(m[2]))
		if name == "" || kind == "" {
			continue
		}
		dup := false
		for _, have := range sh.Names[kind] {
			if have == name {
				dup = true
				break
			}
		}
		if !dup {
			sh.Names[kind] = append(sh.Names[kind], name)
		}
	}

	if sh.Title == "" {
		return nil, fmt.Errorf("no title parsed from %s (markup may have changed)", ShowURL(slug))
	}
	return sh, nil
}

// SlugFromURL extracts the animeschedule slug from a URL or bare slug.
//
// Accepts the forms a user is likely to paste:
//
//	https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
//	animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
//	/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
//	re-zero-kara-hajimeru-isekai-seikatsu-4
//
// Query strings and fragments are dropped. Returns "" when there is no slug to
// be found, which the caller treats as "not a schedule URL" and falls back to
// the plain-name path.
func SlugFromURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Drop query and fragment before looking for the path.
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	// Strip any scheme and host.
	if i := strings.Index(s, "/anime/"); i >= 0 {
		s = s[i+len("/anime/"):]
	} else {
		s = strings.TrimPrefix(s, "anime/")
	}
	s = strings.Trim(s, "/")
	// A slug is a path segment: no slashes, no spaces.
	if s == "" || strings.ContainsAny(s, "/ \t") {
		return ""
	}
	return s
}

// parseAirsAt handles the timestamp formats animeschedule.net emits.
//
// Two traps, both of which silently produce a zero time if missed:
//  1. The offset is HTML-escaped: "2026-09-08T01:00&#43;10:00".
//  2. There are no SECONDS: "01:00", not "01:00:00". Go's time.RFC3339
//     requires seconds and rejects the value outright.
func parseAirsAt(s string) time.Time {
	s = html.UnescapeString(strings.TrimSpace(s))
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04Z07:00", // no seconds
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
