// Package nyaa fetches and parses Nyaa RSS feeds.
package nyaa

import (
	"context"
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

// Client queries one indexer.
//
// The indexer and the rate limiter are fields rather than package state. They
// used to be package-level, set once at startup by SetIndexer, which made
// every call site depend on an ordering that nothing enforced: a query issued
// before the call silently used the default indexer. It also made the tests
// order-sensitive, since each one mutated shared state the others read.
//
// As fields, a Client is constructed with its configuration and cannot be
// half-configured. Two clients with different indexers can coexist, which is
// what a test wants and what a future multi-indexer deployment would need.
type Client struct {
	ix Indexer
	// hc is the HTTP client. Nil means a default one is built per call, which
	// is what the one-shot commands want; a long-lived caller should supply
	// its own so connections are pooled.
	hc *http.Client
	// lim is the rate limiter, held by pointer so a Client can be copied
	// without copying a mutex — and so copies share one budget. Sharing is
	// the point: a copy that got its own limiter would be a way to bypass
	// the politeness the original was configured with.
	lim *limiter
}

// limiter spaces requests out. A public indexer is a shared resource: polling
// every show every tick with no floor between requests is the kind of traffic
// that gets a caller blocked, and being blocked looks exactly like "no
// releases found".
//
// Per client, not per package: two clients are two independent callers as far
// as an indexer is concerned only if they are actually separate, and a shared
// limiter would make one client's politeness depend on another's traffic.
type limiter struct {
	mu          sync.Mutex
	lastRequest time.Time
}

// New builds a Client for an indexer.
//
// A trailing slash on the base is trimmed here rather than at each use, so
// the stored indexer is always in the form the URL builder expects and no
// call site has to remember to normalise it.
func New(ix Indexer) *Client {
	ix.Base = strings.TrimRight(ix.Base, "/")
	return &Client{ix: ix, lim: &limiter{}}
}

// NewDefault builds a Client for the default indexer.
func NewDefault() *Client { return New(DefaultIndexer()) }

// WithHTTPClient returns a copy of c that uses hc for requests.
//
// A copy rather than a mutation: the caller that built c may still be using
// it, and swapping its transport underneath it would be a data race. The
// limiter is shared with the original, so the copy cannot be used to spend a
// second request budget against the same indexer.
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	out := *c
	out.hc = hc
	return &out
}

// Indexer returns the indexer this client queries.
func (c *Client) Indexer() Indexer { return c.ix }

// FeedURL builds a per-show RSS URL.
//
// RSS accepts c (category) and q (search) but ignores p (pagination) and s/o
// (sort). Per-show feeds are what make this work: 75 items covers 14-33 days of
// a single show, so backfill after downtime is free.
func (c *Client) FeedURL(alias string) string {
	return c.ix.Base + "/?page=rss&c=" + url.QueryEscape(c.ix.Category) +
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
func (c *Client) FeedURLsFor(canonical string, aliases []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, c.FeedURL(q))
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
//
// A failing feed is skipped rather than aborting the rest — one bad alias
// should not stop a show being hunted — but the failures are counted and
// returned. Returning only the items made a total indexer outage
// indistinguishable from a quiet week, which is the one case where the
// difference matters most: the caller cannot tell "nothing aired" from
// "nothing was asked".
//
// The error is non-nil only when every feed failed. Partial failures are
// reported through failed, so a caller that wants to be strict can be.
func (c *Client) FetchAll(ctx context.Context, urls []string) (items []Item, failed int, err error) {
	seen := map[string]bool{}
	var out []Item
	for _, u := range urls {
		// A cancelled context abandons the whole batch: there is no point
		// fetching the remaining feeds of a poll that is being shut down.
		if err := ctx.Err(); err != nil {
			return out, failed, err
		}
		got, err := c.Fetch(ctx, u)
		if err != nil {
			failed++
			// One bad query should not lose the rest.
			continue
		}
		for _, it := range got {
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
	if failed > 0 && failed == len(urls) {
		return out, failed, fmt.Errorf("all %d feeds failed", failed)
	}
	return out, failed, nil
}

// Fetch retrieves and parses a feed.
func (c *Client) Fetch(ctx context.Context, rawURL string) ([]Item, error) {
	hc := c.hc
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	// A descriptive user agent: some indexers reject the Go default outright,
	// and an admin reading their logs should be able to see who is polling.
	if ua := c.ix.UserAgent; ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	c.rateLimit()
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nyaa returned %d", resp.StatusCode)
	}
	return Parse(resp.Body)
}

// rateLimit waits out the minimum interval between indexer requests.
func (c *Client) rateLimit() {
	interval := c.ix.MinInterval
	if interval <= 0 {
		return
	}
	c.lim.mu.Lock()
	defer c.lim.mu.Unlock()
	if wait := interval - time.Since(c.lim.lastRequest); wait > 0 {
		time.Sleep(wait)
	}
	c.lim.lastRequest = time.Now()
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
