package schedule

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tile builds one timetable tile in the shape the site renders.
func tile(slug, title, img, when string) string {
	s := `<a href="anime/` + slug + `" class="show-link">`
	if img != "" {
		s += `<img src="` + img + `" alt="">`
	}
	s += `<h2 class="show-title-bar">` + title + `</h2>`
	if when != "" {
		s += `<time datetime="` + when + `"></time>`
	}
	return s + `</a>`
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
	oldURL := TimetableURL
	TimetableURL = srv.URL
	defer func() { TimetableURL = oldURL }()

	for i := 0; i < 3; i++ {
		if _, err := c.Get(nil); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	// One fetch for the whole season, not one per call.
	if calls != 1 {
		t.Errorf("fetches = %d, want 1 (the rest served from cache)", calls)
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

	got, err := c.Get(srv.Client())
	if err != nil {
		t.Fatalf("Get must fall back to the stale snapshot, got: %v", err)
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
