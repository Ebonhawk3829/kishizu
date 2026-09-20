package anilist

import (
	"strings"
	"testing"
)

// TestParseMedia: a real AniList response shape. The cover URL is what an
// adopted season uses for its poster, since SeaDex's own API exposes no image.
func TestParseMedia(t *testing.T) {
	body := `{"data":{"Media":{"id":112124,"coverImage":{"large":"https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/bx112124-ZmoOntBuiSUU.jpg"}}}}`
	m, err := ParseMedia(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("no media parsed")
	}
	if m.ID != 112124 {
		t.Errorf("ID = %d, want 112124", m.ID)
	}
	if m.CoverURL == "" {
		t.Error("cover URL is empty")
	}
}

// TestParseMediaMissing: AniList reports an unknown id as a null Media. That
// is "no art available", not an error worth failing an adoption over.
func TestParseMediaMissing(t *testing.T) {
	m, err := ParseMedia(strings.NewReader(`{"data":{"Media":null}}`))
	if err != nil {
		t.Fatalf("unknown id should not be an error: %v", err)
	}
	if m != nil {
		t.Errorf("got %+v, want nil", m)
	}
}

// TestFetchMediaRejectsBadID: a zero or negative id is a programming error,
// not a query to send.
func TestFetchMediaRejectsBadID(t *testing.T) {
	if _, err := New().FetchMedia(0); err == nil {
		t.Error("id 0 accepted")
	}
	if _, err := New().FetchMedia(-1); err == nil {
		t.Error("negative id accepted")
	}
}
