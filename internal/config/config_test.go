package config

import "testing"

func TestParseSeed(t *testing.T) {
	src := `
# a comment
shows:
  - name: Tomb Raider King
    aliases:
      - Dogul Wang
      - Dogulwang
    watched: 9
    max: 12

  - name: BLEACH: Thousand-Year Blood War
    aliases:
      - Bleach: Sennen Kessen Hen
    watched: 7
    max: 13
`
	shows, err := parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(shows) != 2 {
		t.Fatalf("got %d shows, want 2", len(shows))
	}
	if shows[0].Name != "Tomb Raider King" {
		t.Errorf("name = %q", shows[0].Name)
	}
	if len(shows[0].Aliases) != 2 || shows[0].Aliases[0] != "Dogul Wang" {
		t.Errorf("aliases = %v", shows[0].Aliases)
	}
	if shows[0].Watched != 9 || shows[0].Max != 12 {
		t.Errorf("watched/max = %d/%d, want 9/12", shows[0].Watched, shows[0].Max)
	}
	if shows[1].Name != "BLEACH: Thousand-Year Blood War" {
		t.Errorf("second name = %q", shows[1].Name)
	}
	if shows[1].Watched != 7 || shows[1].Max != 13 {
		t.Errorf("second watched/max = %d/%d, want 7/13", shows[1].Watched, shows[1].Max)
	}
}

// TestParseRejectsBadInput: a malformed file must fail loudly rather than
// silently seeding a partial or wrong list.
func TestParseRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"bad watched": "shows:\n  - name: X\n    watched: abc\n",
		"unknown key": "shows:\n  - name: X\n    colour: red\n",
		"no name":     "shows:\n  - watched: 3\n",
	}
	for name, src := range cases {
		if _, err := parse(src); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// TestParseEmptyIsEmpty: an empty or comment-only file yields no shows, not an
// error, so a user can start from a blank slate.
func TestParseEmptyIsEmpty(t *testing.T) {
	for _, src := range []string{"", "# just a comment\n", "shows:\n"} {
		shows, err := parse(src)
		if err != nil {
			t.Fatalf("parse(%q): %v", src, err)
		}
		if len(shows) != 0 {
			t.Errorf("parse(%q) = %d shows, want 0", src, len(shows))
		}
	}
}
