package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log"
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
	// Title is the display name from the timetable tile, usually romaji.
	Title string `json:"title"`
	// EnglishTitle is the show's English name, empty until the entry has been
	// enriched from its own page.
	//
	// It is not on the tile: the season page lists one name per show. Getting
	// the English one means fetching the show's page, which is one request per
	// show — so it is filled in by Enrich rather than by the timetable fetch,
	// and cached with the snapshot rather than re-fetched on every browse.
	EnglishTitle string `json:"english_title,omitempty"`
	// ImageURL is the tile's poster, empty when the tile has none.
	ImageURL string `json:"image_url,omitempty"`
	// AirsAt is the next episode's air time, when the tile carries one.
	AirsAt time.Time `json:"airs_at,omitempty"`
	// Episodes is the season length, 0 when the page does not say.
	//
	// Filled in by Enrich, not by the tile. It is here so the cache can serve
	// as the fast path when adding a show: without it, adding from the browse
	// list meant re-fetching the show's page for a number already on disk.
	Episodes int `json:"episodes,omitempty"`
	// Status is the airing status: Ongoing, Finished, Upcoming. Empty until
	// enriched.
	//
	// It is what lets the weekly refresh drop shows that have finished, rather
	// than carrying every show from the start of a cour to the end of it.
	Status string `json:"status,omitempty"`
	// Type is the media type: TV, Movie, TV Short, OVA, ONA. Empty until
	// enriched.
	//
	// Needed alongside Episodes because a Movie reports "Episodes: 1", which is
	// true of a film and wrong as a season length. The cache has to carry the
	// same distinction the show page does, or adding from browse would cap a
	// film at one episode.
	Type string `json:"type,omitempty"`
}

// Timetable is a cached snapshot of the seasonal schedule.
type Timetable struct {
	// Fetched is when this snapshot was taken.
	Fetched time.Time `json:"fetched"`
	// Enriched is when English titles were last fetched for these entries.
	//
	// Separate from Fetched because the two run on different schedules: the
	// tile list is cheap (one request) and refreshes daily, while enrichment
	// is one request per show and runs weekly. Conflating them would either
	// re-fetch 100+ pages daily or never refresh the list.
	Enriched time.Time `json:"enriched,omitempty"`
	// Entries is every show on the timetable, ordered by title.
	Entries []Entry `json:"entries"`
}

// BrowseTitle is which name the browse list displays.
type BrowseTitle string

const (
	// BrowseRomaji is the schedule's own display name, usually romaji.
	BrowseRomaji BrowseTitle = "romaji"
	// BrowseEnglish is the show's English name, when it has one.
	BrowseEnglish BrowseTitle = "english"
)

// BrowseTitles lists the choices the settings UI offers.
var BrowseTitles = []BrowseTitle{BrowseRomaji, BrowseEnglish}

// ParseBrowseTitle reads a browse title preference. Empty means romaji, which
// is what the timetable has always shown — an unset value must keep working
// rather than blank the list.
func ParseBrowseTitle(s string) (BrowseTitle, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "romaji":
		return BrowseRomaji, nil
	case "english", "en":
		return BrowseEnglish, nil
	}
	return "", fmt.Errorf("unknown browse title %q (want one of: romaji, english)", s)
}

// DisplayTitle is the name to show for an entry under a preference.
//
// Falls back to the romaji title when the English one is missing or identical:
// most shows have no separate English name, and showing an empty row to make a
// point about data completeness is worse than showing the name that exists.
func (e Entry) DisplayTitle(pref BrowseTitle) string {
	if pref == BrowseEnglish && e.EnglishTitle != "" {
		return e.EnglishTitle
	}
	return e.Title
}

// EnrichTTL is how long English titles are reused before being re-fetched.
//
// A week: enrichment is one request per show, so it is the expensive half of
// the cache, and English titles for a season's shows do not change once the
// season is under way.
const EnrichTTL = 7 * 24 * time.Hour

// NeedsEnrich reports whether English titles are missing or stale.
//
// "Missing" counts, not just the timestamp: a list that was refreshed recently
// but has entries with no English title is not in a usable state for someone
// reading it in English, and waiting out the TTL would leave it that way for
// days. The timestamp alone only says when the last attempt was, not whether it
// covered everything.
func (t *Timetable) NeedsEnrich() bool {
	if time.Since(t.Enriched) >= EnrichTTL {
		return true
	}
	for _, e := range t.Entries {
		if e.EnglishTitle == "" {
			return true
		}
	}
	return false
}

// CarryTitles copies English titles from prev onto any entry with the same
// slug that does not have one.
//
// This is what keeps a list refresh cheap. Most shows persist from one snapshot
// to the next, so their English titles are already known and re-fetching them
// would be ~100 requests to learn what is already on disk. Only genuinely new
// entries need a page fetch, which is usually none at all mid-season.
//
// Returns how many entries still need one.
func (t *Timetable) CarryTitles(prev *Timetable) int {
	if prev == nil {
		return len(t.Entries)
	}
	known := map[string]string{}
	for _, e := range prev.Entries {
		if e.EnglishTitle != "" {
			known[e.Slug] = e.EnglishTitle
		}
	}
	missing := 0
	for i := range t.Entries {
		if t.Entries[i].EnglishTitle != "" {
			continue
		}
		if n, ok := known[t.Entries[i].Slug]; ok {
			t.Entries[i].EnglishTitle = n
			continue
		}
		missing++
	}
	return missing
}

// Enrich fetches each entry's own page to fill in its English title.
//
// Only entries without one are fetched, so a refresh that carried titles
// forward costs nothing. Call CarryTitles first.
//
// Failures are per-entry and non-fatal: one unreachable page leaves that
// entry's EnglishTitle empty rather than failing the whole run, so a single
// dead page cannot cost the season its English names. The caller falls back to
// the romaji title for entries with no English one.
//
// client may be nil. Concurrency is bounded because the list is ~100 shows and
// an unbounded fan-out would look like a scraper to the site — the same
// politeness the indexer config applies to Nyaa.
func (t *Timetable) Enrich(client *http.Client) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	// An entry is pending when it is missing anything the show page supplies,
	// not just the English title. Carrying titles forward by slug means an
	// entry can have its name but no season length, and that is still not a
	// complete record.
	var pending []*Entry
	for i := range t.Entries {
		e := &t.Entries[i]
		if e.EnglishTitle == "" || e.Episodes == 0 || e.Status == "" {
			pending = append(pending, e)
		}
	}
	if len(pending) == 0 {
		t.Enriched = time.Now().UTC()
		return
	}

	const workers = 4
	slugs := make(chan *Entry)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range slugs {
				sh, err := FetchShow(client, e.Slug)
				if err != nil {
					continue
				}
				for _, n := range sh.Names["English"] {
					if n = strings.TrimSpace(n); n != "" {
						e.EnglishTitle = n
						break
					}
				}
				e.Episodes = sh.Episodes
				e.Status = sh.Status
				e.Type = sh.Type
			}
		}()
	}
	for _, e := range pending {
		slugs <- e
	}
	close(slugs)
	wg.Wait()
	t.Enriched = time.Now().UTC()
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

// Get returns the cached timetable. It never fetches.
//
// The cache is written by a background job on its own schedule, so serving the
// browse list is a disk read and nothing else. That is the same shape as the
// tracked shows: a scheduled job writes, the UI only reads.
//
// Doing the fetch here instead would put ~110 requests in the request path the
// first time the list went stale, and the modal would sit empty while they ran.
func (c *Cache) Get() (*Timetable, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load()
}

// Update refreshes the snapshot and makes sure it is in a usable state.
//
// This is the write half, called by the background job and by the UI's Refresh
// button. It is deliberately not called by Get: the two halves run on
// different schedules, and conflating them is what put network I/O in the
// request path.
//
// A refresh always leaves the list enriched, not merely fetched. Deferring
// enrichment to the next pass was cheaper in requests but wrong: the list sat
// without English titles for up to a week after every refresh, which is the
// one thing the feature exists to prevent.
//
// The cost is kept down by carrying titles forward by slug rather than
// re-fetching them — see CarryTitles. Mid-season a refresh usually adds no new
// shows, so it costs one request for the tiles and nothing more.
//
// A failed fetch leaves the existing snapshot untouched rather than clearing
// it — a stale list is more useful than an empty one, and the site being down
// must not cost the user their browse list.
func (c *Cache) Update(client *http.Client) (*Timetable, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	t, _ := c.load()
	listStale := t == nil || time.Since(t.Fetched) >= c.ttl

	if listStale {
		fresh, err := FetchTimetable(client)
		if err != nil {
			return t, err
		}
		// Carry the known titles onto the new entries before enriching, so
		// only genuinely new shows cost a request.
		fresh.CarryTitles(t)
		t = fresh
	}

	if t != nil && t.NeedsEnrich() {
		t.Enrich(client)
	}
	// Pruned after enrichment, since "finished" comes from the show page and
	// is not on the tile. A show that ended mid-cour otherwise lingers in the
	// browse list until the season rolls over.
	if t != nil {
		if dropped := t.DropFinished(); dropped > 0 {
			log.Printf("timetable: dropped %d finished show(s)", dropped)
		}
	}
	if t != nil {
		_ = c.save(t)
	}
	return t, nil
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

// Refresh forces a re-fetch and re-enrichment, ignoring both TTLs. Used by the
// UI's refresh button, for when the user wants the list now rather than at the
// next scheduled pass.
//
// Titles are still carried forward, so a manual refresh mid-season costs one
// request rather than ~100.
func (c *Cache) Refresh(client *http.Client) (*Timetable, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prev, _ := c.load()
	fresh, err := FetchTimetable(client)
	if err != nil {
		return nil, err
	}
	fresh.CarryTitles(prev)
	fresh.Enrich(client)
	_ = c.save(fresh)
	return fresh, nil
}

// load reads the snapshot from disk.
//
// A missing file is not an error: it means the background job has not run yet,
// which is a normal state for a container that has only just started. Only a
// file that exists but cannot be parsed is an error — that is corruption, and
// silently serving an empty list would hide it.
func (c *Cache) load() (*Timetable, error) {
	b, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
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

// Lookup finds one entry by slug. Returns nil when the cache does not have it.
//
// This is the fast path for adding a show: everything needed to create the
// record is already on disk, so adding from the browse list costs no network
// I/O and works when animeschedule is unreachable. A miss is not an error — it
// means the show is not on this season's list, and the caller falls back to
// fetching its page.
func (t *Timetable) Lookup(slug string) *Entry {
	for i := range t.Entries {
		if t.Entries[i].Slug == slug {
			return &t.Entries[i]
		}
	}
	return nil
}

// DropFinished removes entries whose season has ended.
//
// The season page lists a cour's shows, and a show that finished mid-cour stays
// on it until the season rolls over. Without this the browse list accumulates
// every show from the start of a cour, most of which can no longer be added to
// anything useful.
//
// Only an explicit "Finished" status is trusted. Absence from the page is not
// evidence of anything — a transient parse failure or a partial fetch would
// otherwise delete the whole list.
func (t *Timetable) DropFinished() int {
	kept := t.Entries[:0]
	dropped := 0
	for _, e := range t.Entries {
		if strings.EqualFold(e.Status, "Finished") {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	t.Entries = kept
	return dropped
}

// Search filters the timetable by a case-insensitive substring.
//
// Matches the romaji title, the English title and the slug, regardless of
// which one the list is displaying. The display preference is about what is on
// screen, not what is findable: switching to English must not hide a show the
// user could have found by its romaji name, and vice versa.
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
			strings.Contains(strings.ToLower(e.EnglishTitle), q) ||
			strings.Contains(strings.ToLower(e.Slug), q) {
			out = append(out, e)
		}
	}
	return out
}
