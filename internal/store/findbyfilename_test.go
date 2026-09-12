package store

import (
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// TestFindByFileNameExact: the watch signal resolves by exact filename, not by
// scoring. kishizu named the file itself when the download completed and
// stored the path, so the name is known — there is nothing to infer.
func TestFindByFileNameExact(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Re:ZERO -Starting Life in Another World- Season 4", nil, 25)
	_ = s.UpsertEpisode(sh.ID, 16, episode.Downloaded, "", "")
	// The name kishizu actually writes: colon stripped for Windows.
	const name = "ReZERO -Starting Life in Another World- Season 4 - E16.mkv"
	if err := s.SetFilePath(sh.ID, 16, "/media/anime/"+name); err != nil {
		t.Fatal(err)
	}

	ep, err := s.FindByFileName(name)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if ep == nil {
		t.Fatal("no episode found for a name kishizu itself wrote")
	}
	if ep.ShowID != sh.ID || ep.Number != 16 {
		t.Errorf("got show %d ep %d, want show %d ep 16", ep.ShowID, ep.Number, sh.ID)
	}
}

// TestFindByFileNameMisses: an unknown name resolves to nothing rather than a
// near miss. A wrong guess here would delete a file the user still wants.
func TestFindByFileNameMisses(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sh, _ := s.CreateShow("Show", nil, 12)
	_ = s.UpsertEpisode(sh.ID, 1, episode.Downloaded, "", "")
	_ = s.SetFilePath(sh.ID, 1, "/media/anime/Show - E01.mkv")

	for _, q := range []string{"Show - E02.mkv", "Other - E01.mkv", ""} {
		if ep, _ := s.FindByFileName(q); ep != nil {
			t.Errorf("FindByFileName(%q) = show %d ep %d, want no match", q, ep.ShowID, ep.Number)
		}
	}
}

// TestBaseNameSplitsBothSeparators: the watch signal arrives from the user's
// PC, which may send Windows paths.
func TestBaseNameSplitsBothSeparators(t *testing.T) {
	cases := map[string]string{
		`C:\Anime\Show - E01.mkv`:     "Show - E01.mkv",
		"/media/anime/Show - E01.mkv": "Show - E01.mkv",
		"Show - E01.mkv":              "Show - E01.mkv",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}
