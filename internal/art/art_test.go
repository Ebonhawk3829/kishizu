package art

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestEnsureDownloadsAndCaches: the first call fetches, the second does not.
// Without the cache the UI would hit the schedule's CDN on every page load.
func TestEnsureDownloadsAndCaches(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer srv.Close()

	c, err := New(filepath.Join(t.TempDir(), "art"))
	if err != nil {
		t.Fatal(err)
	}

	name, err := c.Ensure(srv.URL + "/cover.jpg?w=360")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if name == "" {
		t.Fatal("no filename returned")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("hits = %d, want 1", got)
	}

	// The file must exist and hold the body.
	body, err := os.ReadFile(filepath.Join(c.dir, name))
	if err != nil {
		t.Fatalf("read cached: %v", err)
	}
	if string(body) != "fake-jpeg-bytes" {
		t.Errorf("cached body = %q", body)
	}

	// Second call is served from disk.
	if _, err := c.Ensure(srv.URL + "/cover.jpg?w=360"); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("hits after second ensure = %d, want 1 (should be cached)", got)
	}
}

// TestEnsureEmptyURL: shows without art are a no-op, not an error.
func TestEnsureEmptyURL(t *testing.T) {
	c, _ := New(filepath.Join(t.TempDir(), "art"))
	name, err := c.Ensure("")
	if err != nil || name != "" {
		t.Errorf("Ensure(\"\") = %q, %v; want empty, nil", name, err)
	}
}

// TestRelease: a finished season's art is deleted, and releasing twice is fine.
func TestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	}))
	defer srv.Close()

	c, _ := New(filepath.Join(t.TempDir(), "art"))
	url := srv.URL + "/cover.jpg"
	name, err := c.Ensure(url)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Release(url); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.dir, name)); !os.IsNotExist(err) {
		t.Errorf("file still present after release (stat err = %v)", err)
	}
	// Idempotent: a season can be marked complete more than once.
	if err := c.Release(url); err != nil {
		t.Errorf("second release: %v", err)
	}
}

// TestFileNameStable: the same URL always maps to the same file, and the
// extension survives so the served content type is right.
func TestFileNameStable(t *testing.T) {
	a, _ := fileName("https://example.com/x/cover.jpg?w=360&q=90")
	b, _ := fileName("https://example.com/x/cover.jpg?w=360&q=90")
	if a != b {
		t.Errorf("not stable: %q vs %q", a, b)
	}
	if filepath.Ext(a) != ".jpg" {
		t.Errorf("ext = %q, want .jpg", filepath.Ext(a))
	}
	other, _ := fileName("https://example.com/y/other.jpg")
	if other == a {
		t.Error("different URLs collided")
	}
}
