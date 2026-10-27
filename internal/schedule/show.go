package schedule

import (
	"encoding/json"
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
	// Source records where this record came from: the show page or the JSON
	// API. It matters for the anchor — an API record carries no countdown, so
	// "no episode here" must not be read as "nothing is scheduled".
	Source string
	// CountdownPresent reports whether the page published a countdown block.
	// A block that is present but not fully readable is a parse failure, not
	// an absence, so callers keep the last known anchor rather than clearing.
	CountdownPresent bool
}

// Sources for Show.Source.
const (
	SourcePage = "page"
	SourceAPI  = "api"
)

// Aliases returns the names worth matching a release against.
//
// Only Romaji, English and Synonyms. Two kinds are excluded:
//
//   - Japanese. Nyaa release titles are romanised; Japanese names never appear
//     in them, and a short Japanese abbreviation can clear the alias threshold
//     against an unrelated show on token overlap alone.
//
//   - Abbreviation. Too short to carry identity — "ReZero 4" matches any
//     release containing those tokens.
func (s *Show) Aliases() []string {
	return s.namesOf("Romaji", "English", "Synonyms")
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
// appear in one. A short Japanese name is actively dangerous in the alias
// set: it can clear the alias threshold against an unrelated show on token
// overlap alone.
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

// The page is server-rendered and carries structured markup: stable class
// names, schema.org itemprop attributes, and machine-readable data-*
// attributes. It is read through the DOM rather than by regex. A regex that
// pins attribute order or whitespace breaks on a cosmetic redesign, which is
// exactly how the countdown parse broke when the site renamed its episode blob.
var (
	reMAL = regexp.MustCompile(`myanimelist\.net/anime/(\d+)`)
	reEp  = regexp.MustCompile(`(?i)^ep\s*(\d+)`)
)

// walk visits n and every descendant in document order (pre-order).
func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

// attr returns the named attribute's value, or "". Attribute names are
// lowercased by the HTML parser, so the key is lowercased to match.
func attr(n *html.Node, key string) string {
	key = strings.ToLower(key)
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// hasClass reports whether the element carries the class.
func hasClass(n *html.Node, want string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == want {
			return true
		}
	}
	return false
}

// text returns an element's text content, trimmed. Character references are
// already resolved by the parser, so no unescaping is needed.
func text(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			b.WriteString(c.Data)
		case html.ElementNode:
			b.WriteString(text(c))
		}
	}
	return strings.TrimSpace(b.String())
}

// findFirst returns the first element in document order that matches.
func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(n, func(c *html.Node) {
		if found == nil && c.Type == html.ElementNode && match(c) {
			found = c
		}
	})
	return found
}

func ParseShow(r io.Reader, slug string) (*Show, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}

	sh := &Show{Slug: slug, Names: map[string][]string{}}

	// Title: the <title> element, minus the " | AnimeSchedule" suffix.
	if t := findFirst(doc, func(n *html.Node) bool { return n.Data == "title" }); t != nil {
		title := text(t)
		if i := strings.LastIndex(title, "|"); i > 0 {
			title = strings.TrimSpace(title[:i])
		}
		sh.Title = title
	}

	// The information block: each .information-content-wrapper pairs an <h3>
	// label with the value element that follows it. The block is rendered
	// twice (mobile and desktop), so each field is only set once.
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || !hasClass(n, "information-content-wrapper") {
			return
		}
		label, value := "", ""
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data == "h3" {
				label = text(c)
				continue
			}
			if value == "" {
				value = text(c)
			}
		}
		switch label {
		case "Episodes":
			if sh.Episodes == 0 {
				sh.Episodes, _ = strconv.Atoi(value)
			}
		case "Status":
			if sh.Status == "" {
				sh.Status = value
			}
		case "Type":
			if sh.Type == "" {
				sh.Type = value
			}
		case "Season":
			if sh.Season == "" {
				sh.Season = value
			}
		}
	})

	// Episode 1's air slot. Only the datetime attribute is trustworthy — the
	// rendered text is localised to the viewer.
	if t := findFirst(doc, func(n *html.Node) bool {
		return n.Data == "time" && attr(n, "id") == "release-time-raw"
	}); t != nil {
		sh.AirsAt = parseAirsAt(attr(t, "datetime"))
	}

	// Countdown: the raw <time> carries the next episode's air time, and the
	// raw countdown's own block carries the episode. Presence of either means
	// the site is publishing a countdown, which is what lets a parse failure
	// be told apart from "nothing scheduled" (see AnchorAction).
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "time" && hasClass(n, "countdown-time-raw") {
			sh.CountdownPresent = true
			if sh.NextAirsAt.IsZero() {
				sh.NextAirsAt = parseAirsAt(attr(n, "data-countdown-target"))
			}
		}
		if hasClass(n, "header-countdown-text-raw") {
			sh.CountdownPresent = true
			if sh.LatestEpisode == 0 {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "span" {
						if m := reEp.FindStringSubmatch(text(c)); m != nil {
							sh.LatestEpisode, _ = strconv.Atoi(m[1])
						}
						break
					}
				}
			}
		}
	})
	// Fallbacks when the countdown block carries no per-episode span: the
	// wrapper's leadingAiredEpisode is the last episode that aired (the next
	// is that plus one), and older pages carried latestEpisode directly.
	if sh.LatestEpisode == 0 {
		if n := findFirst(doc, func(n *html.Node) bool { return attr(n, "leadingAiredEpisode") != "" }); n != nil {
			if v, err := strconv.Atoi(attr(n, "leadingAiredEpisode")); err == nil && v >= 1 {
				sh.LatestEpisode = v + 1
			}
		}
	}
	if sh.LatestEpisode == 0 {
		if n := findFirst(doc, func(n *html.Node) bool { return attr(n, "latestEpisode") != "" }); n != nil {
			sh.LatestEpisode, _ = strconv.Atoi(attr(n, "latestEpisode"))
		}
	}

	// Poster: og:image is canonical. The /anime/jpg/ path is a last resort —
	// the page is full of those (related shows, similar-anime thumbnails), so
	// picking one is a guess, not a source.
	if m := findFirst(doc, func(n *html.Node) bool {
		return n.Data == "meta" && attr(n, "property") == "og:image"
	}); m != nil {
		sh.ImageURL = attr(m, "content")
	}
	if sh.ImageURL == "" {
		if img := findFirst(doc, func(n *html.Node) bool {
			src := attr(n, "src")
			return n.Data == "img" &&
				strings.Contains(src, "img.animeschedule.net") &&
				strings.Contains(src, "/anime/jpg/")
		}); img != nil {
			sh.ImageURL = attr(img, "src")
		}
	}

	// MyAnimeList cross-reference.
	if a := findFirst(doc, func(n *html.Node) bool {
		return n.Data == "a" && reMAL.MatchString(attr(n, "href"))
	}); a != nil {
		if m := reMAL.FindStringSubmatch(attr(a, "href")); m != nil {
			sh.MyAnimeListID, _ = strconv.Atoi(m[1])
		}
	}

	// Alternative names: a heading applies to every value after it until the
	// next heading, and synonyms carry several. The mobile/desktop duplicate
	// block is deduplicated as it is read.
	kind := ""
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if hasClass(n, "alternative-name-heading") {
			kind = text(n)
			return
		}
		if hasClass(n, "alternative-name") {
			name := text(n)
			if name == "" || kind == "" {
				return
			}
			for _, have := range sh.Names[kind] {
				if have == name {
					return
				}
			}
			sh.Names[kind] = append(sh.Names[kind], name)
		}
	})

	if sh.Title == "" {
		return nil, fmt.Errorf("no title parsed from %s (markup may have changed)", ShowURL(slug))
	}
	sh.Source = SourcePage
	return sh, nil
}

// SlugFromURL extracts the animeschedule slug from a URL.
//
// Accepts the forms a user is likely to paste:
//
//	https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
//	animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
//	/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
//
// A bare slug is rejected: a bare word cannot be distinguished from an
// ordinary title, and treating "Bleach" as a slug would send a plain-name
// add down the slug path to a confusing 404. The only unambiguous inputs are
// ones carrying the /anime/ marker. Adding from the browse list passes the
// slug directly and does not go through here.
//
// Query strings and fragments are dropped. Returns "" when there is no slug
// to be found, which the caller treats as "not a schedule URL" and rejects.
//
// Any URL carrying /anime/ is accepted, whatever its host. The slug is then
// verified by fetching the page, which is the real check — a URL on the
// wrong host fails there with "no show at that URL".
func SlugFromURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Drop query and fragment before looking for the path.
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	// The /anime/ marker is what makes the input unambiguous. Without it a
	// bare word is indistinguishable from a show title, and treating it as a
	// slug sends plain-name adds to a confusing 404.
	i := strings.Index(s, "/anime/")
	if i < 0 {
		return ""
	}
	s = s[i+len("/anime/"):]
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

// Finished reports whether the record says the season has ended. Only an
// explicit "Finished" counts; an absent countdown is not evidence, because a
// show on hiatus or awaiting a slot has no countdown either.
func (s *Show) Finished() bool {
	return strings.EqualFold(strings.TrimSpace(s.Status), "Finished")
}

// AnchorAction is what a refresh should do with a show's stored anchor, given
// what a fetch produced. The three outcomes exist because "the site did not
// give me a next episode" and "the site says there is no next episode" look
// identical in the parsed fields but require opposite handling.
type AnchorAction int

const (
	// AnchorKeep leaves the stored anchor alone: the countdown was present but
	// unreadable (a parse failure), the record came from the countdown-less
	// API backup, or the season is finished and the last slot is still history.
	AnchorKeep AnchorAction = iota
	// AnchorSet publishes the fetched episode and air time.
	AnchorSet
	// AnchorClear removes the anchor: the primary page was read cleanly, it
	// published no countdown at all, and the season is not finished, so the
	// site is saying nothing is scheduled.
	AnchorClear
)

// AnchorAction decides what to do with the stored anchor. It is the single
// place that distinguishes a parse failure from a genuine absence:
//
//   - a readable countdown → AnchorSet;
//   - a countdown block that is present but unreadable, or a record from the
//     API backup (which has no countdown), → AnchorKeep, so a site change can
//     never blank every show's anchor again;
//   - a clean primary page with no countdown and a non-finished season →
//     AnchorClear;
//   - a finished season → AnchorKeep, since the last slot is still history.
func (s *Show) AnchorAction() AnchorAction {
	if s.LatestEpisode > 0 && !s.NextAirsAt.IsZero() {
		return AnchorSet
	}
	if s.CountdownPresent || s.Source == SourceAPI {
		return AnchorKeep
	}
	if s.Finished() {
		return AnchorKeep
	}
	return AnchorClear
}

// apiShow is the part of the /api/v3/anime/<route> record mapped onto Show.
//
// The API is the backup source: stable JSON for the fields it carries, but it
// has no countdown and its name list is often partial, so it never supplies
// the anchor and is only consulted when the page cannot be fetched.
type apiShow struct {
	Title             string            `json:"title"`
	PreferredTitle    string            `json:"preferredTitle"`
	Route             string            `json:"route"`
	Episodes          int               `json:"episodes"`
	Status            string            `json:"status"`
	ImageVersionRoute string            `json:"imageVersionRoute"`
	Premier           string            `json:"premier"`
	SubPremier        string            `json:"subPremier"`
	Names             map[string]string `json:"names"`
	MediaTypes        []struct {
		Name string `json:"name"`
	} `json:"mediaTypes"`
	Season struct {
		Title string `json:"title"`
	} `json:"season"`
}

func (a *apiShow) toShow(slug string) *Show {
	sh := &Show{
		Slug:     slug,
		Source:   SourceAPI,
		Status:   strings.TrimSpace(a.Status),
		Episodes: a.Episodes,
		Names:    map[string][]string{},
	}
	sh.Title = strings.TrimSpace(a.PreferredTitle)
	if sh.Title == "" {
		sh.Title = strings.TrimSpace(a.Title)
	}
	if len(a.MediaTypes) > 0 {
		sh.Type = strings.TrimSpace(a.MediaTypes[0].Name)
	}
	sh.Season = strings.TrimSpace(a.Season.Title)
	if a.ImageVersionRoute != "" {
		sh.ImageURL = "https://img.animeschedule.net/production/assets/public/img/" + a.ImageVersionRoute
	}
	sh.AirsAt = parseAirsAt(a.Premier)
	if sh.AirsAt.IsZero() {
		sh.AirsAt = parseAirsAt(a.SubPremier)
	}
	// Map the API's name keys onto the kinds the matcher uses. Keys not in
	// the map are dropped; the API list is partial, which is why it is only a
	// backup.
	kinds := map[string]string{
		"romaji": "Romaji", "english": "English",
		"native": "Japanese", "synonyms": "Synonyms",
	}
	for k, v := range a.Names {
		v = strings.TrimSpace(v)
		if kind, ok := kinds[strings.ToLower(k)]; ok && v != "" {
			sh.Names[kind] = append(sh.Names[kind], v)
		}
	}
	return sh
}

// apiShowURL is the JSON backup endpoint for a show.
func apiShowURL(slug string) string { return URL + "/api/v3/anime/" + slug }

// FetchShowResilient retrieves a show, preferring the page and falling back to
// the JSON API when the page cannot be fetched.
//
// A 404 from the page is authoritative — the site is up and says the slug is
// not a show — so it is returned without consulting the API. Any other page
// failure (timeout, 5xx, a parse that yields no title) is the absence of
// evidence, and the API is tried. The returned Show carries Source so the
// caller knows whether it may hold a countdown.
func FetchShowResilient(client *http.Client, slug string) (*Show, error) {
	info, err := FetchShow(client, slug)
	if err == nil {
		return info, nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil, err
	}
	api, apiErr := fetchShowAPI(client, slug)
	if apiErr != nil {
		// Both sources failed. Return the page error: it is the one the
		// operator expects to see, and the caller keeps its cached anchor.
		return nil, err
	}
	return api, nil
}

// fetchShowAPI retrieves the show's JSON record (the backup source).
func fetchShowAPI(client *http.Client, slug string) (*Show, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(apiShowURL(slug))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, slug)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("animeschedule api returned %d for %q", resp.StatusCode, slug)
	}
	var raw apiShow
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw.toShow(slug), nil
}
