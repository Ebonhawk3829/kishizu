// Package art caches season cover art on disk.
//
// The schedule serves art from its own CDN. Fetching it on every page load
// makes the UI depend on a third party being up, and tells that third party
// which shows are being watched. So art is downloaded once, stored next to
// the database, and served locally.
//
// Art is released when a season ends: the cache holds only what is currently
// airing, which is a handful of small images rather than a growing pile.
package art

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxBytes bounds a cover image. Art is a thumbnail; anything larger is a
// mistake or worse, and we would rather drop it than fill the disk.
const maxBytes = 4 << 20

// Cache stores cover art in a directory, one file per source URL.
type Cache struct {
	dir string
}

// New creates the cache directory if needed.
func New(dir string) (*Cache, error) {
	if dir == "" {
		return nil, fmt.Errorf("art: empty cache dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("art: mkdir %s: %w", dir, err)
	}
	return &Cache{dir: dir}, nil
}

// Dir is the cache directory, for serving the files over HTTP.
func (c *Cache) Dir() string { return c.dir }

// Ensure returns the local filename for a source URL, downloading it first if
// it is not cached yet. An empty URL yields an empty name and no error, so
// callers can pass through shows that have no art.
//
// The write is atomic — download to a temp file, then rename — so a page load
// never sees a half-written image.
func (c *Cache) Ensure(rawURL string) (string, error) {
	if rawURL == "" {
		return "", nil
	}
	name, err := fileName(rawURL)
	if err != nil {
		return "", err
	}
	p := filepath.Join(c.dir, name)
	if _, err := os.Stat(p); err == nil {
		return name, nil
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", fmt.Errorf("art: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("art: fetch: HTTP %d", resp.StatusCode)
	}

	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxBytes)); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// Release deletes the cached copy for a URL. Missing files are not an error:
// releasing is idempotent, and a season can be marked complete more than once.
func (c *Cache) Release(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	name, err := fileName(rawURL)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(c.dir, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// fileName maps a URL to a stable local filename. The hash keeps it unique
// without trusting the remote path; the extension is kept so the content type
// is right when served.
func fileName(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		ext = ".jpg"
	}
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:16] + ext, nil
}
