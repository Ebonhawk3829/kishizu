package train

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
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

func newShow(t *testing.T, st *store.Store, name string, aliases []string, max int) *store.Show {
	t.Helper()
	sh, err := st.CreateShow(name, aliases, max)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	return sh
}

func item(title string) nyaa.Item {
	return nyaa.Item{Title: title, InfoHash: title}
}

// TestSeedDerivesOffset: one example is enough to establish a group's offset.
func TestSeedDerivesOffset(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", []string{"Dogul Wang"}, 12)

	s, err := NewSession(st, sh, 9)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL AAC2.0 H.264"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := s.m.Offsets["ToonsHub"]; got != 0 {
		t.Errorf("ToonsHub offset = %d, want 0", got)
	}
}

// TestSeedDerivesNonZeroOffset covers the BLEACH case, where the number in the
// title is absolute and the offset is therefore non-zero.
func TestSeedDerivesNonZeroOffset(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War - The Calamity", []string{
		"Bleach: Sennen Kessen Hen - Kashin Tan",
		"BLEACH Thousand Year Blood War",
	}, 30)

	s, err := NewSession(st, sh, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed("[SubsPlease] Bleach - Sennen Kessen Hen - 47 (1080p) [B657D64E].mkv"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := s.m.Offsets["SubsPlease"]; got != 40 {
		t.Errorf("SubsPlease offset = %d, want 40", got)
	}
}

func TestSeedRejectsUnreadableTitle(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	s, _ := NewSession(st, sh, 1)
	if err := s.Seed("Show - Some Movie Release"); err == nil {
		t.Error("expected an error when no episode number can be read")
	}
}

// TestProposeRanksUncertaintyFirst is the core property: a release that matches
// the show but whose episode number is unreadable must be proposed before one
// the model is already confident about.
func TestProposeRanksUncertaintyFirst(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War - The Calamity", []string{
		"Bleach: Sennen Kessen Hen - Kashin Tan",
		"BLEACH Thousand Year Blood War",
	}, 30)

	s, _ := NewSession(st, sh, 7)
	if err := s.Seed("[Erai-raws] Bleach: Sennen Kessen Hen - Kashin Tan - 07 [1080p]"); err != nil {
		t.Fatal(err)
	}

	items := []nyaa.Item{
		// Confident and correct: low value as a question.
		item("[Erai-raws] Bleach: Sennen Kessen Hen - Kashin Tan - 07 [1080p DSNP WEB-DL]"),
		// Matches the show, number unreadable: maximally informative.
		item("[Lazier] Bleach Thousand-Year Blood War - 45v2 (WEB 1080p EAC3)"),
		// Absolute numbering, unseen group: needs confirming.
		item("[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p CR WEB-DL"),
	}

	got := s.Propose(items, 3)
	if len(got) == 0 {
		t.Fatal("no proposals")
	}
	if got[0].Uncertainty != 1.0 {
		t.Errorf("first proposal uncertainty = %.2f, want 1.0 (unreadable episode)", got[0].Uncertainty)
	}
	if got[0].Episode != 0 {
		t.Errorf("first proposal episode = %d, want 0 (unreadable)", got[0].Episode)
	}
}

// TestProposeSkipsAsked: the same release must not be proposed twice.
//
// Uses an UNSEEN group so the candidate is genuinely uncertain. A release from
// a group whose offset is already known is no longer proposed at all (see
// TestProposeSkipsConfident), so it would not exercise the asked-set here.
func TestProposeSkipsAsked(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", []string{"Dogul Wang"}, 12)
	s, _ := NewSession(st, sh, 9)
	_ = s.Seed("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL")

	it := item("[SomeOtherGroup] Tomb Raider King - 09 (WEB 1080p)")
	first := s.Propose([]nyaa.Item{it}, 1)
	if len(first) != 1 {
		t.Fatalf("first propose got %d, want 1", len(first))
	}
	if err := s.Accept(first[0]); err != nil {
		t.Fatal(err)
	}
	if again := s.Propose([]nyaa.Item{it}, 1); len(again) != 0 {
		t.Errorf("asked candidate proposed again: %+v", again)
	}
}

// TestProposeSkipsConfident: once a group's offset is known, releases from that
// group are resolved silently instead of being asked about.
//
// Regression: the tool used to ask "is this ep 7?" about releases it had already
// resolved as ep 9, which is asking the user to confirm something the model
// claims to know is false.
func TestProposeSkipsConfident(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", []string{"Dogul Wang"}, 12)
	s, _ := NewSession(st, sh, 9)
	_ = s.Seed("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL")

	// Same group, a different episode: the known offset resolves it to 10.
	it := item("[ToonsHub] Tomb Raider King S01E10 1080p CR WEB-DL")
	if got := s.Propose([]nyaa.Item{it}, 3); len(got) != 0 {
		t.Errorf("confident candidate was proposed: %+v", got)
	}
	// It should instead show up as resolved, so the learning is visible.
	res := s.Resolved([]nyaa.Item{it}, 5)
	if len(res) != 1 {
		t.Fatalf("resolved got %d, want 1", len(res))
	}
	if res[0].Episode != 10 {
		t.Errorf("resolved episode = %d, want 10", res[0].Episode)
	}
}

// TestTeachRecordsManualExample: the user can supply a known-good release
// directly, without answering questions until the model converges.
func TestTeachRecordsManualExample(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War - The Calamity", []string{
		"BLEACH Thousand Year Blood War",
	}, 30)
	s, _ := NewSession(st, sh, 7)

	// Absolute numbering: raw 47 is episode 7, so the offset is 40.
	if err := s.Teach("[SubsPlease] BLEACH: Sennen Kessen-hen - 47 (1080p) [ABCD1234]", 7); err != nil {
		t.Fatalf("Teach: %v", err)
	}
	if got := s.m.Offsets["SubsPlease"]; got != 40 {
		t.Errorf("SubsPlease offset = %d, want 40", got)
	}
}

// TestTeachRejectsUnreadableTitle: a title with no episode number teaches
// nothing, and must say so rather than silently recording a bogus offset.
func TestTeachRejectsUnreadableTitle(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", nil, 12)
	s, _ := NewSession(st, sh, 9)

	if err := s.Teach("[ToonsHub] Tomb Raider King 1080p CR WEB-DL", 9); err == nil {
		t.Error("expected an error for a title with no episode number")
	}
	if len(s.m.Offsets) != 0 {
		t.Errorf("offsets recorded from unreadable title: %v", s.m.Offsets)
	}
}

// TestAcceptLearnsUnseenGroupOffset: accepting a release from a group we have
// no offset for teaches us that group's offset.
func TestAcceptLearnsUnseenGroupOffset(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War - The Calamity", []string{
		"BLEACH Thousand Year Blood War",
	}, 30)
	s, _ := NewSession(st, sh, 7)
	_ = s.Seed("[Erai-raws] Bleach: Sennen Kessen Hen - Kashin Tan - 07 [1080p]")

	c := Candidate{Item: item("[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p CR WEB-DL")}
	if err := s.Accept(c); err != nil {
		t.Fatal(err)
	}
	if got := s.m.Offsets["ToonsHub"]; got != 40 {
		t.Errorf("ToonsHub offset = %d, want 40", got)
	}
}

// TestRejectQualityDoesNotTouchMatcher is the guard against the bug found in
// simulation: rejecting a release for quality must NOT change the matcher, or
// the tool learns that a correct episode is a bad match.
func TestRejectQualityDoesNotTouchMatcher(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Tomb Raider King", []string{"Dogul Wang"}, 12)
	s, _ := NewSession(st, sh, 9)
	_ = s.Seed("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL")

	before := len(s.m.Alias)
	offsetsBefore := len(s.m.Offsets)

	// Correct episode, wrong quality (HEVC).
	c := Candidate{Item: item("[ToonsHub] Tomb Raider King S01E09 1080p BILI WEB-DL AAC2.0 H.265")}
	if err := s.Reject(c, ReasonCodec); err != nil {
		t.Fatal(err)
	}

	if len(s.m.Offsets) != offsetsBefore {
		t.Error("quality rejection must not change offsets")
	}
	if len(s.m.Alias) != before {
		t.Error("quality rejection must not add aliases")
	}

	// It SHOULD have recorded a preference (ranking) and a rejection.
	prefs, err := st.Preferences(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefs) != 1 || prefs[0].Kind != "codec" {
		t.Errorf("preferences = %+v, want one codec preference", prefs)
	}
}

func TestRejectBatchAddsFilter(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "Show", nil, 12)
	s, _ := NewSession(st, sh, 1)

	c := Candidate{Item: item("[SubsPlease] Show (01-12) (1080p) [Batch]")}
	if err := s.Reject(c, ReasonBatch); err != nil {
		t.Fatal(err)
	}
	filters, err := st.Filters(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 1 || filters[0].Kind != "batch" {
		t.Errorf("filters = %+v, want one batch filter", filters)
	}
}

func TestReasonUpdatesMatcher(t *testing.T) {
	cases := map[Reason]bool{
		ReasonWrongEpisode: true,
		ReasonWrongShow:    true,
		ReasonBatch:        false,
		ReasonDub:          false,
		ReasonCodec:        false,
		ReasonQuality:      false,
		ReasonOther:        false,
	}
	for r, want := range cases {
		if got := r.UpdatesMatcher(); got != want {
			t.Errorf("%s.UpdatesMatcher() = %v, want %v", r, got, want)
		}
	}
}

// TestCommitPersists: what the session learned must survive to the database.
func TestCommitPersists(t *testing.T) {
	st := testStore(t)
	sh := newShow(t, st, "BLEACH: Thousand-Year Blood War - The Calamity", []string{
		"BLEACH Thousand Year Blood War",
	}, 30)
	s, _ := NewSession(st, sh, 7)
	_ = s.Seed("[SubsPlease] Bleach - Sennen Kessen Hen - 47 (1080p)")
	_ = s.Accept(Candidate{Item: item("[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p")})

	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	off, err := st.GroupOffsets(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if off["SubsPlease"] != 40 {
		t.Errorf("SubsPlease = %d, want 40", off["SubsPlease"])
	}
	if off["ToonsHub"] != 40 {
		t.Errorf("ToonsHub = %d, want 40", off["ToonsHub"])
	}
}

func TestExtractAliases(t *testing.T) {
	got := extractAliases("[ToonsHub] Tomb Raider King S01E09 1080p CR WEB-DL AAC2.0 H.264 (Dogul Wang, Multi-Subs)")
	want := map[string]bool{"Tomb Raider King": true, "Dogul Wang": true}
	for _, a := range got {
		if !want[a] {
			t.Errorf("unexpected alias %q", a)
		}
	}
	if len(got) != 2 {
		t.Errorf("aliases = %v, want %v", got, want)
	}
}
