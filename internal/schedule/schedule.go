// Package schedule scrapes animeschedule.net for upcoming air dates.
//
// This is a convenience for building the season list and a diagnostic aid
// ("is this a mid-season break or is my client broken?"). It is never
// load-bearing: if the scrape fails, everything else still works.
package schedule

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const URL = "https://animeschedule.net"

// Entry is one show on the schedule.
type Entry struct {
	Title     string
	NextEp    int
	AirsAt    time.Time
	SourceURL string
}

var (
	reTitle = regexp.MustCompile(`(?s)<h2 class="show-title-bar[^"]*">([^<]+)</h2>`)
	reEp    = regexp.MustCompile(`(?s)<span class="show-episode">Ep\s*(\d+)</span>`)
	reTime  = regexp.MustCompile(`(?s)<time datetime="([^"]+)"`)
)

// Fetch retrieves the current schedule.
func Fetch(client *http.Client) ([]Entry, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("animeschedule returned %d", resp.StatusCode)
	}
	return Parse(resp.Body)
}

// Parse extracts entries from the schedule page.
//
// The page is server-rendered, so a plain fetch is enough — no JS. The markup
// groups each show as: title, then episode number, then an absolute timestamp.
func Parse(r io.Reader) ([]Entry, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	s := string(raw)

	// Walk each title and look ahead a bounded distance for its episode/time.
	var out []Entry
	for _, m := range reTitle.FindAllStringSubmatchIndex(s, -1) {
		title := html.UnescapeString(strings.TrimSpace(s[m[2]:m[3]]))
		if title == "" {
			continue
		}
		rest := s[m[1]:]
		if len(rest) > 2000 {
			rest = rest[:2000]
		}
		ep := 0
		if em := reEp.FindStringSubmatch(rest); em != nil {
			fmt.Sscanf(em[1], "%d", &ep)
		}
		var at time.Time
		if tm := reTime.FindStringSubmatch(rest); tm != nil {
			at = parseAirsAt(html.UnescapeString(tm[1]))
		}
		out = append(out, Entry{Title: title, NextEp: ep, AirsAt: at, SourceURL: URL})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no entries parsed from %s (markup may have changed)", URL)
	}
	return out, nil
}

// Find locates a show by fuzzy title match. Returns nil when not found, which is
// normal — the schedule only covers a ~1 week window, so shows on a break or
// airing outside it will be absent.
//
// Scoring is RECALL of the query's tokens against the candidate, not Jaccard.
// That matters because the schedule uses romaji while the user's names are
// English: "BLEACH: Thousand-Year Blood War - The Calamity" vs "BLEACH: Sennen
// Kessen-hen - Kashin-tan" share almost no words, so Jaccard scores near zero
// even though "bleach" alone identifies it. Recall on the distinctive tokens
// handles that; Jaccard does not.
func Find(entries []Entry, name string) *Entry {
	return FindWithAliases(entries, []string{name})
}

// FindWithAliases tries every alias and keeps the best match. Aliases matter
// here for the same reason they matter to the matcher: the schedule's romaji
// title is often the only thing that overlaps.
func FindWithAliases(entries []Entry, aliases []string) *Entry {
	best := 0.0
	var found *Entry
	for _, a := range aliases {
		want := normalise(a)
		if want == "" {
			continue
		}
		for i := range entries {
			have := normalise(entries[i].Title)
			score := recall(want, have)
			if score > best {
				best = score
				found = &entries[i]
			}
		}
	}
	// Require a reasonable match; otherwise we would confidently return the
	// wrong show.
	if best < 0.6 {
		return nil
	}
	return found
}

// parseAirsAt handles the timestamp format animeschedule.net emits.
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

// recall is the fraction of the query's tokens present in the candidate.
func recall(want, have string) float64 {
	ws := strings.Fields(want)
	if len(ws) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, w := range strings.Fields(have) {
		set[w] = true
	}
	hit := 0
	for _, w := range ws {
		if set[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(ws))
}

func normalise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// overlap is Jaccard similarity on word sets.
func overlap(a, b string) float64 {
	as := strings.Fields(a)
	bs := strings.Fields(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	setA := map[string]bool{}
	for _, w := range as {
		setA[w] = true
	}
	hit := 0
	setB := map[string]bool{}
	for _, w := range bs {
		setB[w] = true
		if setA[w] {
			hit++
		}
	}
	union := len(setA)
	for w := range setB {
		if !setA[w] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(hit) / float64(union)
}
