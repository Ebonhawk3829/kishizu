package transmission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAddHandlesSessionHandshake: Transmission answers the first request with
// 409 and a session token; the retry must carry it and succeed.
func TestAddHandlesSessionHandshake(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "TOKEN123")
			w.WriteHeader(http.StatusConflict)
			return
		}
		if got := r.Header.Get("X-Transmission-Session-Id"); got != "TOKEN123" {
			t.Errorf("retry carried token %q, want TOKEN123", got)
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.AddWithDir(context.Background(), "magnet:?xt=urn:btih:ABC123", "/downloads/x"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if c.session != "TOKEN123" {
		t.Errorf("session = %q, want TOKEN123", c.session)
	}
}

// TestAddReportsFailure: a non-success result must be an error, not silence.
func TestAddReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":"unrecognized info","arguments":{}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.AddWithDir(context.Background(), "magnet:?xt=urn:btih:BAD", "/downloads/x"); err == nil {
		t.Error("expected an error for a failed add")
	}
}

// TestListParsesTorrents: the fields kishizu needs come back intact.
