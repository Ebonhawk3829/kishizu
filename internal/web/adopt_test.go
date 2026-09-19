package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/store"
)

func testServerWithAdopt(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAdopt(t.TempDir(), t.TempDir(), "http://127.0.0.1:1/transmission/rpc")
	return srv, st
}

func postJSON(t *testing.T, srv *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
	return rec
}

// TestAdoptPreviewRejectsBadURL: a URL with no entry id must be refused rather
// than silently resolving to entry 0.
func TestAdoptPreviewRejectsBadURL(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt/preview", map[string]string{"url": "https://example.com/nope"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptRejectsDuplicateEpisode: two files both tagged episode 1 would race
// in the reconciler, and which one won would depend on directory order. This
// must be caught before anything reaches Transmission.
func TestAdoptRejectsDuplicateEpisode(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files": []map[string]any{
			{"name": "a.mkv", "include": true, "episode": 1},
			{"name": "b.mkv", "include": true, "episode": 1},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("assigned twice")) {
		t.Errorf("body = %s, want a duplicate-episode error", rec.Body.String())
	}
}

// TestAdoptRejectsOutOfRange: the dropdown bounds the choice, so a number
// outside it is a mistake worth refusing.
func TestAdoptRejectsOutOfRange(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files": []map[string]any{
			{"name": "a.mkv", "include": true, "episode": 99},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptRequiresTitle: without a title there is nothing to name the show or
// the library directory after.
func TestAdoptRequiresTitle(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "   ",
		"info_hash": "abc",
		"files":     []map[string]any{{"name": "a.mkv", "include": true, "episode": 1}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptRejectsEmptySelection: adopting nothing is a mistake, not a no-op.
func TestAdoptRejectsEmptySelection(t *testing.T) {
	srv, _ := testServerWithAdopt(t)
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files":     []map[string]any{{"name": "a.mkv", "include": false, "episode": 0}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAdoptDisabledWithoutConfig: a server with no staging, library or RPC
// configured must say so rather than creating rows that can never resolve.
func TestAdoptDisabledWithoutConfig(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, srv, "/api/adopt", map[string]any{
		"url":       "https://releases.moe/1/",
		"title":     "Show",
		"info_hash": "abc",
		"files":     []map[string]any{{"name": "a.mkv", "include": true, "episode": 1}},
	})
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501: %s", rec.Code, rec.Body.String())
	}
}
