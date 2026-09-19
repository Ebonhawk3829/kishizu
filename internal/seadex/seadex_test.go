package seadex

import (
	"os"
	"strings"
	"testing"
)

// loadFixture reads a recorded API response. Tests never touch the network:
// the fixture is a real capture, so the parsing is exercised against real
// shapes without depending on SeaDex being up.
func loadFixture(t *testing.T, name string) *Entry {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	e, err := ParseEntry(f)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if e == nil {
		t.Fatal("fixture parsed to no entry")
	}
	return e
}

// TestAniListIDFromURL: the entry page's path IS the AniList id, so a pasted
// link needs no lookup to become an API query.
func TestAniListIDFromURL(t *testing.T) {
	cases := map[string]int{
		"https://releases.moe/112124/":   112124,
		"https://releases.moe/112124":    112124,
		"releases.moe/112124":            112124,
		"/112124":                        112124,
		"112124":                         112124,
		"https://releases.moe/112124/?x": 112124,
		"https://releases.moe/":          0,
		"":                               0,
		"not-a-url":                      0,
	}
	for in, want := range cases {
		if got := AniListIDFromURL(in); got != want {
			t.Errorf("AniListIDFromURL(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestParseEntry: the fixture is the DanMachi III entry, which has one best
// Nyaa torrent and one that is not on Nyaa.
func TestParseEntry(t *testing.T) {
	e := loadFixture(t, "entry_112124.json")
	if e.AniListID != 112124 {
		t.Errorf("AniListID = %d, want 112124", e.AniListID)
	}
	if e.Incomplete {
		t.Error("entry should not be incomplete")
	}
	if len(e.Torrents) != 2 {
		t.Fatalf("got %d torrents, want 2", len(e.Torrents))
	}
	best := e.BestNyaa()
	if len(best) != 1 {
		t.Fatalf("got %d best Nyaa torrents, want 1", len(best))
	}
	if best[0].ReleaseGroup != "sam" {
		t.Errorf("group = %q, want sam", best[0].ReleaseGroup)
	}
	if best[0].InfoHash == "" {
		t.Error("infohash is empty; nothing could be handed to Transmission")
	}
	if len(best[0].Files) != 16 {
		t.Errorf("got %d files, want 16", len(best[0].Files))
	}
}

// TestClassifyFilesSeparatesEpisodesFromExtras: the pack has 12 episodes and
// 4 extras (NCOP, NCED, an OVA and the OVA's ending). The classifier must
// propose including exactly the 12.
func TestClassifyFilesSeparatesEpisodesFromExtras(t *testing.T) {
	e := loadFixture(t, "entry_112124.json")
	best := e.BestNyaa()[0]

	classes := ClassifyFiles(best.Files)
	if len(classes) != 16 {
		t.Fatalf("got %d rows, want 16 (all files are media)", len(classes))
	}

	var eps []int
	for _, c := range classes {
		if c.Include {
			eps = append(eps, c.Episode)
		}
	}
	if len(eps) != 12 {
		t.Fatalf("got %d included, want 12: %+v", len(eps), classes)
	}
	for i, n := range eps {
		if n != i+1 {
			t.Errorf("included[%d] = %d, want %d", i, n, i+1)
		}
	}
}

// TestClassifyDropsNonMedia: a pack can carry hundreds of scans and
// booklets. Listing them would make the review screen unusable, so they are
// dropped before the rows are built.
func TestClassifyDropsNonMedia(t *testing.T) {
	files := []File{
		{Name: "[sam] Show - 01 [BD 1080p].mkv"},
		{Name: "Advert 01.png"},
		{Name: "Booklet.pdf"},
		{Name: "Scans/art.png"},
		{Name: "[sam] Show - 02 [BD 1080p].mkv"},
	}
	classes := ClassifyFiles(files)
	if len(classes) != 2 {
		t.Fatalf("got %d rows, want 2 (only the media files)", len(classes))
	}
}

// TestDeriveTitle: the title is the text every file shares before the
// episode separator.
func TestDeriveTitle(t *testing.T) {
	e := loadFixture(t, "entry_112124.json")
	classes := ClassifyFiles(e.BestNyaa()[0].Files)

	want := "Dungeon ni Deai wo Motomeru no wa Machigatteiru Darou ka III"
	if got := DeriveTitle(classes); got != want {
		t.Errorf("DeriveTitle() = %q, want %q", got, want)
	}
}

// TestPlanEntry: the plan carries everything the review screen needs, and
// the dropdown bound is the media file count rather than the total file
// count.
func TestPlanEntry(t *testing.T) {
	e := loadFixture(t, "entry_112124.json")
	p, err := PlanEntry(e)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title == "" {
		t.Error("title not derived")
	}
	if p.MaxEpisode != 16 {
		t.Errorf("MaxEpisode = %d, want 16 (media file count)", p.MaxEpisode)
	}
	if len(p.Files) != 16 {
		t.Errorf("got %d rows, want 16", len(p.Files))
	}
	if p.Torrent.InfoHash == "" {
		t.Error("no infohash on the planned torrent")
	}
}

// TestValidateRejectsDuplicateEpisode: two files both tagged episode 1 would
// race in the reconciler, and which one won would depend on directory order.
// This must be caught before anything is handed to Transmission.
func TestValidateRejectsDuplicateEpisode(t *testing.T) {
	sel := []Selected{
		{Name: "a.mkv", Episode: 1},
		{Name: "b.mkv", Episode: 1},
	}
	if err := Validate(sel, 16); err == nil {
		t.Error("duplicate episode accepted")
	}
}

// TestValidateRejectsOutOfRange: the dropdown bounds the choice, so a number
// outside it is a mistake worth refusing.
func TestValidateRejectsOutOfRange(t *testing.T) {
	sel := []Selected{{Name: "a.mkv", Episode: 99}}
	if err := Validate(sel, 16); err == nil {
		t.Error("out-of-range episode accepted")
	}
}

// TestValidateAllowsUntracked: episode 0 means "download it but do not track
// it", which is how a special you actually want is handled. Several files may
// be untracked at once.
func TestValidateAllowsUntracked(t *testing.T) {
	sel := []Selected{
		{Name: "ova.mkv", Episode: 0},
		{Name: "ncop.mkv", Episode: 0},
		{Name: "01.mkv", Episode: 1},
	}
	if err := Validate(sel, 16); err != nil {
		t.Errorf("untracked rows rejected: %v", err)
	}
	if got := Episodes(sel); len(got) != 1 || got[0] != 1 {
		t.Errorf("Episodes() = %v, want [1]", got)
	}
}

// TestValidateAcceptsCleanSelection: the happy path, 12 distinct episodes.
func TestValidateAcceptsCleanSelection(t *testing.T) {
	var sel []Selected
	for i := 1; i <= 12; i++ {
		sel = append(sel, Selected{Name: "e.mkv", Episode: i})
	}
	if err := Validate(sel, 16); err != nil {
		t.Errorf("clean selection rejected: %v", err)
	}
	if got := Episodes(sel); len(got) != 12 {
		t.Errorf("Episodes() = %v, want 12 entries", got)
	}
}

// TestExtraDetectionIsWordBounded: a show with "Ending" or "OP" inside its
// own title must not have its episodes excluded by a loose substring match.
func TestExtraDetectionIsWordBounded(t *testing.T) {
	files := []File{
		{Name: "[sam] Show - 01 [BD].mkv"},
		{Name: "[sam] Show - NCOP [BD].mkv"},
		{Name: "[sam] Show - NCED [BD].mkv"},
		{Name: "[sam] Show - OVA [BD].mkv"},
	}
	classes := ClassifyFiles(files)
	included := 0
	for _, c := range classes {
		if c.Include {
			included++
		}
	}
	if included != 1 {
		t.Errorf("got %d included, want 1: %+v", included, classes)
	}
}

// TestVersionSuffixStillReadsEpisode: packs often carry "01v2" style names.
// The version suffix must not stop the number being read.
func TestVersionSuffixStillReadsEpisode(t *testing.T) {
	files := []File{{Name: "[sam] Show - 01v2 [BD 1080p FLAC] [47408149].mkv"}}
	classes := ClassifyFiles(files)
	if len(classes) != 1 || !classes[0].Include || classes[0].Episode != 1 {
		t.Errorf("got %+v, want one included episode 1", classes)
	}
	if !strings.Contains(classes[0].Why, "v2") {
		t.Errorf("Why = %q, should mention the version", classes[0].Why)
	}
}
