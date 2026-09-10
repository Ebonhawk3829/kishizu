package transmission

import (
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
	if err := c.Add("magnet:?xt=urn:btih:ABC123"); err != nil {
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
	if err := c.Add("magnet:?xt=urn:btih:BAD"); err == nil {
		t.Error("expected an error for a failed add")
	}
}

// TestListParsesTorrents: the fields kishizu needs come back intact.
func TestListParsesTorrents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrents":[
			{"id":1,"name":"Show - E01","hashString":"AAA","status":6,"isFinished":true},
			{"id":2,"name":"Show - E02","hashString":"BBB","status":4,"isFinished":false}]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d torrents, want 2", len(got))
	}
	if got[0].Hash != "AAA" || got[0].Name != "Show - E01" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].IsFinished {
		t.Error("second should not be finished")
	}
}

// TestRemoveSendsHashAndFlag: removal must target the hash and honour the
// delete-local-data flag, since the watch sweeper deletes files itself.
func TestRemoveSendsHashAndFlag(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = buf
		_, _ = w.Write([]byte(`{"result":"success","arguments":{}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Remove("AAA", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !contains(string(body), `"ids":["AAA"]`) || !contains(string(body), `"delete-local-data":false`) {
		t.Errorf("body = %s", body)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(haystack) > 0 && stringContains(haystack, needle))
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
