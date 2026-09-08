package schedule

import (
	"strings"
	"testing"
	"time"
)

const samplePage = `<html><body>
<h2 class="show-title-bar show-title-small">BLEACH: Sennen Kessen-hen - Kashin-tan</h2>
<h3 class="time-bar"><span class="show-episode">Ep 8</span>
<time datetime="2026-09-13T00:00&#43;10:00" class="show-air-time">12:00 AM</time></h3>
<h2 class="show-title-bar show-title-small">Clevatess II: Majuu no Ou to Itsuwari no Yuusha Denshou</h2>
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

// TestFindPrefersRomajiAlias: the schedule uses romaji while the user's names
// are English, so matching must work off aliases. Jaccard similarity fails here
// because the two share almost no words; recall on the alias succeeds.
func TestFindPrefersRomajiAlias(t *testing.T) {
	entries, err := Parse(strings.NewReader(samplePage))
	if err != nil {
		t.Fatal(err)
	}

	// The English name alone does not match.
	if got := Find(entries, "BLEACH: Thousand-Year Blood War - The Calamity"); got != nil {
		t.Errorf("english name should not match, got %q", got.Title)
	}
	// With the romaji alias it does.
	got := FindWithAliases(entries, []string{
		"BLEACH: Thousand-Year Blood War - The Calamity",
		"Bleach: Sennen Kessen Hen - Kashin Tan",
	})
	if got == nil {
		t.Fatal("romaji alias should match")
	}
	if !strings.Contains(got.Title, "BLEACH") {
		t.Errorf("matched %q, want BLEACH", got.Title)
	}
}

func TestFindRejectsUnrelated(t *testing.T) {
	entries, err := Parse(strings.NewReader(samplePage))
	if err != nil {
		t.Fatal(err)
	}
	if got := FindWithAliases(entries, []string{"Some Completely Different Show"}); got != nil {
		t.Errorf("should not match, got %q", got.Title)
	}
}

func TestParseEmptyPageErrors(t *testing.T) {
	if _, err := Parse(strings.NewReader("<html></html>")); err == nil {
		t.Error("expected an error for a page with no entries")
	}
}
