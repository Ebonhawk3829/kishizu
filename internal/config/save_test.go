package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveRoundTrips: a file written by Save must load back.
//
// Regression: the YAML encoder writes top-level keys at column 0, so encoding
// the server struct directly after a "server:" line produced keys that were
// siblings of "server:" rather than children of it. The result was invalid
// YAML that the show parser then rejected — so saving settings once made the
// whole file unloadable, and kishizu would refuse to start.
func TestSaveRoundTrips(t *testing.T) {
	cur := &File{
		Server: DefaultServer(),
		Shows: []Show{
			{Name: "Tomb Raider King", Aliases: []string{"Dogul Wang"}, Watched: 9, Max: 12},
		},
	}
	next := DefaultServer()
	next.Library = "/new/anime"
	next.Downloader.Kind = "qbittorrent"
	next.Downloader.QBittorrentPass = "hunter2"

	p := filepath.Join(t.TempDir(), "kishizu.yaml")
	if err := Save(p, cur, next); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.Server.Library != "/new/anime" {
		t.Errorf("library = %q, want /new/anime", got.Server.Library)
	}
	if got.Server.Downloader.Kind != "qbittorrent" {
		t.Errorf("downloader = %q", got.Server.Downloader.Kind)
	}
	if got.Server.Downloader.QBittorrentPass != "hunter2" {
		t.Errorf("password = %q", got.Server.Downloader.QBittorrentPass)
	}
	// The show list is preserved verbatim.
	if len(got.Shows) != 1 || got.Shows[0].Name != "Tomb Raider King" {
		t.Errorf("shows = %+v, want the original list", got.Shows)
	}
}

// TestSaveIndentsUnderServer: the server keys must be nested, not siblings —
// sibling keys parse as a different document, and the show parser would then
// reject the file kishizu itself just wrote.
func TestSaveIndentsUnderServer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kishizu.yaml")
	if err := Save(p, &File{Server: DefaultServer()}, DefaultServer()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	inServer := false
	for _, l := range lines {
		if l == "server:" {
			inServer = true
			continue
		}
		if inServer && strings.TrimSpace(l) == "" {
			continue
		}
		// A top-level key ends the server block.
		if inServer && l != "" && !strings.HasPrefix(l, " ") {
			inServer = false
			continue
		}
		if inServer && strings.HasSuffix(l, ":") && strings.HasPrefix(l, "  ") {
			// A nested section header, fine.
			continue
		}
		if inServer && !strings.HasPrefix(l, "  ") {
			t.Errorf("server key %q is not indented under server:", l)
		}
	}
}

// TestSaveIsAtomic: a crash mid-write must not leave a file that fails to
// parse. The temp-then-rename is what guarantees that.
func TestSaveIsAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kishizu.yaml")
	if err := Save(p, &File{Server: DefaultServer()}, DefaultServer()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("a .tmp file was left behind")
	}
}

// TestSaveRejectsNil: nothing to save is a programming error, not an empty
// file. Writing an empty config would silently reset everything to defaults.
func TestSaveRejectsNil(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kishizu.yaml")
	if err := Save(p, &File{Server: DefaultServer()}, nil); err == nil {
		t.Error("expected an error for a nil server")
	}
}

// TestSaveCreatesFile: the file may not exist yet, which is the normal case
// for someone configuring kishizu from the UI for the first time.
func TestSaveCreatesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "kishizu.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, &File{Server: DefaultServer()}, DefaultServer()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file was not created: %v", err)
	}
}
