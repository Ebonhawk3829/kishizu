package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/schedule"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// These cover the browse-title preference, which used to live in the config
// file. The bug that motivated moving it: the UI toggled the preference, the
// server re-read it from the file, the file still held the old value, so the
// server re-asserted it and reset the control. The preference is now in the
// database, and the file is only a default for a value never set.

// seededServer builds a server with a timetable cache holding one entry that
// has both a romaji and an English name, so the display choice is observable.
func seededServer(t *testing.T) (*Server, *store.Store, string) {
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
	c, err := schedule.NewCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Seed(&schedule.Timetable{
		Fetched: time.Now(),
		Entries: []schedule.Entry{
			{Slug: "a-show", Title: "A Romaji", EnglishTitle: "A English"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	srv.SetTimetable(c)

	path := filepath.Join(t.TempDir(), "kishizu.yaml")
	srv.SetConfigPath(path)
	return srv, st, path
}

// TestBrowseTitleToggleSticks: the regression. The toggle writes the
// preference, and the next list request must honour it rather than
// re-asserting the file's value.
func TestBrowseTitleToggleSticks(t *testing.T) {
	srv, _, path := seededServer(t)
	// The file says romaji, and is never rewritten by the toggle.
	if err := os.WriteFile(path, []byte("server:\n  browse:\n    title: romaji\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Switch to English through the toggle endpoint.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/config/browse-title",
		strings.NewReader(`{"title":"english"}`)))
	if rec.Code != 200 {
		t.Fatalf("toggle status = %d: %s", rec.Code, rec.Body.String())
	}

	// The list must now display the English name.
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable", nil))
	if rec.Code != 200 {
		t.Fatalf("timetable status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Title   string `json:"title"`
		Entries []struct {
			Title string `json:"title"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "english" {
		t.Errorf("title = %q, want english: the preference did not stick", got.Title)
	}
	if len(got.Entries) != 1 || got.Entries[0].Title != "A English" {
		t.Errorf("entries = %+v, want the English name displayed", got.Entries)
	}

	// The config file must be untouched: it is deployment config, not
	// runtime state.
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "english") {
		t.Errorf("the toggle rewrote the config file:\n%s", onDisk)
	}
}

// TestBrowseTitleFallsBackToTheConfiguredDefault: an existing deployment's
// configured choice must keep working. The file is the default for a
// preference the user has never set, not the value.
func TestBrowseTitleFallsBackToTheConfiguredDefault(t *testing.T) {
	srv, _, path := seededServer(t)
	if err := os.WriteFile(path, []byte("server:\n  browse:\n    title: english\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "english" {
		t.Errorf("title = %q, want english from the configured default", got.Title)
	}
}

// TestBrowseTitlePreferenceBeatsTheFile: once the user has chosen, their
// choice wins over the file. Otherwise a deployment that configures english
// could never be switched to romaji from the UI.
func TestBrowseTitlePreferenceBeatsTheFile(t *testing.T) {
	srv, _, path := seededServer(t)
	if err := os.WriteFile(path, []byte("server:\n  browse:\n    title: english\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := srv.st.SetPreference(prefKeyBrowseTitle, "romaji"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "romaji" {
		t.Errorf("title = %q, want romaji: the file overrode the user's choice", got.Title)
	}
}

// TestBrowseTitleRejectsUnknownValues: a bad value must be refused rather
// than stored, or the list would render a preference nothing can parse.
func TestBrowseTitleRejectsUnknownValues(t *testing.T) {
	srv, _, _ := seededServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/config/browse-title",
		strings.NewReader(`{"title":"klingon"}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 for an unknown title preference", rec.Code)
	}
}

// TestBrowseTitleDefaultsToRomaji: with nothing configured and nothing set,
// the list must still render. A blank preference is the normal state of a
// fresh install, not an error.
func TestBrowseTitleDefaultsToRomaji(t *testing.T) {
	srv, _, _ := seededServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/timetable", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "romaji" {
		t.Errorf("title = %q, want romaji", got.Title)
	}
}

// TestPreferenceRoundTrips: the store is the durable record, so a value
// written must read back — and an unset key must report not-found rather
// than an error, since that is the normal state of a fresh install.
func TestPreferenceRoundTrips(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, found, err := st.Preference("nope"); err != nil {
		t.Fatalf("Preference on an unset key: %v", err)
	} else if found {
		t.Error("an unset preference must report not-found, not an empty value")
	}

	if err := st.SetPreference("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if got, found, err := st.Preference("k"); err != nil || !found || got != "v1" {
		t.Errorf("Preference = %q,%v,%v want v1,true,nil", got, found, err)
	}

	// Overwriting must replace, not append: a second row for the same key
	// would make the read ambiguous.
	if err := st.SetPreference("k", "v2"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.Preference("k"); got != "v2" {
		t.Errorf("Preference = %q, want v2 after overwrite", got)
	}
}
