package nyaa

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleFeed = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:nyaa="https://nyaa.si/xmlns/nyaa">
  <channel>
    <title>Nyaa</title>
    <item>
      <title>[VARYG] Show Name - 07 [1080p]</title>
      <link>https://nyaa.si/view/1</link>
      <guid isPermaLink="false">https://nyaa.si/view/1</guid>
      <pubDate>Sun, 21 Sep 2026 01:02:03 +0000</pubDate>
      <nyaa:seeders>42</nyaa:seeders>
      <nyaa:leechers>7</nyaa:leechers>
      <nyaa:size>1.4 GiB</nyaa:size>
      <nyaa:infoHash>ABCDEF0123456789ABCDEF0123456789ABCDEF01</nyaa:infoHash>
      <nyaa:categoryId>1_2</nyaa:categoryId>
      <nyaa:trusted>Yes</nyaa:trusted>
      <nyaa:remake>No</nyaa:remake>
    </item>
    <item>
      <title>[OtherGroup] Show Name - 07 [1080p x265]</title>
      <link>https://nyaa.si/view/2</link>
      <pubDate>Sun, 21 Sep 2026 02:00:00 +0000</pubDate>
      <nyaa:seeders>3</nyaa:seeders>
      <nyaa:leechers>1</nyaa:leechers>
      <nyaa:infoHash>0123456789ABCDEF0123456789ABCDEF01234567</nyaa:infoHash>
      <nyaa:trusted>no</nyaa:trusted>
      <nyaa:remake>yes</nyaa:remake>
    </item>
  </channel>
</rss>`

// TestParseReadsEveryField: the fields kishizu ranks on (seeders, infohash,
// trusted, remake) all come from the feed. A field that silently reads as
// zero is a ranking that quietly goes wrong.
func TestParseReadsEveryField(t *testing.T) {
	items, err := Parse(strings.NewReader(sampleFeed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	it := items[0]
	if it.Title != "[VARYG] Show Name - 07 [1080p]" {
		t.Errorf("title = %q", it.Title)
	}
	if it.Seeders != 42 {
		t.Errorf("seeders = %d, want 42", it.Seeders)
	}
	if it.Leechers != 7 {
		t.Errorf("leechers = %d, want 7", it.Leechers)
	}
	if it.Size != "1.4 GiB" {
		t.Errorf("size = %q", it.Size)
	}
	if it.Category != "1_2" {
		t.Errorf("category = %q", it.Category)
	}
	if !it.Trusted {
		t.Error("trusted = false, want true")
	}
	if it.Remake {
		t.Error("remake = true, want false")
	}
	// Lowercased: infohashes are compared for dedupe, and Nyaa's casing is
	// not guaranteed to match what a magnet link carries.
	if it.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("infohash = %q, want lowercased", it.InfoHash)
	}
	want := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	if !it.PubDate.Equal(want) {
		t.Errorf("pubdate = %v, want %v", it.PubDate, want)
	}

	// The second item exercises the negative cases.
	if items[1].Trusted {
		t.Error("second item: trusted = true, want false")
	}
	if !items[1].Remake {
		t.Error("second item: remake = false, want true")
	}
}

// TestParseToleratesMissingFields: a partial item must still parse. Nyaa's
// feed varies by uploader, and losing a whole feed over one odd item would
// stop a show from being hunted at all.
func TestParseToleratesMissingFields(t *testing.T) {
	feed := `<rss><channel><item><title>Bare</title></item></channel></rss>`
	items, err := Parse(strings.NewReader(feed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Title != "Bare" {
		t.Errorf("title = %q", items[0].Title)
	}
	if items[0].Seeders != 0 || items[0].Trusted || items[0].Remake {
		t.Errorf("missing fields must read as zero, got %+v", items[0])
	}
	if !items[0].PubDate.IsZero() {
		t.Errorf("an unparseable date must be the zero time, got %v", items[0].PubDate)
	}
}

// TestParseRejectsGarbage: malformed XML must be an error, not an empty feed.
// An empty feed looks like "no releases yet", which is a lie that would stop
// a show being hunted with no explanation.
func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse(strings.NewReader("not xml at all")); err == nil {
		t.Error("expected an error for malformed XML")
	}
}

// TestParseTimeHandlesNyaaVariants: Nyaa emits several RFC1123 spellings.
// A date that fails to parse becomes the zero time, which sorts every item
// to the bottom and breaks recency ordering.
func TestParseTimeHandlesNyaaVariants(t *testing.T) {
	cases := []string{
		"Sun, 21 Sep 2026 01:02:03 +0000",
		"Sun, 21 Sep 2026 01:02:03 GMT",
		"Sun, 2 Jan 2026 01:02:03 +0000", // single-digit day
	}
	for _, c := range cases {
		if got := parseTime(c); got.IsZero() {
			t.Errorf("parseTime(%q) = zero, want a real time", c)
		}
	}
	if got := parseTime(""); !got.IsZero() {
		t.Error("an empty date must be the zero time")
	}
	if got := parseTime("not a date"); !got.IsZero() {
		t.Error("an unparseable date must be the zero time")
	}
}

// TestFeedURLIsPerShow: the feed is per show, with the anime category. A
// global feed would be useless — 75 items of everything is not 75 items of
// one show.
func TestFeedURLIsPerShow(t *testing.T) {
	got := FeedURL("BLEACH Sennen Kessen")
	if !strings.HasPrefix(got, "https://nyaa.si/?page=rss") {
		t.Errorf("url = %q, want the RSS endpoint", got)
	}
	if !strings.Contains(got, "c=1_2") {
		t.Errorf("url = %q, want the anime-english category", got)
	}
	if !strings.Contains(got, "q=BLEACH+Sennen+Kessen") {
		t.Errorf("url = %q, want the query escaped", got)
	}
}

// TestFeedURLsForOrdersBroadestFirst: a long canonical name misses groups
// that write the title differently, so the short aliases must be queried
// first. This is what recovered 14 VARYG releases a full-name search missed.
func TestFeedURLsForOrdersBroadestFirst(t *testing.T) {
	got := FeedURLsFor("BLEACH: Thousand-Year Blood War - The Calamity",
		[]string{"Bleach S17", "BLEACH Sennen Kessen Hen"})
	if len(got) != 3 {
		t.Fatalf("got %d urls, want 3", len(got))
	}
	// Shortest first.
	if !strings.Contains(got[0], "Bleach+S17") {
		t.Errorf("first url = %q, want the shortest alias", got[0])
	}
	if !strings.Contains(got[2], "Thousand-Year") {
		t.Errorf("last url = %q, want the canonical name", got[2])
	}
}

// TestFeedURLsForDeduplicates: the same query twice is two identical HTTP
// requests for the same 75 items.
func TestFeedURLsForDeduplicates(t *testing.T) {
	got := FeedURLsFor("Show", []string{"Show", " Show ", "Other"})
	if len(got) != 2 {
		t.Errorf("got %d urls, want 2 (Show and Other)", len(got))
	}
}

// TestFetchAllDeduplicatesOnInfoHash: every group publishes a distinct
// infohash for the same episode, so merging feeds without dedupe would
// evaluate the same release once per query.
func TestFetchAllDeduplicatesOnInfoHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()

	items, err := FetchAll(nil, []string{srv.URL, srv.URL})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("got %d items, want 2 (deduped across two identical feeds)", len(items))
	}
}

// TestFetchAllSurvivesOneBadFeed: one failing query must not lose the rest.
// A single bad alias would otherwise stop a show being hunted entirely.
func TestFetchAllSurvivesOneBadFeed(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleFeed))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	items, err := FetchAll(nil, []string{bad.URL, good.URL})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("got %d items, want 2 from the good feed", len(items))
	}
}

// TestFetchReportsNonOK: a 500 must be an error, not an empty feed. Silence
// here is indistinguishable from "no releases yet".
func TestFetchReportsNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := Fetch(nil, srv.URL); err == nil {
		t.Error("expected an error for a non-200 response")
	}
}
