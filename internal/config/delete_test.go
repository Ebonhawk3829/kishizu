package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeDeleteConfig puts a minimal config file in a temp dir and loads it.
func writeDeleteConfig(t *testing.T, body string) *File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kishizu.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return f
}

// TestDeleteDefaults: with nothing set, the policy is immediate and the delay
// is zero — existing configs behave exactly as they did before this setting.
func TestDeleteDefaults(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Server.Delete != "immediate" {
		t.Errorf("default delete = %q, want immediate", f.Server.Delete)
	}
	d, err := f.Server.DeleteAfterDuration()
	if err != nil {
		t.Fatalf("DeleteAfterDuration: %v", err)
	}
	if d != 0 {
		t.Errorf("default delete_after = %v, want 0", d)
	}
}

// TestDeleteAfterParsing: the documented suffixes parse to the right
// durations, and garbage fails loudly rather than falling back to a default.
func TestDeleteAfterParsing(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"48h", 48 * time.Hour, true},
		{"7d", 7 * 24 * time.Hour, true},
		{"30d", 30 * 24 * time.Hour, true},
		{"90m", 90 * time.Minute, true},
		{"", 0, true},
		{"7x", 0, false},
		{"0h", 0, false},
		{"-5d", 0, false},
	}
	for _, c := range cases {
		s := &Server{DeleteAfter: c.in}
		got, err := s.DeleteAfterDuration()
		if c.ok {
			if err != nil {
				t.Errorf("%q: unexpected error %v", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("%q = %v, want %v", c.in, got, c.want)
			}
		} else if err == nil {
			t.Errorf("%q: expected error, got %v", c.in, got)
		}
	}
}

// TestDeleteFromFile: the yaml keys reach the struct through the merge.
func TestDeleteFromFile(t *testing.T) {
	f := writeDeleteConfig(t, "server:\n  delete: after\n  delete_after: 7d\nshows:\n")
	if f.Server.Delete != "after" {
		t.Errorf("delete = %q, want after", f.Server.Delete)
	}
	d, err := f.Server.DeleteAfterDuration()
	if err != nil {
		t.Fatalf("DeleteAfterDuration: %v", err)
	}
	if d != 7*24*time.Hour {
		t.Errorf("delete_after = %v, want 168h", d)
	}
}
