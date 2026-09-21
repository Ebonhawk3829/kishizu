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

// TimetableURL is the current season's show list.
//
// The site has no "/seasonal" page — that was a guess and 404s. The season
// pages are /seasons/<season>-<year>, and each lists every show for that cour
// as an anime-tile.
//
// A variable rather than a constant so tests can point it at a stub. It is
// refreshed from the date on each fetch, so the browse list does not go stale
// at the turn of a cour.
var TimetableURL = CurrentSeasonURL(time.Now())

// pinnedTimetableURL is set by PinTimetableURL, which tests use to point the
// fetch at a stub. While pinned, FetchTimetable will not recompute the URL
// from the date.
var pinnedTimetableURL bool

// PinTimetableURL fixes the timetable URL, for tests. It returns a function
// that restores the normal date-derived behaviour.
func PinTimetableURL(u string) func() {
	old := TimetableURL
	TimetableURL = u
	pinnedTimetableURL = true
	return func() {
		TimetableURL = old
		pinnedTimetableURL = false
	}
}

// SeasonURL builds the show-list URL for a season and year.
func SeasonURL(season string, year int) string {
	return fmt.Sprintf("%s/seasons/%s-%d", URL, strings.ToLower(season), year)
}

// CurrentSeasonURL returns the show-list URL for the season containing now.
//
// Derived from the date rather than hardcoded, so the browse list does not go
// stale at the turn of a cour. Seasons start in January, April, July and
// October.
func CurrentSeasonURL(now time.Time) string {
	seasons := []string{"winter", "spring", "summer", "fall"}
	season := seasons[int(now.Month()-1)/3]
	return SeasonURL(season, now.Year())
}

// Tile markup, as the season pages actually render it. Each tile is a div
// carrying a route attribute (the slug) and an anime-tile-title heading.
//
// The slug comes from the route attribute rather than the anchor href because
// the tile repeats the link several times (thumbnail, title, buttons) and the
// attribute is the one unambiguous place it appears.
var (
	reTileRoute = regexp.MustCompile(`route="([^"]+)"`)
	reTileTitle = regexp.MustCompile(`<h2 class="anime-tile-title"[^>]*>([^<]+)</h2>`)
	reTileImg   = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	reTileTime  = regexp.MustCompile(`<time[^>]+datetime="([^"]+)"`)
	// The page inlines its CSS, which repeats the tile class names.
	reStyleBlock = regexp.MustCompile(`(?s)<style.*?</style>`)
)

// FetchTimetable retrieves the seasonal timetable.
func FetchTimetable(client *http.Client) (*Timetable, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	// Refresh from the date, so the list follows the season rather than
	// whatever cour was current at startup. A long-running container would
	// otherwise keep browsing a season that has ended.
	//
	// Skipped when a test has pinned TimetableURL to a stub, which is what
	// RefreshSeasonURL is for.
	if !pinnedTimetableURL {
		TimetableURL = CurrentSeasonURL(time.Now())
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
	// Strip style blocks first: the page inlines its CSS, which contains the
	// same class names as the markup and would otherwise match.
	page = reStyleBlock.ReplaceAllString(page, "")

	type tile struct {
		slug string
		pos  int
	}
	var tiles []tile
	for _, m := range reTileRoute.FindAllStringSubmatchIndex(page, -1) {
		tiles = append(tiles, tile{slug: page[m[2]:m[3]], pos: m[0]})
	}
	if len(tiles) == 0 {
		return nil, fmt.Errorf("no shows found on the season page")
	}

	// Titles, by position, so each tile can claim the one that follows it.
	type titled struct {
		pos   int
		title string
	}
	var titles []titled
	for _, m := range reTileTitle.FindAllStringSubmatchIndex(page, -1) {
		titles = append(titles, titled{
			pos:   m[0],
			title: html.UnescapeString(strings.TrimSpace(page[m[2]:m[3]])),
		})
	}

	seen := map[string]bool{}
	out := make([]Entry, 0, len(tiles))
	for i, t := range tiles {
		if seen[t.slug] {
			continue
		}
		seen[t.slug] = true

		e := Entry{Slug: t.slug}
		// The tile's extent runs to the next tile, or the end of the page.
		end := len(page)
		if i+1 < len(tiles) {
			end = tiles[i+1].pos
		}
		body := page[t.pos:end]

		for _, ti := range titles {
			if ti.pos >= t.pos && ti.pos < end {
				e.Title = ti.title
				break
			}
		}
		if m := reTileImg.FindStringSubmatch(body); m != nil {
			e.ImageURL = html.UnescapeString(strings.ReplaceAll(m[1], "&amp;", "&"))
		}
		if m := reTileTime.FindStringSubmatch(body); m != nil {
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
