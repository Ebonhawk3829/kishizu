package store

import (
	"path/filepath"
	"testing"
)

// TestAliasesIncludeSanitisedForm: a show's alias set must include the
// Windows-sanitised form of each alias, so a filename kishizu itself wrote
// still matches on the way back.
//
// kishizu writes "Re:ZERO" to disk as "ReZERO" because colons are forbidden
// on Windows. Normalise turns punctuation into spaces, so the two forms
// tokenise differently — "re zero" vs "rezero" — and score zero against each
// other. A long canonical name survives that on its other tokens; a short
// alias does not, and the watch signal fails to match, so the episode is
// never marked watched and never deleted.
func TestAliasesIncludeSanitisedForm(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Re:ZERO", []string{"Re:ZERO"}, 25)
	m, err := s.NewMatcher(sh)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, a := range m.Aliases() {
		got[a] = true
	}
	if !got["Re:ZERO"] {
		t.Errorf("aliases missing the stored form: %v", m.Aliases())
	}
	if !got["ReZERO"] {
		t.Errorf("aliases missing the sanitised form: %v", m.Aliases())
	}
}

// TestAliasesDeduplicate: an alias that is already safe should not appear
// twice, since duplicates would skew nothing but do bloat the set.
func TestAliasesDeduplicate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Tomb Raider King", nil, 12)
	m, _ := s.NewMatcher(sh)
	if len(m.Aliases()) != 1 {
		t.Errorf("aliases = %v, want a single entry for an already-safe name", m.Aliases())
	}
}
