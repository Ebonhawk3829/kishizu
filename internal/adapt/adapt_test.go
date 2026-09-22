package adapt

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/match"
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

// TestShowSatisfiesTheMatcherContract: the adapter is the only thing joining
// the store to the matcher, so if it stops satisfying the interface nothing
// compiles — but the assertion is here so the intent is stated where the
// adapter lives rather than only in the package that consumes it.
func TestShowSatisfiesTheMatcherContract(t *testing.T) {
	var _ match.Show = (*Show)(nil)
}

// TestShowResolvesTwoConventions checks the store-backed Show resolves the
// BLEACH two-convention case through the database rather than in-memory
// fixtures.
func TestShowResolvesTwoConventions(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("BLEACH: Thousand-Year Blood War - The Calamity", []string{
		"Bleach: Sennen Kessen Hen - Kashin Tan",
		"BLEACH Thousand Year Blood War",
	}, 30)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetGroupOffset(sh.ID, "Erai-raws", 0, "seed")
	_ = s.SetGroupOffset(sh.ID, "SubsPlease", 40, "seed")

	m, err := NewVocab(s).Show(s, sh)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		title string
		want  int
	}{
		{"[Erai-raws] Bleach: Sennen Kessen Hen - Kashin Tan - 07 [1080p]", 7},
		{"[SubsPlease] Bleach - Sennen Kessen Hen - 47 (1080p) [B657D64E].mkv", 7},
		// ToonsHub is unseen; should generalise via the known offset set.
		{"[ToonsHub] BLEACH Thousand-Year Blood War S01E47 1080p CR WEB-DL", 7},
	}
	for _, tc := range cases {
		got := match.Match(m, tc.title)
		if !got.Matched || got.Episode != tc.want {
			t.Errorf("%s: matched=%v ep=%d want %d (%s)", tc.title, got.Matched, got.Episode, tc.want, got.Reason)
		}
	}
}

// TestGroupOffsetDoesNotMatchBySubstring: an earlier group lookup used
// unrestricted substring matching, so "BrandNewGroup" inherited group A's
// offset. Both implementations must behave the same way.
func TestGroupOffsetDoesNotMatchBySubstring(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupOffset(sh.ID, "A", 40, "training"); err != nil {
		t.Fatal(err)
	}
	m, err := NewVocab(s).Show(s, sh)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.GroupOffset("BrandNewGroup"); ok {
		t.Error("BrandNewGroup matched group A by substring")
	}
	if v, ok := m.GroupOffset("A"); !ok || v != 40 {
		t.Errorf("GroupOffset(A) = %d,%v want 40,true", v, ok)
	}
}

// TestGroupOffsetNormalisesPunctuation: the same group written with different
// punctuation must still resolve.
func TestGroupOffsetNormalisesPunctuation(t *testing.T) {
	s := testStore(t)
	sh, err := s.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupOffset(sh.ID, "Erai-raws", 0, "training"); err != nil {
		t.Fatal(err)
	}
	m, err := NewVocab(s).Show(s, sh)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"Erai-raws", "Erai_raws", "erai-raws", "[Erai-raws]"} {
		if _, ok := m.GroupOffset(g); !ok {
			t.Errorf("GroupOffset(%q) did not match Erai-raws", g)
		}
	}
}

// TestVocabIsSharedAcrossShows: the vocabulary is global, so building a view
// of two shows must not reload it. Loading per show per poll was a full table
// scan every tick.
func TestVocabIsSharedAcrossShows(t *testing.T) {
	s := testStore(t)
	a, err := s.CreateShow("A", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateShow("B", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVocab(s)
	sa, err := v.Show(s, a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := v.Show(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if sa.vocab != sb.vocab {
		t.Error("each show got its own vocabulary; it should be shared")
	}
}

// TestVocabLearnIsVisibleImmediately: a token taught through the cache must
// be honoured by the next parse. Writing only to the store would leave the
// running pipeline reading a stale vocabulary, so a token the user just
// taught would appear not to have been learned.
func TestVocabLearnIsVisibleImmediately(t *testing.T) {
	s := testStore(t)
	v := NewVocab(s)

	before := v.Parse("[Group] Show - 01 [1080p AVC]")
	if before.Codec != "" && before.Codec == "h.264" {
		t.Fatalf("precondition: AVC already resolves to h.264 (%q)", before.Codec)
	}

	if err := v.Learn(s, "codec", "AVC", "h.264"); err != nil {
		t.Fatalf("Learn: %v", err)
	}
	after := v.Parse("[Group] Show - 01 [1080p AVC]")
	if after.Codec != "h.264" {
		t.Errorf("codec = %q, want h.264: the learned token was not honoured", after.Codec)
	}
}

// TestVocabRefreshKeepsTheCurrentVocabularyOnFailure: a failed refresh must
// not unlearn everything the user has taught. Blanking it would silently
// undo training because of a transient database error.
func TestVocabRefreshKeepsTheCurrentVocabularyOnFailure(t *testing.T) {
	s := testStore(t)
	v := NewVocab(s)
	if err := v.Learn(s, "codec", "AVC", "h.264"); err != nil {
		t.Fatalf("Learn: %v", err)
	}
	s.Close() // every subsequent query fails

	v.Refresh(s)
	if got := v.Parse("[Group] Show - 01 [1080p AVC]"); got.Codec != "h.264" {
		t.Errorf("codec = %q, want h.264: a failed refresh unlearned the vocabulary", got.Codec)
	}
}
