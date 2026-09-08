package match

import (
	"testing"
)

// The reference set: 11 hand-labelled torrents supplied by the user, covering
// the three trickiest shows. These are the same cases the Python prototype was
// validated against, so this test is the port's acceptance criterion.
//
// Titles are the real Nyaa release names.
var referenceCases = []struct {
	show    string
	title   string
	wantEp  int
	wantHit bool
}{
	// BLEACH ep 7 — two numbering conventions in one show.
	{"bleach", "[Erai-raws] Bleach: Sennen Kessen Hen - Kashin Tan - 07 [1080p DSNP WEB-DL AVC AAC][MultiSub][18BD5661]", 7, true},
	{"bleach", "[SubsPlease] Bleach - Sennen Kessen Hen - 47 (1080p) [B657D64E].mkv", 7, true},
	{"bleach", "[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p CR WEB-DL AAC2.0 H.264 (BLEACH: Sennen Kessen-hen, Multi-Subs)", 7, true},
	{"bleach", "[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p DSNP WEB-DL AAC2.0 H.264 (BLEACH: Sennen Kessen-hen, Multi-Subs)", 7, true},
	{"bleach", "BLEACH Thousand Year Blood War S01E47 THE END 2 1080p DSNP WEB-DL AAC2.0 H.264-VARYG (BLEACH: Sennen Kessen-hen, Multi-Subs)", 7, true},

	// Re:ZERO ep 15 — single convention, but the alias is romaji.
	{"rezero", "[Erai-raws] Re:Zero kara Hajimeru Isekai Seikatsu 4th Season - 15 [1080p CR WEB-DL AVC AAC][MultiSub][354662D2]", 15, true},
	{"rezero", "Re ZERO Starting Life in Another World S03E15 1080p CR WEB-DL AAC2.0 H 264-VARYG (Re:Zero kara Hajimeru Isekai Seikatsu 2nd Season Part 2, Multi-Subs)", 15, true},
	{"rezero", "Re ZERO Starting Life in Another World S03E15 1080p CR WEB-DL AAC2.0 H 264 DUAL-VARYG (Re:Zero kara Hajimeru Isekai Seikatsu 2nd Season Part 2, Dual-Audio, Multi-Subs)", 15, true},

	// Tomb Raider King ep 9 — matched via the alternate title "Dogul Wang".
	{"trk", "[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL AAC2.0 H.264 (Dogul Wang, Multi-Subs, Japanese Dub)", 9, true},
	{"trk", "Tomb Raider King S01E09 The Ruler of Relics 1080p CR WEB-DL AAC2.0 H.264-VARYG (Dogul Wang, Multi-Subs)", 9, true},
	{"trk", "[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL AAC2.0 H.264 (Dogul Wang, Multi-Subs, Korean Audio)", 9, true},
}

func refShows() map[string]*MemShow {
	return map[string]*MemShow{
		"bleach": {
			Name: "BLEACH: Thousand-Year Blood War - The Calamity",
			Alias: []string{
				"Bleach: Sennen Kessen Hen - Kashin Tan",
				"BLEACH Sennen Kessen-hen",
				"Bleach Sennen Kessen Hen",
				"BLEACH Thousand Year Blood War",
			},
			Max:      30,
			Offsets:  map[string]int{"Erai-raws": 0, "SubsPlease": 40, "ToonsHub": 40, "(none)": 40},
			Defaults: []int{0, 40},
		},
		"rezero": {
			Name: "Re:ZERO -Starting Life in Another World- Season 4",
			Alias: []string{
				"Re:Zero kara Hajimeru Isekai Seikatsu 4th Season",
				"Re ZERO Starting Life in Another World",
				"Re:Zero kara Hajimeru Isekai Seikatsu",
			},
			Max:      30,
			Offsets:  map[string]int{"Erai-raws": 0, "(none)": 0},
			Defaults: []int{0},
		},
		"trk": {
			Name:     "Tomb Raider King",
			Alias:    []string{"Dogul Wang"},
			Max:      30,
			Offsets:  map[string]int{"ToonsHub": 0, "(none)": 0},
			Defaults: []int{0},
		},
	}
}

func TestReferenceSet(t *testing.T) {
	shows := refShows()
	for _, tc := range referenceCases {
		s, ok := shows[tc.show]
		if !ok {
			t.Fatalf("unknown show key %q", tc.show)
		}
		got := Match(s, tc.title)
		if got.Matched != tc.wantHit {
			t.Errorf("%s: matched=%v want %v (%s)\n  %s", tc.show, got.Matched, tc.wantHit, got.Reason, tc.title)
			continue
		}
		if got.Episode != tc.wantEp {
			t.Errorf("%s: episode=%d want %d (%s)\n  %s", tc.show, got.Episode, tc.wantEp, got.Reason, tc.title)
		}
	}
}

// TestUnseenGroupGeneralises is the key property: a group never seen in training
// still resolves, because it is tried against the offsets the show has exhibited.
func TestUnseenGroupGeneralises(t *testing.T) {
	s := &MemShow{
		Name:     "BLEACH: Thousand-Year Blood War - The Calamity",
		Alias:    []string{"BLEACH Thousand Year Blood War", "Bleach Sennen Kessen Hen"},
		Max:      30,
		Offsets:  map[string]int{"Erai-raws": 0, "SubsPlease": 40},
		Defaults: []int{0, 40},
	}
	// VARYG was never in the offset map.
	got := Match(s, "BLEACH Thousand Year Blood War S01E47 THE END 2 1080p DSNP WEB-DL AAC2.0 H.264-VARYG")
	if !got.Matched || got.Episode != 7 {
		t.Fatalf("unseen group should generalise: matched=%v ep=%d (%s)", got.Matched, got.Episode, got.Reason)
	}
}

// TestRejectsOtherShows guards against false positives. These are real titles
// from the live feed that share tokens with the seed shows but are not them.
func TestRejectsOtherShows(t *testing.T) {
	shows := refShows()
	negatives := []string{
		"[ToonsHub] Skeleton Knight in Another World S02E10 1080p CR WEB-DL DUAL AAC2.0 H.264",
		"[Ironclad] Gaikotsu Kishi-sama - S02E10 [WEB.1080p.AV1] | Skeleton Knight in Another World",
		"[DKB] Suterare Seijo no Isekai Gohan Tabi: Kakure Skill de Camping Car wo Shoukan shitemita",
		"Ultraman Zero - Cosmo Rise (Youtube)",
		"[SubsPlease] Ao no Miburo (01-24) (1080p) [Batch]",
	}
	for _, title := range negatives {
		for key, s := range shows {
			if got := Match(s, title); got.Matched {
				t.Errorf("%s should not match %q (score passed, ep=%d, %s)", key, title, got.Episode, got.Reason)
			}
		}
	}
}

// TestBatchIsNotAnEpisode: batch releases must not resolve to a single episode.
// They are out of scope and would otherwise look like a plausible low episode.
func TestBatchIsNotAnEpisode(t *testing.T) {
	s := refShows()["trk"]
	got := Match(s, "[SubsPlease] Tomb Raider King (01-12) (1080p) [Batch]")
	if got.Matched && got.Episode > 0 {
		t.Errorf("batch should not resolve to episode %d", got.Episode)
	}
}
