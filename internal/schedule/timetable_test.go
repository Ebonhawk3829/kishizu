package schedule

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tile builds one season-page tile in the shape the site renders: a div
// carrying a route attribute (the slug) and an anime-tile-title heading.
func tile(slug, title, img, when string) string {
	s := `<div route="` + slug + `" showID="X">`
	s += `<a href="/anime/` + slug + `">`
	s += `<h2 class="anime-tile-title" itemprop="name">` + title + `</h2></a>`
	if img != "" {
		s += `<img src="` + img + `" alt="">`
	}
	if when != "" {
		s += `<time datetime="` + when + `"></time>`
	}
	return s + `</div>`
}

var sampleTimetable = `<html><body><div class="timetable">
` + tile("re-zero-kara-hajimeru-isekai-seikatsu-4", "Re:ZERO - Starting Life in Another World 4", "https://img.animeschedule.net/x/anime/jpg/rezero.jpg", "2026-10-02T23:00&#43;09:00") + `
` + tile("bleach-sennen-kessen-hen", "BLEACH: Sennen Kessen-hen", "https://img.animeschedule.net/x/anime/jpg/bleach.jpg", "2026-10-04T17:30&#43;09:00") + `
` + tile("some-show-with-no-art", "Some Show With No Art", "", "") + `
</div></body></html>`

// TestParseTimetablePairsSlugWithTitle: the slug is the identity and the title
// is what the user picks by. Pairing them wrongly would add the wrong show.
func TestParseTimetablePairsSlugWithTitle(t *testing.T) {
	tt, err := ParseTimetable(strings.NewReader(sampleTimetable))
	if err != nil {
		t.Fatalf("ParseTimetable: %v", err)
	}
	if len(tt.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(tt.Entries))
	}

	bySlug := map[string]Entry{}
	for _, e := range tt.Entries {
		bySlug[e.Slug] = e
	}
	rezero := bySlug["re-zero-kara-hajimeru-isekai-seikatsu-4"]
	if rezero.Title != "Re:ZERO - Starting Life in Another World 4" {
		t.Errorf("rezero title = %q", rezero.Title)
	}
	if !strings.Contains(rezero.ImageURL, "rezero.jpg") {
		t.Errorf("rezero image = %q", rezero.ImageURL)
	}
	// The offset is HTML-escaped on the page; unescaping it is what makes
	// the timestamp parse at all.
	if rezero.AirsAt.IsZero() {
		t.Error("rezero airs_at is zero; the escaped offset was not handled")
	}
	bleach := bySlug["bleach-sennen-kessen-hen"]
	if bleach.Title != "BLEACH: Sennen Kessen-hen" {
		t.Errorf("bleach title = %q", bleach.Title)
	}
}

// TestParseTimetableToleratesIncompleteTiles: a tile with no image or air time
// must still appear. The slug alone is enough to add the show and fetch its
// full record, so dropping it would make the browse list silently wrong.
func TestParseTimetableToleratesIncompleteTiles(t *testing.T) {
	tt, err := ParseTimetable(strings.NewReader(sampleTimetable))
	if err != nil {
		t.Fatalf("ParseTimetable: %v", err)
	}
	var found bool
	for _, e := range tt.Entries {
		if e.Slug == "some-show-with-no-art" {
			found = true
			if e.ImageURL != "" {
				t.Errorf("image = %q, want empty", e.ImageURL)
			}
			if !e.AirsAt.IsZero() {
				t.Errorf("airs_at = %v, want zero", e.AirsAt)
			}
		}
	}
	if !found {
		t.Error("the tile with no art or air time is missing entirely")
	}
}

// TestParseTimetableDeduplicates: the page renders each tile more than once
// (mobile and desktop layouts), so the same slug appears several times.
func TestParseTimetableDeduplicates(t *testing.T) {
	page := strings.Replace(sampleTimetable, "</div></body></html>",
		tile("re-zero-kara-hajimeru-isekai-seikatsu-4", "Re:ZERO (duplicate)", "", "")+
			"</div></body></html>", 1)
	tt, err := ParseTimetable(strings.NewReader(page))
	if err != nil {
		t.Fatalf("ParseTimetable: %v", err)
	}
	n := 0
	for _, e := range tt.Entries {
		if e.Slug == "re-zero-kara-hajimeru-isekai-seikatsu-4" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("slug appears %d times, want 1", n)
	}
}

// TestParseTimetableRejectsEmptyPage: a page with no tiles is a scrape that
// broke, not an empty season. Returning an empty list would look like "no
// shows are airing", which is a lie.
func TestParseTimetableRejectsEmptyPage(t *testing.T) {
	if _, err := ParseTimetable(strings.NewReader("<html><body>nothing</body></html>")); err == nil {
		t.Error("expected an error for a page with no tiles")
	}
}

// TestTimetableSearch: filtering lives here so the same search works from the
// UI and the command line, and an empty query means everything in both.
func TestTimetableSearch(t *testing.T) {
	tt, err := ParseTimetable(strings.NewReader(sampleTimetable))
	if err != nil {
		t.Fatalf("ParseTimetable: %v", err)
	}
	if got := tt.Search(""); len(got) != 3 {
		t.Errorf("empty query = %d entries, want all 3", len(got))
	}
	if got := tt.Search("bleach"); len(got) != 1 || got[0].Slug != "bleach-sennen-kessen-hen" {
		t.Errorf("search 'bleach' = %+v", got)
	}
	// Case-insensitive, and matches the slug as well as the title.
	if got := tt.Search("RE:ZERO"); len(got) != 1 {
		t.Errorf("search 'RE:ZERO' = %d entries, want 1", len(got))
	}
	if got := tt.Search("isekai"); len(got) != 1 {
		t.Errorf("search by slug substring = %d entries, want 1", len(got))
	}
	if got := tt.Search("nothing-matches-this"); len(got) != 0 {
		t.Errorf("search with no match = %d entries, want 0", len(got))
	}
}

// TestGetNeverFetches: serving the browse list must be a disk read and nothing
// else. The snapshot is written by a background job on its own schedule, so a
// request that reaches the network would put ~110 requests in the request path
// and leave the modal empty while they ran.
func TestGetNeverFetches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(sampleTimetable))
	}))
	defer srv.Close()
	defer PinTimetableURL(srv.URL)()

	dir := t.TempDir()
	c, err := NewCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing cached yet, and Get must not go and get it: an empty list is the
	// honest answer until the background job has run.
	tt, err := c.Get()
	if err != nil {
		t.Fatalf("Get on an empty cache: %v", err)
	}
	if tt != nil {
		t.Errorf("Get on an empty cache = %d entries, want nil", len(tt.Entries))
	}
	if calls != 0 {
		t.Errorf("fetches = %d, want 0 (Get must never fetch)", calls)
	}
}

// TestUpdateLeavesListEnriched: a refresh must leave the list in a usable
// state, not merely fetched.
//
// This is the bug the deferral caused: after a list refresh the entries had no
// English titles, and because Enriched was recent, NeedsEnrich() said no — so
// the list stayed unenriched for up to a week. Someone reading it in English
// saw romaji for days after every refresh, which is the one thing the feature
// exists to prevent.
func TestUpdateLeavesListEnriched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleTimetable))
	}))
	defer srv.Close()
	defer PinTimetableURL(srv.URL)()

	dir := t.TempDir()
	c, err := NewCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	tt, err := c.Update(nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(tt.Entries) == 0 {
		t.Fatal("no entries")
	}
	// Enrichment must have been attempted in this pass, not deferred to a
	// later one. Whether it succeeded depends on the upstream — this stub
	// serves the timetable page for show pages too, so no English names come
	// back — but the attempt is what the deferral bug removed.
	if tt.Enriched.IsZero() {
		t.Error("Enriched is zero: enrichment was deferred, want it attempted in this pass")
	}
	// And it must not be skipped on the next pass either: a list with gaps
	// still needs enriching however recently it was tried.
	if !tt.NeedsEnrich() {
		t.Error("NeedsEnrich = false with entries still missing titles, want true")
	}
}

// TestCarryTitlesReusesKnownNames: a refresh must not re-fetch titles it
// already knows. Most shows persist between snapshots, so carrying them
// forward by slug is what keeps a mid-season refresh to one request.
func TestCarryTitlesReusesKnownNames(t *testing.T) {
	prev := &Timetable{Entries: []Entry{
		{Slug: "a", Title: "A Romaji", EnglishTitle: "A English"},
		{Slug: "b", Title: "B Romaji", EnglishTitle: "B English"},
	}}
	next := &Timetable{Entries: []Entry{
		{Slug: "a", Title: "A Romaji"},
		{Slug: "b", Title: "B Romaji"},
		{Slug: "c", Title: "C Romaji"}, // new this season
	}}

	missing := next.CarryTitles(prev)
	if missing != 1 {
		t.Errorf("missing = %d, want 1 (only the new show needs a fetch)", missing)
	}
	if next.Entries[0].EnglishTitle != "A English" {
		t.Errorf("a = %q, want the carried English title", next.Entries[0].EnglishTitle)
	}
	if next.Entries[2].EnglishTitle != "" {
		t.Errorf("c = %q, want empty (it is new)", next.Entries[2].EnglishTitle)
	}
	// A list with one gap still needs enriching, even though the rest are done.
	if !next.NeedsEnrich() {
		t.Error("NeedsEnrich = false with an entry still missing a title, want true")
	}
}

// TestFetchShowDistinguishesNotFound: a 404 is evidence the page is gone; a
// 500 or a timeout is the absence of evidence. Callers use the difference to
// decide whether a failed lookup is a finding or a retry, so conflating them
// would let a transient outage be reported as a show having finished.
func TestFetchShowDistinguishesNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	defer PinShowURL(srv.URL)()

	_, err := FetchShow(srv.Client(), "gone-show")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: err = %v, want ErrNotFound", err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()
	defer PinShowURL(down.URL)()

	_, err = FetchShow(down.Client(), "some-show")
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("500: err = %v, must NOT be ErrNotFound (the site is up)", err)
	}
}

// TestDropFinishedOnlyTrustsExplicitStatus: absence from the page is not
// evidence that a show finished. Only an explicit "Finished" status is, so a
// partial fetch or a parse failure cannot delete the whole browse list.
func TestDropFinishedOnlyTrustsExplicitStatus(t *testing.T) {
	tt := &Timetable{Entries: []Entry{
		{Slug: "a", Status: "Ongoing"},
		{Slug: "b", Status: "Finished"},
		{Slug: "c", Status: ""}, // not enriched yet
		{Slug: "d", Status: "Upcoming"},
	}}
	dropped := tt.DropFinished()
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	slugs := []string{}
	for _, e := range tt.Entries {
		slugs = append(slugs, e.Slug)
	}
	// c has no status, and an unknown status must keep the entry: dropping it
	// would mean a failed enrichment silently emptied the list.
	if len(slugs) != 3 || slugs[0] != "a" || slugs[1] != "c" || slugs[2] != "d" {
		t.Errorf("kept %v, want [a c d]", slugs)
	}
}

// TestLookupFindsBySlug: the cache is the fast path for adding a show, so a
// hit must return the entry and a miss must be nil rather than an error — a
// miss just means the show is not on this season's list.
func TestLookupFindsBySlug(t *testing.T) {
	tt := &Timetable{Entries: []Entry{
		{Slug: "a", Title: "A", Episodes: 12, Status: "Ongoing"},
	}}
	if e := tt.Lookup("a"); e == nil || e.Episodes != 12 {
		t.Errorf("Lookup(a) = %+v, want the entry with 12 episodes", e)
	}
	if e := tt.Lookup("nope"); e != nil {
		t.Errorf("Lookup(nope) = %+v, want nil", e)
	}
}

// TestCacheReusesFreshSnapshot: the point of the cache is one request for the
// whole season instead of one per show.
func TestCacheReusesFreshSnapshot(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(sampleTimetable))
	}))
	defer srv.Close()

	dir := t.TempDir()
	c, err := NewCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer PinTimetableURL(srv.URL)()

	// Update is the write half: it fetches once, then no more while fresh.
	for i := 0; i < 3; i++ {
		if _, err := c.Update(nil); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	// One fetch for the whole season, not one per call.
	if calls != 1 {
		t.Errorf("fetches = %d, want 1 (the rest served from cache)", calls)
	}

	// Get is the read half: it must not fetch at all, so serving the browse
	// list costs no network I/O.
	for i := 0; i < 3; i++ {
		if _, err := c.Get(); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("fetches = %d after 3 Gets, want still 1 (Get never fetches)", calls)
	}
}

// TestCachePersistsToDisk: the snapshot survives a restart, which is what
// keeps browsing working when the site is unreachable.
func TestCachePersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := &Timetable{
		Fetched: time.Now().UTC().Truncate(time.Second),
		Entries: []Entry{{Slug: "a-show", Title: "A Show"}},
	}
	if err := c.save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := c.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Slug != "a-show" {
		t.Errorf("loaded %+v, want the saved entry", got.Entries)
	}
}

// TestCacheSaveIsAtomic: a crash mid-write must not leave a cache that fails
// to parse on next start. The temp-file-then-rename is what guarantees that.
func TestCacheSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.save(&Timetable{Fetched: time.Now(), Entries: []Entry{{Slug: "x"}}}); err != nil {
		t.Fatal(err)
	}
	// No leftover temp file.
	if _, err := os.Stat(c.path + ".tmp"); !os.IsNotExist(err) {
		t.Error("a .tmp file was left behind")
	}
	// The real path is valid JSON.
	b, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "x") {
		t.Errorf("cache file = %q", string(b))
	}
}

// TestCacheFallsBackToStale: a refresh failure must return the stale snapshot
// rather than an error. A list from yesterday is far more useful than an empty
// one, and the site being down must not break browsing.
func TestCacheFallsBackToStale(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, time.Nanosecond) // always stale
	if err != nil {
		t.Fatal(err)
	}
	stale := &Timetable{
		Fetched: time.Now().Add(-48 * time.Hour),
		Entries: []Entry{{Slug: "old", Title: "Old"}},
	}
	if err := c.save(stale); err != nil {
		t.Fatal(err)
	}

	// Point the fetch at a server that fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer PinTimetableURL(srv.URL)()

	// A failed refresh must leave the existing snapshot in place rather than
	// clearing it: a stale list is more useful than an empty one.
	if _, err := c.Update(srv.Client()); err == nil {
		t.Fatal("expected an error when the fetch fails")
	}
	got, err := c.Get()
	if err != nil {
		t.Fatalf("Get must still serve the stale snapshot, got: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Slug != "old" {
		t.Errorf("got %+v, want the stale entry", got.Entries)
	}
}

// TestNewCacheRejectsEmptyDir: an empty dir would write the cache next to the
// binary, which is not where data belongs.
func TestNewCacheRejectsEmptyDir(t *testing.T) {
	if _, err := NewCache("", time.Hour); err == nil {
		t.Error("expected an error for an empty dir")
	}
}

// TestNewCacheCreatesDir: the cache dir lives next to the database, and may
// not exist on a fresh install.
func TestNewCacheCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "art")
	if _, err := NewCache(dir, time.Hour); err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir was not created: %v", err)
	}
}
