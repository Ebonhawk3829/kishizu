package train

import "testing"

// TestInspectIncludesAudioTrack: the training panel must show the language
// track, not just the audio codec. It is the attribute the user corrects when
// a group writes an English dub in an unfamiliar way.
func TestInspectIncludesAudioTrack(t *testing.T) {
	g := Inspect("[Group] Show - 01 [1080p DUB]", 1)

	for _, a := range g.Attrs {
		if a.Key != AttrAudioKind {
			continue
		}
		if a.Label != "Audio track" {
			t.Errorf("label = %q, want Audio track", a.Label)
		}
		if a.Value != "english-dub" {
			t.Errorf("value = %q, want english-dub", a.Value)
		}
		if !a.Present {
			t.Error("an explicit dub marker must be present")
		}
		return
	}
	t.Fatal("Inspect did not include the audio track attribute")
}

// TestInspectAudioTrackAbsentWhenUnmarked: absence is honest. A title with no
// audio marker is not silently recorded as "sub"; the user can still correct
// it, but the panel does not invent a claim the title never made.
func TestInspectAudioTrackAbsentWhenUnmarked(t *testing.T) {
	g := Inspect("[Group] Show - 01 [1080p]", 1)

	for _, a := range g.Attrs {
		if a.Key == AttrAudioKind {
			if a.Present {
				t.Error("an unmarked title must not claim an audio track")
			}
			if a.Value != "" {
				t.Errorf("value = %q, want empty", a.Value)
			}
			return
		}
	}
	t.Fatal("Inspect did not include the audio track attribute")
}
