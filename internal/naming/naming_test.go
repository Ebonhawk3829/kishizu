package naming

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRoundTrip: the writer and the reader must agree for every preset.
//
// This is the failure mode the whole package exists to prevent: a scheme that
// writes a name it cannot read back leaves every watch signal unmatched, so
// files pile up on disk because nothing ever marks them watched.
func TestRoundTrip(t *testing.T) {
	for _, p := range []Preset{PresetKishizu, PresetSonarr, PresetPlex} {
		s, err := Resolve(p, "", nil)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", p, err)
		}
		for _, ep := range []int{1, 9, 10, 100} {
			name := s.Filename("Show Name", 1, ep, ".mkv")
			if got := s.EpisodeFrom(name); got != ep {
				t.Errorf("%s: EpisodeFrom(%q) = %d, want %d", p, name, got, ep)
			}
		}
	}
}

// TestPresetLayouts: each preset produces the layout it claims. These are the
// shapes people's existing libraries already have, so they must be exact.
func TestPresetLayouts(t *testing.T) {
	cases := []struct {
		preset Preset
		want   string
		dir    string
	}{
		{PresetKishizu, "Show - E09.mkv", filepath.Join("/lib", "Show")},
		{PresetSonarr, "Show - S01E09.mkv", filepath.Join("/lib", "Show", "Season 01")},
		{PresetPlex, "Show - s01e09.mkv", filepath.Join("/lib", "Show", "Season 01")},
	}
	for _, c := range cases {
		s, err := Resolve(c.preset, "", nil)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", c.preset, err)
		}
		if got := s.Filename("Show", 1, 9, ".mkv"); got != c.want {
			t.Errorf("%s: filename = %q, want %q", c.preset, got, c.want)
		}
		if got := s.Dir("/lib", "Show", 1); got != c.dir {
			t.Errorf("%s: dir = %q, want %q", c.preset, got, c.dir)
		}
	}
}

// TestCustomPattern: a custom layout must work end to end, including reading
// the episode back out.
func TestCustomPattern(t *testing.T) {
	s, err := Resolve(PresetCustom, "{show} S{season:2}E{episode:2}", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	name := s.Filename("Show", 2, 7, ".mkv")
	if name != "Show S02E07.mkv" {
		t.Errorf("filename = %q", name)
	}
	if got := s.EpisodeFrom(name); got != 7 {
		t.Errorf("EpisodeFrom(%q) = %d, want 7", name, got)
	}
}

// TestCustomRequiresPattern: a custom preset with no pattern cannot produce a
// filename at all, so it must fail at config time rather than at rename time.
func TestCustomRequiresPattern(t *testing.T) {
	if _, err := Resolve(PresetCustom, "", nil); err == nil {
		t.Error("expected an error for custom with no pattern")
	}
	if _, err := Resolve(PresetCustom, "   ", nil); err == nil {
		t.Error("expected an error for a blank pattern")
	}
}

// TestValidatePattern: a pattern that cannot produce a usable filename must be
// rejected. Without {episode} the watch signal can never read a number back.
func TestValidatePattern(t *testing.T) {
	bad := map[string]string{
		"no show":         "{episode:2}",
		"no episode":      "{show} - E",
		"unknown token":   "{show} - {epsiode:2}",
		"unclosed brace":  "{show - E{episode:2}",
		"path separator":  "{show}/{episode:2}",
		"windows illegal": "{show}: {episode:2}",
		"empty":           "",
	}
	for name, p := range bad {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("%s: ValidatePattern(%q) = nil, want an error", name, p)
		}
	}
	good := []string{
		"{show} - E{episode:2}",
		"{show} - S{season:2}E{episode:2}",
		"{show} {episode}",
		"{show} Episode {episode:2}",
	}
	for _, p := range good {
		if err := ValidatePattern(p); err != nil {
			t.Errorf("ValidatePattern(%q) = %v, want nil", p, err)
		}
	}
}

// TestSeasonFolderOverride: an explicit season_folder wins over the preset's
// default, so a user can take the sonarr filename without the directory.
func TestSeasonFolderOverride(t *testing.T) {
	no := false
	yes := true

	s, _ := Resolve(PresetSonarr, "", &no)
	if strings.Contains(s.Dir("/lib", "Show", 1), "Season") {
		t.Error("season_folder=false must override the sonarr preset")
	}

	k, _ := Resolve(PresetKishizu, "", &yes)
	if !strings.Contains(k.Dir("/lib", "Show", 1), "Season") {
		t.Error("season_folder=true must add a season directory")
	}
}

// TestParsePresetDefaultsToKishizu: an unset value must keep the existing
// layout. Silently renaming someone's whole library is unacceptable.
func TestParsePresetDefaultsToKishizu(t *testing.T) {
	for _, in := range []string{"", "kishizu", "Kishizu", " kishizu "} {
		got, err := ParsePreset(in)
		if err != nil {
			t.Errorf("ParsePreset(%q): %v", in, err)
			continue
		}
		if got != PresetKishizu {
			t.Errorf("ParsePreset(%q) = %q, want kishizu", in, got)
		}
	}
	if _, err := ParsePreset("jellyfin"); err == nil {
		t.Error("expected an error for an unknown preset")
	}
}

// TestPresetsAreResolvable: every advertised preset must actually resolve.
func TestPresetsAreResolvable(t *testing.T) {
	for _, p := range Presets {
		if _, err := ParsePreset(string(p)); err != nil {
			t.Errorf("Presets lists %q but ParsePreset rejects it: %v", p, err)
		}
	}
}

// TestCandidatesCoversExtensions: the healer finds a file whose extension it
// does not know by trying each one. Every candidate must be a real path under
// the right directory.
func TestCandidatesCoversExtensions(t *testing.T) {
	s, _ := Resolve(PresetKishizu, "", nil)
	got := s.Candidates("/lib", "Show", 1, 9)
	if len(got) != len(Extensions) {
		t.Fatalf("got %d candidates, want %d", len(got), len(Extensions))
	}
	for _, c := range got {
		if filepath.Dir(c) != filepath.Join("/lib", "Show") {
			t.Errorf("candidate %q is in the wrong directory", c)
		}
		if !strings.Contains(c, "E09") {
			t.Errorf("candidate %q does not carry the episode", c)
		}
	}
}

// TestSanitiseIsApplied: a show name with characters illegal in a filename
// must still produce a usable path.
func TestSanitiseIsApplied(t *testing.T) {
	s, _ := Resolve(PresetKishizu, "", nil)
	name := s.Filename(`Show: A/B?C`, 1, 1, ".mkv")
	for _, c := range []string{":", "/", "?", "\\"} {
		if strings.Contains(name, c) {
			t.Errorf("filename %q contains illegal %q", name, c)
		}
	}
}

// TestEpisodeFromRejectsOtherForms: a name that does not match the scheme must
// read as 0, not as a guess. A wrong guess here deletes a file the user may
// still want.
func TestEpisodeFromRejectsOtherForms(t *testing.T) {
	s, _ := Resolve(PresetKishizu, "", nil)
	for _, name := range []string{
		"[Group] Show - 09 [1080p].mkv", // a release title, not a library name
		"Show - S01E09.mkv",             // a different scheme
		"random.mkv",
		"",
	} {
		if got := s.EpisodeFrom(name); got != 0 {
			t.Errorf("EpisodeFrom(%q) = %d, want 0", name, got)
		}
	}
}
