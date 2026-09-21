// Package nyaa fetches and parses Nyaa RSS feeds.
package nyaa

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Item is one entry in a Nyaa RSS feed.
type Item struct {
	Title    string
	Link     string
	GUID     string
	PubDate  time.Time
	Seeders  int
	Leechers int
	Size     string
	InfoHash string
	Category string
	Trusted  bool
	Remake   bool
}

// Indexer is where releases are searched for.
//
// Nyaa is the default and the only indexer kishizu has been tested against,
// but the base URL and category are configurable so a mirror, or a different
// indexer with the same RSS shape, can be used.
type Indexer struct {
	// Base is the indexer root, e.g. https://nyaa.si
	Base string
	// Category is the indexer's category filter, e.g. 1_2 for
	// anime-english-translated on Nyaa.
	Category string
	// UserAgent is sent on every request. Some indexers reject the default
	// Go user agent outright, and a descriptive one lets an admin see who is
	// polling them.
	UserAgent string
	// MinInterval is the shortest time between requests. Politeness: a
	// public indexer should not be hammered, and a burst that looks like a
	// scraper gets the caller blocked.
	MinInterval time.Duration
}

// DefaultIndexer is Nyaa's anime-english-translated category.
func DefaultIndexer() Indexer {
	return Indexer{
		Base:        "https://nyaa.si",
		Category:    "1_2",
		UserAgent:   "kishizu",
		MinInterval: time.Second,
	}
}

// current is the indexer used by the package-level helpers.
//
// A package-level default rather than a parameter on every call, because the
// indexer is a property of the deployment, not of any one query. Set once at
// startup from configuration.
var current = DefaultIndexer()

// SetIndexer sets the indexer used by the package-level helpers. Empty fields
// in ix keep the current value, so a partial config does not blank it out.
func SetIndexer(ix Indexer) {
	if ix.Base != "" {
		current.Base = strings.TrimRight(ix.Base, "/")
	}
	if ix.Category != "" {
		current.Category = ix.Category
	}
	if ix.UserAgent != "" {
		current.UserAgent = ix.UserAgent
	}
	if ix.MinInterval > 0 {
		current.MinInterval = ix.MinInterval
	}
}

// IndexerInUse returns the indexer the package-level helpers will use.
func IndexerInUse() Indexer { return current }

// FeedURL builds a per-show RSS URL.
//
// RSS accepts c (category) and q (search) but ignores p (pagination) and s/o
// (sort). Per-show feeds are what make this work: 75 items covers 14-33 days of
// a single show, so backfill after downtime is free.
func FeedURL(alias string) string {
	return current.Base + "/?page=rss&c=" + url.QueryEscape(current.Category) +
		"&q=" + url.QueryEscape(alias)
}

// FeedURLsFor returns candidate feed URLs for a show, broadest first.
//
// A single query is not enough: searching the full canonical name
// ("BLEACH: Thousand-Year Blood War - The Calamity") returned 28 items and
// missed VARYG entirely, while "BLEACH Sennen Kessen" returned 75 including 14
// VARYG releases. Nyaa's search is a plain substring match, so a long specific
// name excludes groups that write the title differently.
//
// Callers should merge the results, deduplicating on infohash.
func FeedURLsFor(canonical string, aliases []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, FeedURL(q))
	}
	// Shortest alias first: broader queries match more groups.
	cands := append([]string{}, aliases...)
	cands = append(cands, canonical)
	sort.Slice(cands, func(i, j int) bool { return len(cands[i]) < len(cands[j]) })
	for _, c := range cands {
		add(c)
	}
	return out
}

// FetchAll retrieves several feeds and merges them, deduplicating on infohash.
func FetchAll(client *http.Client, urls []string) ([]Item, error) {
	seen := map[string]bool{}
	var out []Item
	for _, u := range urls {
		items, err := Fetch(client, u)
		if err != nil {
			// One bad query should not lose the rest.
			continue
		}
		for _, it := range items {
			key := it.InfoHash
			if key == "" {
				key = it.Title
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, it)
		}
	}
	return out, nil
}

// Fetch retrieves and parses a feed.
func Fetch(client *http.Client, rawURL string) ([]Item, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	// A descriptive user agent: some indexers reject the Go default outright,
	// and an admin reading their logs should be able to see who is polling.
	if ua := current.UserAgent; ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	rateLimit()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nyaa returned %d", resp.StatusCode)
	}
	return Parse(resp.Body)
}

// Rate limiting state. A public indexer is a shared resource: polling every
// show every tick with no floor between requests is the kind of traffic that
// gets a caller blocked, and being blocked looks exactly like "no releases
// found".
var (
	rateMu      sync.Mutex
	lastRequest time.Time
)

// rateLimit waits out the minimum interval between indexer requests.
func rateLimit() {
	interval := current.MinInterval
	if interval <= 0 {
		return
	}
	rateMu.Lock()
	defer rateMu.Unlock()
	if wait := interval - time.Since(lastRequest); wait > 0 {
		time.Sleep(wait)
	}
	lastRequest = time.Now()
}

// ---------- XML shape ----------

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []rawItem `xml:"item"`
	} `xml:"channel"`
}

type rawItem struct {
	Title    string `xml:"title"`
	Link     string `xml:"link"`
	GUID     string `xml:"guid"`
	PubDate  string `xml:"pubDate"`
	Seeders  string `xml:"seeders"`
	Leechers string `xml:"leechers"`
	Size     string `xml:"size"`
	InfoHash string `xml:"infoHash"`
	Category string `xml:"categoryId"`
	Trusted  string `xml:"trusted"`
	Remake   string `xml:"remake"`
}

// timeLayouts covers the RFC1123 variants Nyaa emits.
var timeLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 -0700",
}

func Parse(r io.Reader) ([]Item, error) {
	var f rss
	if err := xml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse rss: %w", err)
	}
	out := make([]Item, 0, len(f.Channel.Items))
	for _, ri := range f.Channel.Items {
		it := Item{
			Title:    strings.TrimSpace(ri.Title),
			Link:     ri.Link,
			GUID:     ri.GUID,
			Size:     ri.Size,
			InfoHash: strings.ToLower(strings.TrimSpace(ri.InfoHash)),
			Category: ri.Category,
			Trusted:  strings.EqualFold(ri.Trusted, "yes"),
			Remake:   strings.EqualFold(ri.Remake, "yes"),
		}
		it.Seeders, _ = strconv.Atoi(ri.Seeders)
		it.Leechers, _ = strconv.Atoi(ri.Leechers)
		it.PubDate = parseTime(ri.PubDate)
		out = append(out, it)
	}
	return out, nil
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
