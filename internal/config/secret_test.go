package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecretFromEnvOverridesFile: an environment variable is the way to keep a
// credential out of the configuration file entirely. The file is plain text on
// disk and is the one file most likely to be backed up, copied, or committed
// by mistake.
func TestSecretFromEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kishizu.yaml")
	src := `server:
  library: /media/anime
  staging: /downloads/anime
  downloader:
    kind: qbittorrent
    qbittorrent_pass: from-file
  notifier:
    kind: gotify
    gotify_url: https://gotify.test
    gotify_token: token-from-file
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvQBittorrentPass, "from-env")
	t.Setenv(EnvGotifyToken, "token-from-env")

	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Server.Downloader.QBittorrentPass != "from-env" {
		t.Errorf("password = %q, want from-env", f.Server.Downloader.QBittorrentPass)
	}
	if f.Server.Notifier.GotifyToken != "token-from-env" {
		t.Errorf("token = %q, want token-from-env", f.Server.Notifier.GotifyToken)
	}
}

// TestFileWinsWhenEnvUnset: the file is the primary source. An unset variable
// must not blank out a credential the user put there deliberately.
func TestFileWinsWhenEnvUnset(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kishizu.yaml")
	src := `server:
  library: /a
  staging: /b
  downloader:
    qbittorrent_pass: from-file
  notifier:
    gotify_token: token-from-file
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// Explicitly empty, since t.Setenv would otherwise inherit the ambient
	// environment and make this test order-dependent.
	t.Setenv(EnvQBittorrentPass, "")
	t.Setenv(EnvGotifyToken, "")

	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Server.Downloader.QBittorrentPass != "from-file" {
		t.Errorf("password = %q, want from-file", f.Server.Downloader.QBittorrentPass)
	}
	if f.Server.Notifier.GotifyToken != "token-from-file" {
		t.Errorf("token = %q, want token-from-file", f.Server.Notifier.GotifyToken)
	}
}

// TestBlankEnvDoesNotOverride: a variable that is set but empty or whitespace
// is treated as unset. Otherwise an exported-but-empty variable in a compose
// file would silently clear a configured credential.
func TestBlankEnvDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kishizu.yaml")
	src := `server:
  library: /a
  staging: /b
  downloader:
    qbittorrent_pass: from-file
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvQBittorrentPass, "   ")

	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Server.Downloader.QBittorrentPass != "from-file" {
		t.Errorf("password = %q, want from-file (blank env must not clear it)",
			f.Server.Downloader.QBittorrentPass)
	}
}

// TestSecretEnvWithNoFile: a deployment with no configuration file at all
// still gets its credentials from the environment.
func TestSecretEnvWithNoFile(t *testing.T) {
	t.Setenv(EnvQBittorrentPass, "from-env")
	t.Setenv(EnvGotifyToken, "token-from-env")

	f, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Server.Downloader.QBittorrentPass != "from-env" {
		t.Errorf("password = %q, want from-env", f.Server.Downloader.QBittorrentPass)
	}
	if f.Server.Notifier.GotifyToken != "token-from-env" {
		t.Errorf("token = %q, want token-from-env", f.Server.Notifier.GotifyToken)
	}
}

// TestSecretEnvNamesAreStable: these are documented, so changing one is a
// breaking change for anyone who has set it.
func TestSecretEnvNamesAreStable(t *testing.T) {
	if EnvQBittorrentPass != "KISHIZU_QBITTORRENT_PASS" {
		t.Errorf("EnvQBittorrentPass = %q", EnvQBittorrentPass)
	}
	if EnvGotifyToken != "KISHIZU_GOTIFY_TOKEN" {
		t.Errorf("EnvGotifyToken = %q", EnvGotifyToken)
	}
	// Consistent with the existing KISHIZU_DEBUG convention.
	for _, n := range []string{EnvQBittorrentPass, EnvGotifyToken} {
		if !strings.HasPrefix(n, "KISHIZU_") {
			t.Errorf("%q should be prefixed KISHIZU_", n)
		}
	}
}
