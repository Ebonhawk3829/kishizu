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
	want := []string{
		"Re:Zero kara Hajimeru Isekai Seikatsu 4",
		"Re:Zero - Starting Life in Another World 4",
		"Re:ゼロから始める異世界生活 4",
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

// TestAbbreviationExcludedFromAliases: the abbreviation is too short to match
// on safely, so it must not reach the matcher. It is still available for feed
// queries, where a broad net is what you want.
func TestAbbreviationExcludedFromAliases(t *testing.T) {
	sh, err := ParseShow(strings.NewReader(showPage), "slug")
	if err != nil {
		t.Fatalf("ParseShow: %v", err)
	}
	for _, a := range sh.Aliases() {
		if a == "ReZero 4" {
			t.Error("abbreviation leaked into Aliases(); it must stay feed-only")
		}
	}
	if got := sh.Abbreviations(); len(got) != 1 || got[0] != "ReZero 4" {
		t.Errorf("Abbreviations() = %v, want [ReZero 4]", got)
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

// TestFindWithAliasesSkipsShortAliases: recall scoring rewards an alias whose
// tokens are all present in the candidate, so a very short alias scores a
// perfect 1.0 against almost anything.
//
// This is not hypothetical. "シャンフロ３" (two tokens) matched "PetitCure:
// Precure Fairies 3rd Season" at ep 26, and the daily refresh then projected 26
// episode rows onto Shangri-La Frontier — a different show entirely. Short
// aliases carry too little identity to be trusted on the fallback path.
func TestFindWithAliasesSkipsShortAliases(t *testing.T) {
	entries := []Entry{
		{Title: "PetitCure: Precure Fairies 3rd Season", NextEp: 26, Slug: "petitcure"},
		{Title: "Re:Zero kara Hajimeru Isekai Seikatsu 4", NextEp: 18, Slug: "rezero"},
	}

	// A two-token alias must not match, however well it scores.
	if e := FindWithAliases(entries, []string{"シャンフロ３"}); e != nil {
		t.Errorf("short alias matched %q; it must be skipped", e.Title)
	}
	// A long alias still matches normally.
	if e := FindWithAliases(entries, []string{"Re:Zero kara Hajimeru Isekai Seikatsu 4"}); e == nil {
		t.Error("long alias should still match")
	} else if e.Slug != "rezero" {
		t.Errorf("matched %q, want rezero", e.Slug)
	}
	// A short alias alongside a long one must not win by scoring higher.
	if e := FindWithAliases(entries, []string{"シャンフロ３", "Re:Zero kara Hajimeru Isekai Seikatsu 4"}); e == nil || e.Slug != "rezero" {
		t.Errorf("mixed aliases matched %v, want rezero", e)
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
