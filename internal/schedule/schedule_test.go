package schedule

import (
	"strings"
	"testing"
	"time"
)

// Each tile is wrapped in a link carrying the slug, exactly as the real page
// does. The slug is what the refresh looks a show up by.
const samplePage = `<html><body>
<a href="anime/bleach-sennen-kessen-hen-kashin-tan" class="show-link">
<h2 class="show-title-bar show-title-small">BLEACH: Sennen Kessen-hen - Kashin-tan</h2></a>
<h3 class="time-bar"><span class="show-episode">Ep 8</span>
<time datetime="2026-09-13T00:00&#43;10:00" class="show-air-time">12:00 AM</time></h3>
<a href="anime/clevatess-ii" class="show-link">
<h2 class="show-title-bar show-title-small">Clevatess II: Majuu no Ou to Itsuwari no Yuusha Denshou</h2></a>
<h3 class="time-bar"><span class="show-episode">Ep 10</span>
<time datetime="2026-09-09T22:00&#43;10:00" class="show-air-time">10:00 PM</time></h3>
</body></html>`

func TestParse(t *testing.T) {
	got, err := Parse(strings.NewReader(samplePage))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Title != "BLEACH: Sennen Kessen-hen - Kashin-tan" {
		t.Errorf("title = %q", got[0].Title)
	}
	if got[0].NextEp != 8 {
		t.Errorf("next ep = %d, want 8", got[0].NextEp)
	}
	if got[0].Slug != "bleach-sennen-kessen-hen-kashin-tan" {
		t.Errorf("slug = %q", got[0].Slug)
	}
	if got[1].Slug != "clevatess-ii" {
		t.Errorf("slug = %q", got[1].Slug)
	}
}

// TestParseAirsAtNoSeconds is the regression test for the bug that made every
// entry have a zero timestamp: animeschedule emits "01:00" with no seconds, and
// Go's time.RFC3339 rejects that outright.
func TestParseAirsAtNoSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-09-13T00:00&#43;10:00", "2026-09-13T00:00:00+10:00"},
		{"2026-09-09T22:00+10:00", "2026-09-09T22:00:00+10:00"},
		{"2026-09-09T22:00:00+10:00", "2026-09-09T22:00:00+10:00"},
	}
	for _, tc := range cases {
		got := parseAirsAt(tc.in)
		if got.IsZero() {
			t.Errorf("parseAirsAt(%q) = zero, want %s", tc.in, tc.want)
			continue
		}
		if got.Format(time.RFC3339) != tc.want {
			t.Errorf("parseAirsAt(%q) = %s, want %s", tc.in, got.Format(time.RFC3339), tc.want)
		}
	}
}

func TestParseAirsAtGarbage(t *testing.T) {
	if got := parseAirsAt(""); !got.IsZero() {
		t.Error("empty should be zero")
	}
	if got := parseAirsAt("not a date"); !got.IsZero() {
		t.Error("garbage should be zero")
	}
}

// TestParseProducesRealTimestamps guards the whole pipeline: after parsing,
// entries must carry a usable timestamp, not a zero one.
func TestParseProducesRealTimestamps(t *testing.T) {
	got, err := Parse(strings.NewReader(samplePage))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if e.AirsAt.IsZero() {
			t.Errorf("%q has zero AirsAt", e.Title)
		}
	}
}

// TestFindBySlugIsExact: lookup is by slug, so it cannot pick the wrong show
// and it does not care that the timetable's romaji title shares no words with
// the user's English name. This is why there is no fuzzy title matching: every
// show is added from an animeschedule URL, so every show has a slug.
func TestFindBySlugIsExact(t *testing.T) {
	entries, err := Parse(strings.NewReader(samplePage))
	if err != nil {
		t.Fatal(err)
	}
	var want *Entry
	for i := range entries {
		if entries[i].Slug != "" {
			want = &entries[i]
			break
		}
	}
	if want == nil {
		t.Fatal("sample page has no slugs to look up")
	}
	got := FindBySlug(entries, want.Slug)
	if got == nil {
		t.Fatalf("FindBySlug(%q) = nil", want.Slug)
	}
	if got.Title != want.Title {
		t.Errorf("FindBySlug(%q) = %q, want %q", want.Slug, got.Title, want.Title)
	}
}

// TestFindBySlugMissesCleanly: a show not on the timetable is simply absent.
// It must never fall back to guessing — a wrong match silently points a show at
// another show's air times, which is far worse than having none.
func TestFindBySlugMissesCleanly(t *testing.T) {
	entries, err := Parse(strings.NewReader(samplePage))
	if err != nil {
		t.Fatal(err)
	}
	if got := FindBySlug(entries, "some-show-that-is-not-airing"); got != nil {
		t.Errorf("unknown slug matched %q; lookup must be exact", got.Title)
	}
	if got := FindBySlug(entries, ""); got != nil {
		t.Errorf("empty slug matched %q", got.Title)
	}
}

func TestParseEmptyPageErrors(t *testing.T) {
	if _, err := Parse(strings.NewReader("<html></html>")); err == nil {
		t.Error("expected an error for a page with no entries")
	}
}
