package release

import "testing"

// TestParseLibraryForm: kishizu must be able to read the episode number from
// the filenames it writes itself.
//
// On completion it renames a download to "<Show> - E<NN>.mkv", and the mpv
// watch signal sends that filename back. The parser did not recognise the
// "- E09" form, so every watch signal failed with "no confident match" —
// the tool could not read its own output.
func TestParseLibraryForm(t *testing.T) {
	cases := map[string]int{
		"Clevatess Season 2 - E09.mkv":                             9,
		"Tomb Raider King - E10.mkv":                               10,
		"BLEACH: Thousand-Year Blood War - The Calamity - E08.mkv": 8,
		"Show - E1.mkv":                                            1,
		"Show - E100.mkv":                                          100,
	}
	for title, want := range cases {
		if got := Parse(title).RawEpisode(); got != want {
			t.Errorf("Parse(%q).RawEpisode() = %d, want %d", title, got, want)
		}
	}
}

// TestLibraryFormDoesNotOverrideReleaseTitles: a real release title keeps its
// own reading. The library pattern must only fill in when nothing else found
// a number, or it would corrupt ordinary matching.
func TestLibraryFormDoesNotOverrideReleaseTitles(t *testing.T) {
	cases := map[string]int{
		"[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL": 9,
		"Show - 09 [1080p]":         9,
		"[Group] Show - 47 (1080p)": 47,
	}
	for title, want := range cases {
		if got := Parse(title).RawEpisode(); got != want {
			t.Errorf("Parse(%q).RawEpisode() = %d, want %d", title, got, want)
		}
	}
}
