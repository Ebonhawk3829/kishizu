package schedule

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one show on the seasonal timetable.
//
// Deliberately smaller than Show: the timetable is a browsing index, not a
// record of truth. It carries enough to pick a show and enough to add it —
// the slug is an exact identity, so the full record can be fetched by it
// afterwards.
type Entry struct {
	// Slug is the animeschedule identifier, an exact identity.
	Slug string `json:"slug"`
	// Title is the display name from the timetable tile.
	Title string `json:"title"`
	// ImageURL is the tile's poster, empty when the tile has none.
	ImageURL string `json:"image_url,omitempty"`
	// AirsAt is the next episode's air time, when the tile carries one.
	AirsAt time.Time `json:"airs_at,omitempty"`
}

// Timetable is a cached snapshot of the seasonal schedule.
type Timetable struct {
	// Fetched is when this snapshot was taken.
	Fetched time.Time `json:"fetched"`
	// Entries is every show on the timetable, ordered by title.
	Entries []Entry `json:"entries"`
}

// TimetableURL is the seasonal timetable page.
//
// A variable rather than a constant so tests can point it at a stub.
var TimetableURL = URL + "/seasonal"

// reTileAnchor matches a timetable tile's link, which carries the slug.
//
// The tile markup pairs an anchor with the title heading that follows it, so
// the title is recovered by forward-parsing to the next heading rather than
// by matching a block shape — the same nesting problem the show page has.
var (
	reTileAnchor = regexp.MustCompile(`<a href="anime/([^"]+)"[^>]*class="show-link"`)
	reTileTitle  = regexp.MustCompile(`<h2 class="show-title-bar">([^<]+)</h2>`)
	reTileImg    = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	reTileTime   = regexp.MustCompile(`<time[^>]+datetime="([^"]+)"`)
)

// FetchTimetable retrieves the seasonal timetable.
func FetchTimetable(client *http.Client) (*Timetable, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(TimetableURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("animeschedule returned %d for the timetable", resp.StatusCode)
	}
	return ParseTimetable(resp.Body)
}

// ParseTimetable extracts the timetable from the seasonal page.
//
// Every field is optional: a tile with no image or no air time still yields an
// entry, because the slug alone is enough to add the show and fetch its full
// record. Dropping a show because its tile was incomplete would make the
// browse list silently wrong.
func ParseTimetable(r interface{ Read([]byte) (int, error) }) (*Timetable, error) {
	// Read the whole page: the parser pairs anchors with the headings that
	// follow them, which needs the document in order rather than line by line.
	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return parseTimetableString(b.String())
}

func parseTimetableString(page string) (*Timetable, error) {
	type anchor struct {
		slug string
		pos  int
	}
	var anchors []anchor
	for _, m := range reTileAnchor.FindAllStringSubmatchIndex(page, -1) {
		anchors = append(anchors, anchor{slug: page[m[2]:m[3]], pos: m[1]})
	}
	if len(anchors) == 0 {
		return nil, fmt.Errorf("no shows found on the timetable page")
	}

	// Each anchor's title is the next heading after it. Pairing by position
	// is what makes this robust: the tile markup nests, so a block regex
	// cannot tell one tile's end from the next tile's start.
	titles := make([]struct {
		pos   int
		title string
	}, 0)
	for _, m := range reTileTitle.FindAllStringSubmatchIndex(page, -1) {
		titles = append(titles, struct {
			pos   int
			title string
		}{pos: m[0], title: html.UnescapeString(strings.TrimSpace(page[m[2]:m[3]]))})
	}

	seen := map[string]bool{}
	out := make([]Entry, 0, len(anchors))
	for i, a := range anchors {
		if seen[a.slug] {
			continue
		}
		seen[a.slug] = true

		e := Entry{Slug: a.slug}
		// The tile's extent is up to the next anchor, or the end of the page.
		end := len(page)
		if i+1 < len(anchors) {
			end = anchors[i+1].pos
		}
		tile := page[a.pos:end]

		for _, t := range titles {
			if t.pos >= a.pos && t.pos < end {
				e.Title = t.title
				break
			}
		}
		if m := reTileImg.FindStringSubmatch(tile); m != nil {
			e.ImageURL = html.UnescapeString(strings.ReplaceAll(m[1], "&amp;", "&"))
		}
		if m := reTileTime.FindStringSubmatch(tile); m != nil {
			e.AirsAt = parseAirsAt(html.UnescapeString(m[1]))
		}
		out = append(out, e)
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return &Timetable{Fetched: time.Now().UTC(), Entries: out}, nil
}

// ---------- cache ----------

// Cache stores a timetable snapshot on disk.
//
// The timetable is fetched once and reused, for two reasons. It is one request
// for every show on the season instead of one request per show, and it keeps
// the browse list working when the site is unreachable — which is the same
// resilience the rest of kishizu has.
type Cache struct {
	mu   sync.Mutex
	path string
	ttl  time.Duration
}

// NewCache builds a timetable cache in dir. ttl is how long a snapshot stays
// fresh; zero means never refresh automatically.
func NewCache(dir string, ttl time.Duration) (*Cache, error) {
	if dir == "" {
		return nil, fmt.Errorf("timetable cache: empty dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("timetable cache: mkdir: %w", err)
	}
	return &Cache{path: filepath.Join(dir, "timetable.json"), ttl: ttl}, nil
}

// DefaultTimetableTTL is how long a snapshot is reused.
//
// A day: the timetable changes when shows are announced or delayed, which is
// not more than daily, and a stale list is still a usable browse list.
const DefaultTimetableTTL = 24 * time.Hour

// Get returns the cached timetable, refreshing it when stale or missing.
//
// A refresh failure falls back to the stale snapshot rather than erroring: a
// cached list from yesterday is far more useful than an empty one, and the
// site being down must not break browsing.
func (c *Cache) Get(client *http.Client) (*Timetable, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if t, err := c.load(); err == nil && t != nil && time.Since(t.Fetched) < c.ttl {
		return t, nil
	}
	fresh, err := FetchTimetable(client)
	if err != nil {
		// Fall back to whatever is on disk, however old.
		if t, lerr := c.load(); lerr == nil && t != nil {
			return t, nil
		}
		return nil, err
	}
	if err := c.save(fresh); err != nil {
		// A failed save is not fatal: the snapshot is still usable in memory.
		return fresh, nil
	}
	return fresh, nil
}

// Seed writes a snapshot into the cache without fetching it.
//
// For tests, and for seeding a cache from a saved snapshot. It exists because
// the cache's own write path is private: a test that wants to assert on
// reading should not have to stand up a fake site to do it.
func (c *Cache) Seed(t *Timetable) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.save(t)
}

// Refresh forces a re-fetch, ignoring the TTL. Used by the UI's refresh button.
func (c *Cache) Refresh(client *http.Client) (*Timetable, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fresh, err := FetchTimetable(client)
	if err != nil {
		return nil, err
	}
	_ = c.save(fresh)
	return fresh, nil
}

func (c *Cache) load() (*Timetable, error) {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	var t Timetable
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (c *Cache) save(t *Timetable) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	// Atomic: write a temp file then rename, so a crash mid-write cannot
	// leave a half-written cache that fails to parse on next start.
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Search filters the timetable by a case-insensitive substring.
//
// Filtering happens here rather than in the UI so the same search works from
// the command line, and so an empty query means "everything" in both places.
func (t *Timetable) Search(q string) []Entry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return t.Entries
	}
	var out []Entry
	for _, e := range t.Entries {
		if strings.Contains(strings.ToLower(e.Title), q) ||
			strings.Contains(strings.ToLower(e.Slug), q) {
			out = append(out, e)
		}
	}
	return out
}
