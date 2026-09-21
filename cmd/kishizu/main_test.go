package main

import (
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/notify"
)

// TestBuildDownloaderDefaultsToTransmission: Transmission is what every
// existing deployment uses, so an unset value must keep working rather than
// fail to start.
func TestBuildDownloaderDefaultsToTransmission(t *testing.T) {
	dl, err := buildDownloader("", "http://host:9091/rpc", "", "", "")
	if err != nil {
		t.Fatalf("buildDownloader: %v", err)
	}
	if dl == nil {
		t.Fatal("downloader is nil")
	}
	if dl.Name() != "Transmission" {
		t.Errorf("Name = %q, want Transmission", dl.Name())
	}
}

// TestBuildDownloaderRejectsUnknown: an unrecognised name must be an error.
// Silently falling back to Transmission would leave the user believing they
// had configured something else.
func TestBuildDownloaderRejectsUnknown(t *testing.T) {
	if _, err := buildDownloader("deluge", "http://host:9091/rpc", "", "", ""); err == nil {
		t.Error("expected an error for an unknown downloader")
	}
}

// TestBuildDownloaderRequiresQBitURL: qBittorrent needs somewhere to talk to.
// Starting without it would fail on the first grab instead of at startup.
func TestBuildDownloaderRequiresQBitURL(t *testing.T) {
	if _, err := buildDownloader("qbittorrent", "", "", "", ""); err == nil {
		t.Error("expected an error when -qbittorrent is empty")
	}
	dl, err := buildDownloader("qbittorrent", "", "http://host:8080", "u", "p")
	if err != nil {
		t.Fatalf("buildDownloader: %v", err)
	}
	if dl.Name() != "qBittorrent" {
		t.Errorf("Name = %q, want qBittorrent", dl.Name())
	}
}

// TestBuildNotifierDefaultsToNone: notifications are opt-in. An unset value
// must not start posting somewhere the user did not choose.
func TestBuildNotifierDefaultsToNone(t *testing.T) {
	n, err := buildNotifier("", "", "", "")
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if n != nil {
		t.Errorf("notifier = %v, want nil (disabled)", n)
	}
}

// TestBuildNotifierNtfyNeedsATopic: ntfy is the default backend, but it needs
// a topic URL. An empty one means the user never configured it, which is the
// documented way to run without notifications — not an error.
func TestBuildNotifierNtfyNeedsATopic(t *testing.T) {
	n, err := buildNotifier("ntfy", "", "", "")
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if n != nil {
		t.Errorf("notifier = %v, want nil when no topic URL is given", n)
	}
	n, err = buildNotifier("ntfy", "https://ntfy.sh/kishizu", "", "")
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if n == nil || n.Name() != "ntfy" {
		t.Errorf("notifier = %v, want an ntfy notifier", n)
	}
}

// TestBuildNotifierGotifyNeedsBoth: Gotify needs a server and a token. One
// without the other cannot work, and failing at startup is better than
// failing silently on every notification.
func TestBuildNotifierGotifyNeedsBoth(t *testing.T) {
	if _, err := buildNotifier("gotify", "", "https://gotify.test", ""); err == nil {
		t.Error("expected an error when the token is missing")
	}
	if _, err := buildNotifier("gotify", "", "", "TOKEN"); err == nil {
		t.Error("expected an error when the URL is missing")
	}
	n, err := buildNotifier("gotify", "", "https://gotify.test", "TOKEN")
	if err != nil {
		t.Fatalf("buildNotifier: %v", err)
	}
	if n == nil || n.Name() != "gotify" {
		t.Errorf("notifier = %v, want a gotify notifier", n)
	}
}

// TestBuildNotifierRejectsUnknown: an unrecognised backend must be an error,
// not a silent default to none.
func TestBuildNotifierRejectsUnknown(t *testing.T) {
	if _, err := buildNotifier("pushover", "", "", ""); err == nil {
		t.Error("expected an error for an unknown notifier")
	}
}

// TestKindsAreBuildable: every advertised kind must actually be constructible.
// A kind listed in Kinds but rejected by the builder is a config option that
// cannot be used.
func TestKindsAreBuildable(t *testing.T) {
	for _, k := range download.Kinds {
		if _, err := download.ParseKind(string(k)); err != nil {
			t.Errorf("download.Kinds lists %q but ParseKind rejects it: %v", k, err)
		}
	}
	for _, k := range notify.Kinds {
		if _, err := notify.ParseKind(string(k)); err != nil {
			t.Errorf("notify.Kinds lists %q but ParseKind rejects it: %v", k, err)
		}
	}
}
