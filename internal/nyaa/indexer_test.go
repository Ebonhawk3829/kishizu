package nyaa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The tests here construct their own Client rather than mutating package
// state. They used to call SetIndexer and restore it afterwards, which made
// every test depend on the ones before it having cleaned up, and made the
// whole suite unrunnable in parallel.

// TestClientIsConfigurable: the base URL and category are configurable so a
// mirror, or another indexer with the same RSS shape, can be used.
func TestClientIsConfigurable(t *testing.T) {
	c := New(Indexer{Base: "https://mirror.example/", Category: "1_3"})
	got := c.FeedURL("Show")
	if !strings.HasPrefix(got, "https://mirror.example/?page=rss") {
		t.Errorf("url = %q, want the configured base", got)
	}
	if !strings.Contains(got, "c=1_3") {
		t.Errorf("url = %q, want the configured category", got)
	}
}

// TestClientTrimsTrailingSlash: a trailing slash on the base would produce
// "//?page=rss", which some servers reject.
func TestClientTrimsTrailingSlash(t *testing.T) {
	c := New(Indexer{Base: "https://nyaa.si/"})
	if got := c.FeedURL("Show"); strings.Contains(got, "si//?") {
		t.Errorf("url = %q, want no doubled slash", got)
	}
}

// TestClientsAreIndependent: two clients must not see each other's
// configuration. This is the property the package-level indexer could not
// offer, and the reason a test suite could not run two configurations at once.
func TestClientsAreIndependent(t *testing.T) {
	a := New(Indexer{Base: "https://a.example", Category: "1_2"})
	b := New(Indexer{Base: "https://b.example", Category: "1_3"})
	if got := a.FeedURL("S"); !strings.HasPrefix(got, "https://a.example") {
		t.Errorf("a url = %q, want a.example", got)
	}
	if got := b.FeedURL("S"); !strings.HasPrefix(got, "https://b.example") {
		t.Errorf("b url = %q, want b.example", got)
	}
	if !strings.Contains(a.FeedURL("S"), "c=1_2") || !strings.Contains(b.FeedURL("S"), "c=1_3") {
		t.Error("categories leaked between clients")
	}
}

// TestFetchSendsUserAgent: some indexers reject the Go default user agent
// outright, and a descriptive one lets an admin see who is polling them.
func TestFetchSendsUserAgent(t *testing.T) {
	c := New(Indexer{UserAgent: "kishizu/1.0", MinInterval: 0})

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "kishizu/1.0" {
		t.Errorf("User-Agent = %q, want kishizu/1.0", got)
	}
}

// TestRateLimitSpacesRequests: a public indexer is a shared resource. Polling
// every show every tick with no floor between requests is the traffic that
// gets a caller blocked — and being blocked looks exactly like "no releases
// found", which is a miserable thing to debug.
func TestRateLimitSpacesRequests(t *testing.T) {
	c := New(Indexer{MinInterval: 150 * time.Millisecond})

	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	for i := 0; i < 3; i++ {
		if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	}
	if len(times) != 3 {
		t.Fatalf("got %d requests, want 3", len(times))
	}
	for i := 1; i < len(times); i++ {
		// A small tolerance: time.Sleep guarantees at least the duration, but
		// the clock read on either side can land a fraction short.
		if gap := times[i].Sub(times[i-1]); gap < 140*time.Millisecond {
			t.Errorf("gap %d = %v, want at least ~150ms", i, gap)
		}
	}
}

// TestRateLimitIsPerClient: one client's politeness must not depend on
// another's traffic. A shared limiter would make a second client inherit the
// first one's last-request time, which is wrong in both directions.
func TestRateLimitIsPerClient(t *testing.T) {
	fast := New(Indexer{MinInterval: 0})
	slow := New(Indexer{MinInterval: 150 * time.Millisecond})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	// Warm the slow client so it has a last-request time.
	if _, err := slow.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("slow Fetch: %v", err)
	}

	// The fast client must not wait for it.
	start := time.Now()
	if _, err := fast.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fast Fetch: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("fast client waited %v; rate limiting leaked between clients", elapsed)
	}
}

// TestRateLimitDisabled: a zero interval means no waiting, which is what the
// test suite and anyone running against their own indexer wants.
func TestRateLimitDisabled(t *testing.T) {
	c := New(Indexer{MinInterval: 0})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("3 fetches took %v with no rate limit; want them immediate", elapsed)
	}
}

// TestDefaultIndexerIsUsable: the defaults must be a real indexer, not
// placeholders someone has to fill in before anything works.
func TestDefaultIndexerIsUsable(t *testing.T) {
	ix := DefaultIndexer()
	if ix.Base == "" || ix.Category == "" {
		t.Error("base and category must have defaults")
	}
	if ix.UserAgent == "" {
		t.Error("user agent must have a default; some indexers reject the Go default")
	}
	if ix.MinInterval <= 0 {
		t.Error("min interval must default to something polite, not zero")
	}
}

// TestWithHTTPClientDoesNotMutate: the copy must be independent of the
// original. Swapping the transport underneath a client another goroutine is
// using would be a data race.
func TestWithHTTPClientDoesNotMutate(t *testing.T) {
	base := New(Indexer{MinInterval: 0})
	other := base.WithHTTPClient(&http.Client{Timeout: time.Second})
	if base.hc != nil {
		t.Error("WithHTTPClient mutated the original client")
	}
	if other.hc == nil {
		t.Error("the copy did not receive the client")
	}
	if other.ix != base.ix {
		t.Error("the copy lost the indexer configuration")
	}
}
