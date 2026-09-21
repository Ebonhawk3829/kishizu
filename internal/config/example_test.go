package config

import (
	"os"
	"testing"
)

// TestExampleFileIsValid: the shipped example must actually load.
//
// It is the thing a new user copies, so a mistake in it is a mistake in
// everyone's first run. It is also the documentation of every key, so it
// must stay in step with the struct.
func TestExampleFileIsValid(t *testing.T) {
	b, err := os.ReadFile("../../kishizu.yaml.example")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}
	if f.Server == nil {
		t.Fatal("no server section")
	}
	// The example is meant to be complete, so every documented key should
	// be present rather than defaulted.
	if f.Server.Library == "" || f.Server.Staging == "" {
		t.Error("example must show library and staging")
	}
	if f.Server.Downloader.Kind == "" {
		t.Error("example must show a downloader kind")
	}
	if f.Server.Notifier.Kind == "" {
		t.Error("example must show a notifier kind")
	}
	if f.Server.Indexer.Base == "" || f.Server.Indexer.Category == "" {
		t.Error("example must show the indexer")
	}
	if f.Server.Quality.ResolutionFloor == "" || len(f.Server.Quality.GroupOrder) == 0 {
		t.Error("example must show the quality policy")
	}
	if f.Server.Naming.Preset == "" {
		t.Error("example must show a naming preset")
	}
	if len(f.Shows) == 0 {
		t.Error("example must show at least one show")
	}
}

// TestExampleRoundTrips: the example must survive a save, since the settings
// UI rewrites whatever file it is pointed at.
func TestExampleRoundTrips(t *testing.T) {
	b, err := os.ReadFile("../../kishizu.yaml.example")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	cur, err := Parse(b)
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}
	p := t.TempDir() + "/kishizu.yaml"
	if err := Save(p, cur, cur.Server); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after, err := Load(p)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if after.Server.Library != cur.Server.Library {
		t.Errorf("library = %q, want %q", after.Server.Library, cur.Server.Library)
	}
	if len(after.Shows) != len(cur.Shows) {
		t.Errorf("got %d shows, want %d", len(after.Shows), len(cur.Shows))
	}
}
