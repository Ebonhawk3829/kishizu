package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestHealthzIsShallow: a healthcheck must not depend on anything external.
//
// If it checked the database or reached out to Nyaa, a slow upstream would
// mark a correctly-running container unhealthy and get it restarted — and
// the whole design is that kishizu keeps working when an upstream is down.
func TestHealthzIsShallow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if got["status"] != "ok" {
		t.Errorf("status = %v, want ok", got["status"])
	}
	// A healthcheck that reads a stale 200 is worse than useless.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// TestSummaryCarriesVersion: the widget feed reports the version too, so a
// dashboard can show which build it is talking to without a second request.
func TestSummaryCarriesVersion(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/summary", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if v, _ := got["version"].(string); v == "" {
		t.Error("summary has no version")
	}
}
