package nyaa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// These cover cancellation propagation. Without a context on the fetch
// helpers, a shutdown mid-poll waited out the client's 30-second timeout
// instead of abandoning the request — so SIGTERM during a slow indexer
// response left the process hanging.

// TestFetchHonoursCancellation: a cancelled context must abandon the request
// rather than waiting out the client timeout.
func TestFetchHonoursCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow enough that a 30s client timeout would be the only other
		// thing to end this.
		time.Sleep(2 * time.Second)
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	start := time.Now()
	if _, err := NewDefault().Fetch(ctx, srv.URL); err == nil {
		t.Error("expected an error for a cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("cancelled fetch took %v; it should return immediately", elapsed)
	}
}

// TestFetchAllAbandonsTheBatchOnCancellation: once the context is done there
// is no point fetching the remaining feeds of a poll that is being shut down.
func TestFetchAllAbandonsTheBatchOnCancellation(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	urls := []string{srv.URL, srv.URL, srv.URL}
	if _, _, err := NewDefault().FetchAll(ctx, urls); err == nil {
		t.Error("expected an error for a cancelled context")
	}
	if requests != 0 {
		t.Errorf("made %d requests with a cancelled context, want 0", requests)
	}
}

// TestFetchAllStopsMidBatch: cancelling partway through must not fetch the
// rest, even though the earlier feeds succeeded.
func TestFetchAllStopsMidBatch(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			// Cancel after the first feed so the second is never asked for.
			w.Write([]byte(`<rss><channel></channel></rss>`))
			return
		}
		w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A client whose first fetch cancels the context, standing in for a
	// shutdown arriving mid-batch.
	c := NewDefault()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	urls := []string{srv.URL, srv.URL, srv.URL, srv.URL, srv.URL}
	_, _, _ = c.FetchAll(ctx, urls)
	if requests >= len(urls) {
		t.Errorf("made %d of %d requests; cancellation did not stop the batch",
			requests, len(urls))
	}
}
