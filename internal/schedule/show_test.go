package schedule

import (
	"strings"
	"testing"
)

// showPage is a trimmed copy of a real animeschedule.net show page. Only the
// parts ParseShow reads are kept, in the order they appear in the document.
//
// The alternative-names block is deliberately included twice: the site renders
// it once for mobile and once for desktop, and the parser must not double up.
const showPage = `<html><head>
<title>Re:Zero kara Hajimeru Isekai Seikatsu 4 (Re:Zero - Starting Life in Another World 4) | AnimeSchedule</title>
</head><body>
<section id="information-section-small" class="section-content">
    <div class="information-content-wrapper"><h3>Type</h3>
        <a href="/media-types/tv" class="information-link">TV</a></div>
    <div class="information-content-wrapper"><h3>Season</h3>
        <a href="/seasons/spring-2026" class="information-link">Spring 2026</a></div>
    <div class="information-content-wrapper"><h3>Status</h3><div>Ongoing</div></div>
    <div class="information-content-wrapper"><h3>Episodes</h3>
        <div itemprop="numberOfEpisodes">19</div></div>
    <div class="information-content-wrapper"><h3>Release Date</h3>
        <time id="start-date-mobile" itemprop="startDate" datetime="2026-04-08">Apr 08, 2026</time></div>
</section>
<section id="alternative-names-section-small" class="section-content">
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">Romaji</span>
        <div class="alternative-name">Re:Zero kara Hajimeru Isekai Seikatsu 4</div>
    </div>
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">English</span>
        <div class="alternative-name" itemprop="alternateName">Re:Zero - Starting Life in Another World 4</div>
    </div>
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">Japanese</span>
        <div class="alternative-name" itemprop="alternateName">Re:ゼロから始める異世界生活 4</div>
    </div>
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">Abbreviation</span>
        <div class="alternative-name" itemprop="alternateName">ReZero 4</div>
    </div>
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">Synonyms</span>
        <div class="alternative-name" itemprop="alternateName">Re:ZERO -Starting Life in Another World- Season 4</div>
        <div class="alternative-name" itemprop="alternateName">Re:Zero kara Hajimeru Isekai Seikatsu 4th Season</div>
    </div>
</section>
<section id="alternative-names-section-large" class="section-content">
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">Romaji</span>
        <div class="alternative-name">Re:Zero kara Hajimeru Isekai Seikatsu 4</div>
    </div>
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">English</span>
        <div class="alternative-name" itemprop="alternateName">Re:Zero - Starting Life in Another World 4</div>
    </div>
</section>
<div class="links-wrapper">
    <a href="//myanimelist.net/anime/61316/Re_Zero" title="MyAnimeList">MAL</a>
    <a href="//anilist.co/anime/189046/ReZero" title="AniList">AniList</a>
</div>
</body></html>`

func TestParseShow(t *testing.T) {
	sh, err := ParseShow(strings.NewReader(showPage), "re-zero-kara-hajimeru-isekai-seikatsu-4")
	if err != nil {
		t.Fatalf("ParseShow: %v", err)
	}
	if sh.Slug != "re-zero-kara-hajimeru-isekai-seikatsu-4" {
		t.Errorf("Slug = %q", sh.Slug)
	}
	if sh.Episodes != 19 {
		t.Errorf("Episodes = %d, want 19", sh.Episodes)
	}
	if sh.Status != "Ongoing" {
		t.Errorf("Status = %q, want Ongoing", sh.Status)
	}
	if sh.Season != "Spring 2026" {
		t.Errorf("Season = %q, want Spring 2026", sh.Season)
	}
	if sh.AniListID != 189046 {
		t.Errorf("AniListID = %d, want 189046", sh.AniListID)
	}
	if sh.MyAnimeListID != 61316 {
		t.Errorf("MyAnimeListID = %d, want 61316", sh.MyAnimeListID)
	}
	// Release Date is the season's start. For an unaired show it is the only
	// air information that exists, since the timetable covers ~1 week.
	if got := sh.ReleaseDate.Format("2006-01-02"); got != "2026-04-08" {
		t.Errorf("ReleaseDate = %s, want 2026-04-08", got)
	}
	// The title carries the parenthesised English gloss; it is the display
	// name, not an alias, so it is kept whole.
	if !strings.Contains(sh.Title, "Re:Zero kara Hajimeru Isekai Seikatsu 4") {
		t.Errorf("Title = %q", sh.Title)
	}
}

// TestParseShowAliases: every name kind is captured, and the duplicated
// mobile/desktop block does not produce duplicates.
func TestParseShowAliases(t *testing.T) {
	sh, err := ParseShow(strings.NewReader(showPage), "slug")
	if err != nil {
		t.Fatalf("ParseShow: %v", err)
	}
	// Japanese is excluded: Nyaa titles are romanised, so it never appears in
	// a release name and only adds noise to the alias set.
	want := []string{
		"Re:Zero kara Hajimeru Isekai Seikatsu 4",
		"Re:Zero - Starting Life in Another World 4",
		"Re:ZERO -Starting Life in Another World- Season 4",
		"Re:Zero kara Hajimeru Isekai Seikatsu 4th Season",
	}
	got := sh.Aliases()
	if len(got) != len(want) {
		t.Fatalf("Aliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Aliases[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestJapaneseAndAbbreviationExcludedFromAliases: neither kind may reach the
// matcher.
//
// Japanese names never appear in Nyaa release titles, which are romanised, so
// they are dead weight — and a short Japanese abbreviation is actively
// dangerous: it scored above the alias threshold against an unrelated show on
// token overlap alone, which silently pointed that show at another show's air
// times. Abbreviations are too short to carry identity for the same reason.
func TestJapaneseAndAbbreviationExcludedFromAliases(t *testing.T) {
	sh, err := ParseShow(strings.NewReader(showPage), "slug")
	if err != nil {
		t.Fatalf("ParseShow: %v", err)
	}
	for _, a := range sh.Aliases() {
		if a == "ReZero 4" {
			t.Error("abbreviation leaked into Aliases()")
		}
		if a == "Re:ゼロから始める異世界生活 4" {
			t.Error("japanese name leaked into Aliases()")
		}
	}
	// The romaji, English and synonym names must still be there.
	want := map[string]bool{
		"Re:Zero kara Hajimeru Isekai Seikatsu 4":           false,
		"Re:Zero - Starting Life in Another World 4":        false,
		"Re:ZERO -Starting Life in Another World- Season 4": false,
		"Re:Zero kara Hajimeru Isekai Seikatsu 4th Season":  false,
	}
	for _, a := range sh.Aliases() {
		if _, ok := want[a]; ok {
			want[a] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("alias %q missing from Aliases()", name)
		}
	}
}

// TestJapaneseFilteredFromAnyField: the site files Japanese names under
// "Japanese" but also slips them into "Synonyms", so filtering by field label
// lets them through. Filtering must be by script.
func TestJapaneseFilteredFromAnyField(t *testing.T) {
	page := `<html><head><title>Show | AnimeSchedule</title></head><body>
<section id="alternative-names-section-small" class="section-content">
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">Romaji</span>
        <div class="alternative-name">Shangri-La Frontier 3rd Season</div>
    </div>
    <div class="alternative-name-wrapper">
        <span class="alternative-name-heading">Synonyms</span>
        <div class="alternative-name" itemprop="alternateName">神ゲーに挑まんとす〜 3rd season</div>
        <div class="alternative-name" itemprop="alternateName">Shangri-La Frontier Season 3</div>
    </div>
</section></body></html>`
	sh, err := ParseShow(strings.NewReader(page), "slug")
	if err != nil {
		t.Fatalf("ParseShow: %v", err)
	}
	for _, a := range sh.Aliases() {
		if hasJapanese(a) {
			t.Errorf("japanese alias %q survived the filter", a)
		}
	}
	// The romanised synonym in the same block must survive.
	found := false
	for _, a := range sh.Aliases() {
		if a == "Shangri-La Frontier Season 3" {
			found = true
		}
	}
	if !found {
		t.Errorf("romanised synonym was dropped: %v", sh.Aliases())
	}
}

// TestParseShowMissingFieldsAreZero: a sparse page yields zero values rather
// than an error. The site's record is incomplete for shows announced before
// their run is confirmed, and a partial record still beats nothing.
func TestParseShowMissingFieldsAreZero(t *testing.T) {
	sh, err := ParseShow(strings.NewReader(
		`<html><head><title>Some Show | AnimeSchedule</title></head><body></body></html>`), "slug")
	if err != nil {
		t.Fatalf("ParseShow: %v", err)
	}
	if sh.Episodes != 0 || sh.Status != "" || sh.Season != "" {
		t.Errorf("sparse page: eps=%d status=%q season=%q, want all zero",
			sh.Episodes, sh.Status, sh.Season)
	}
	if len(sh.Aliases()) != 0 {
		t.Errorf("sparse page aliases = %v, want none", sh.Aliases())
	}
}

func TestParseShowNoTitleIsError(t *testing.T) {
	if _, err := ParseShow(strings.NewReader("<html></html>"), "slug"); err == nil {
		t.Error("expected an error when no title can be parsed")
	}
}

// TestSeasonLengthKeepsMovieCount: a film reports "Episodes: 1" and that is the
// right answer — a film is one download. The season-complete check is
// `next > max`, so max=1 still permits episode 1 and stops once it is watched.
//
// This guards against reintroducing a "movies are not seasons" special case.
// Suppressing the count leaves max=0, which loses the plausibility bound and
// lets a mis-numbered release be accepted as episode 5 of a film.
func TestSeasonLengthKeepsMovieCount(t *testing.T) {
	cases := []struct {
		typ      string
		episodes int
		want     int
	}{
		{"TV", 12, 12},
		{"TV Short", 24, 24},
		{"Movie", 1, 1},
		{"OVA", 1, 1},
		{"ONA", 4, 4},
		{"TV", 0, 0}, // unknown
	}
	for _, c := range cases {
		sh := &Show{Type: c.typ, Episodes: c.episodes}
		if got := sh.SeasonLength(); got != c.want {
			t.Errorf("SeasonLength(%s, %d) = %d, want %d", c.typ, c.episodes, got, c.want)
		}
	}
}

func TestSlugFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4",
			"re-zero-kara-hajimeru-isekai-seikatsu-4"},
		{"animeschedule.net/anime/foo-bar", "foo-bar"},
		{"/anime/foo-bar", "foo-bar"},
		{"anime/foo-bar", "foo-bar"},
		{"foo-bar", "foo-bar"},
		{"https://animeschedule.net/anime/foo-bar?x=1", "foo-bar"},
		{"https://animeschedule.net/anime/foo-bar#frag", "foo-bar"},
		{"  https://animeschedule.net/anime/foo-bar/  ", "foo-bar"},
		// Not a schedule URL: a plain show name. Callers fall back to the
		// name path when this returns "".
		{"BLEACH: Thousand-Year Blood War - The Calamity", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := SlugFromURL(c.in); got != c.want {
			t.Errorf("SlugFromURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
