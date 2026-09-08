package anilist

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// mockServer serves a canned AniList response. AniList is currently returning
// 403, so the importer cannot be tested against the live API — the mock is what
// keeps this covered.
func mockServer(t *testing.T, pages []string) *Client {
	t.Helper()
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if i >= len(pages) {
			// Unexpected extra page: fail loudly rather than loop forever.
			t.Errorf("unexpected request %d", i)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(pages[i]))
		i++
	}))
	t.Cleanup(srv.Close)

	c := NewClient()
	c.Endpoint = srv.URL
	return c
}

const onePage = `{"data":{"Page":{"pageInfo":{"hasNextPage":false,"currentPage":1},"mediaList":[
  {"status":"CURRENT","progress":3,"media":{"id":100,"episodes":12,"format":"TV",
   "title":{"romaji":"Boku no Anime","english":"My Anime","native":"僕のアニメ"},
   "synonyms":["BokuAnime"]}},
  {"status":"PLANNING","progress":0,"media":{"id":200,"episodes":24,"format":"TV",
   "title":{"romaji":"Plan Anime","english":"","native":""},"synonyms":[]}}
]}}}`

// The synonyms field is a list in the real API; a string would fail to unmarshal.
const onePageSynonyms = `{"data":{"Page":{"pageInfo":{"hasNextPage":false,"currentPage":1},"mediaList":[
  {"status":"CURRENT","progress":2,"media":{"id":300,"episodes":12,"format":"TV",
   "title":{"romaji":"Shizuku","english":"Droplet","native":"雫"},
   "synonyms":["Shizuku TV","Droplet Anime"]}}
]}}}`

func TestFetchList(t *testing.T) {
	c := mockServer(t, []string{onePage})
	got, err := c.FetchList("someone", []string{"CURRENT", "PLANNING"})
	if err != nil {
		t.Fatalf("FetchList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].MediaID != 100 || got[0].Title != "My Anime" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[0].Progress != 3 {
		t.Errorf("progress = %d, want 3", got[0].Progress)
	}
	// English empty -> fall back to romaji.
	if got[1].Title != "Plan Anime" {
		t.Errorf("entry 1 title = %q, want romaji fallback", got[1].Title)
	}
}

func TestFetchListPaginates(t *testing.T) {
	page1 := `{"data":{"Page":{"pageInfo":{"hasNextPage":true,"currentPage":1},"mediaList":[
		{"status":"CURRENT","progress":1,"media":{"id":1,"episodes":12,"format":"TV","title":{"romaji":"A","english":"","native":""},"synonyms":[]}}]}}}`
	page2 := `{"data":{"Page":{"pageInfo":{"hasNextPage":false,"currentPage":2},"mediaList":[
		{"status":"CURRENT","progress":1,"media":{"id":2,"episodes":12,"format":"TV","title":{"romaji":"B","english":"","native":""},"synonyms":[]}}]}}}`

	c := mockServer(t, []string{page1, page2})
	got, err := c.FetchList("someone", nil)
	if err != nil {
		t.Fatalf("FetchList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries across 2 pages, want 2", len(got))
	}
}

func TestFetchListSurfacesAPIError(t *testing.T) {
	errBody := `{"errors":[{"message":"The AniList API has been temporarily disabled due to severe stability issues.","status":403}]}`
	c := mockServer(t, []string{errBody})
	_, err := c.FetchList("someone", nil)
	if err == nil {
		t.Fatal("expected an error for a 403 envelope")
	}
	if !contains(err.Error(), "403") {
		t.Errorf("error should mention the status: %v", err)
	}
}

func TestImportCreatesShowsAndAliases(t *testing.T) {
	st := testStore(t)
	c := mockServer(t, []string{onePageSynonyms})

	entries, err := c.FetchList("someone", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewImporter(st).Import(entries)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Created != 1 {
		t.Errorf("created = %d, want 1", res.Created)
	}

	sh, err := st.GetShowByAniListID(300)
	if err != nil {
		t.Fatal(err)
	}
	if sh == nil {
		t.Fatal("show not found by anilist id")
	}
	if sh.CanonicalName != "Droplet" {
		t.Errorf("canonical = %q, want english title", sh.CanonicalName)
	}
	if sh.Source != "anilist" {
		t.Errorf("source = %q, want anilist", sh.Source)
	}
	if sh.MaxEpisode != 12 {
		t.Errorf("max_episode = %d, want 12", sh.MaxEpisode)
	}

	// Every title variant must be an alias: release groups use any of them.
	want := map[string]bool{"Droplet": true, "Shizuku": true, "雫": true, "Shizuku TV": true, "Droplet Anime": true}
	for _, a := range sh.Aliases {
		if !want[a] {
			t.Errorf("unexpected alias %q", a)
		}
	}
	if len(sh.Aliases) != len(want) {
		t.Errorf("aliases = %v, want %v", sh.Aliases, want)
	}
}

// TestImportMarksWatchedHistoryTerminal is the most valuable thing the import
// does: without it, a fresh database would re-download episodes the user
// finished months ago.
func TestImportMarksWatchedHistoryTerminal(t *testing.T) {
	st := testStore(t)
	c := mockServer(t, []string{onePage})

	entries, err := c.FetchList("someone", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewImporter(st).Import(entries)
	if err != nil {
		t.Fatal(err)
	}
	if res.Episodes != 3 {
		t.Errorf("episodes written = %d, want 3 (progress of first entry)", res.Episodes)
	}

	sh, _ := st.GetShowByAniListID(100)
	for i := 1; i <= 3; i++ {
		e, err := st.GetEpisode(sh.ID, i)
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			t.Fatalf("episode %d not written", i)
		}
		if e.State != episode.Watched {
			t.Errorf("episode %d state = %s, want watched", i, e.State)
		}
		if !e.State.Terminal() {
			t.Errorf("episode %d should be terminal so it is never re-grabbed", i)
		}
	}
	// Episode 4 is beyond progress: must NOT be marked, or we would skip
	// something the user has not seen.
	if e, _ := st.GetEpisode(sh.ID, 4); e != nil {
		t.Errorf("episode 4 should not exist, got state %s", e.State)
	}
}

// TestImportIsIdempotent: AniList is flaky, so a partial import must be
// resumable without creating duplicates.
func TestImportIsIdempotent(t *testing.T) {
	st := testStore(t)
	c := mockServer(t, []string{onePageSynonyms, onePageSynonyms})

	for run := 0; run < 2; run++ {
		entries, err := c.FetchList("someone", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewImporter(st).Import(entries); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}

	shows, err := st.ListShows()
	if err != nil {
		t.Fatal(err)
	}
	if len(shows) != 1 {
		t.Fatalf("got %d shows after two imports, want 1", len(shows))
	}
}

func TestTracked(t *testing.T) {
	cases := map[string]bool{
		"CURRENT":   true,
		"PLANNING":  true,
		"REPEATING": true,
		"COMPLETED": false,
		"DROPPED":   false,
		"PAUSED":    false,
	}
	for status, want := range cases {
		if got := Tracked(status); got != want {
			t.Errorf("Tracked(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestAliasesForDeduplicates(t *testing.T) {
	e := Entry{Title: "A", Romaji: "A", Native: "", Synonyms: []string{"B", "B", ""}}
	got := aliasesFor(e)
	if len(got) != 2 {
		t.Errorf("aliases = %v, want [A B]", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

var _ = json.Marshal
