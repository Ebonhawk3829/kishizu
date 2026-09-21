package nyaa

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMain disables rate limiting for the suite. The limiter sleeps between
// requests by design, and a test that waits a second per fetch is a test
// nobody runs.
func TestMain(m *testing.M) {
	current.MinInterval = 0
	m.Run()
}

// restore puts the default indexer back, so one test cannot leak config into
// the next.
func restore(t *testing.T) {
	t.Helper()
	old := IndexerInUse()
	t.Cleanup(func() { current = old })
}

// TestSetIndexerIsConfigurable: the base URL and category are configurable so
// a mirror, or another indexer with the same RSS shape, can be used.
func TestSetIndexerIsConfigurable(t *testing.T) {
	restore(t)
	SetIndexer(Indexer{Base: "https://mirror.example/", Category: "1_3"})
	got := FeedURL("Show")
	if !strings.HasPrefix(got, "https://mirror.example/?page=rss") {
		t.Errorf("url = %q, want the configured base", got)
	}
	if !strings.Contains(got, "c=1_3") {
		t.Errorf("url = %q, want the configured category", got)
	}
}

// TestSetIndexerKeepsUnsetFields: a partial config must not blank out the
// rest. Otherwise configuring only the category would silently drop the base
// URL and every request would go nowhere.
func TestSetIndexerKeepsUnsetFields(t *testing.T) {
	restore(t)
	SetIndexer(Indexer{Category: "1_4"})
	ix := IndexerInUse()
	if ix.Base != "https://nyaa.si" {
		t.Errorf("base = %q, want it kept", ix.Base)
	}
	if ix.Category != "1_4" {
		t.Errorf("category = %q, want 1_4", ix.Category)
	}
}

// TestSetIndexerTrimsTrailingSlash: a trailing slash on the base would
// produce "//?page=rss", which some servers reject.
func TestSetIndexerTrimsTrailingSlash(t *testing.T) {
	restore(t)
	SetIndexer(Indexer{Base: "https://nyaa.si/"})
	if got := FeedURL("Show"); strings.Contains(got, "si//?") {
		t.Errorf("url = %q, want no doubled slash", got)
	}
}

// TestFetchSendsUserAgent: some indexers reject the Go default user agent
// outright, and a descriptive one lets an admin see who is polling them.
func TestFetchSendsUserAgent(t *testing.T) {
	restore(t)
	SetIndexer(Indexer{UserAgent: "kishizu/1.0", MinInterval: 0})

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	if _, err := Fetch(nil, srv.URL); err != nil {
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
	restore(t)
	SetIndexer(Indexer{MinInterval: 150 * time.Millisecond})

	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	for i := 0; i < 3; i++ {
		if _, err := Fetch(nil, srv.URL); err != nil {
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

// TestRateLimitDisabled: a zero interval means no waiting, which is what the
// test suite and anyone running against their own indexer wants.
func TestRateLimitDisabled(t *testing.T) {
	restore(t)
	SetIndexer(Indexer{MinInterval: 0})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := Fetch(nil, srv.URL); err != nil {
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
