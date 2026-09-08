// Package nyaa fetches and parses Nyaa RSS feeds.
package nyaa

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// FeedURL builds a per-show RSS URL.
//
// RSS accepts c (category) and q (search) but ignores p (pagination) and s/o
// (sort). Per-show feeds are what make this work: 75 items covers 14-33 days of
// a single show, so backfill after downtime is free.
func FeedURL(alias string) string {
	return "https://nyaa.si/?page=rss&c=1_2&q=" + url.QueryEscape(alias)
}

// Fetch retrieves and parses a feed.
func Fetch(client *http.Client, rawURL string) ([]Item, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nyaa returned %d", resp.StatusCode)
	}
	return Parse(resp.Body)
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
