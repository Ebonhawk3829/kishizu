package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/config"
)

// TestSaveConfigNeverWritesEnvSecret: a credential supplied by environment
// variable must not be written into the configuration file.
//
// Saving it would silently defeat the entire reason for using an environment
// variable — the secret would end up in plain text on disk, in the one file
// most likely to be backed up or committed by mistake.
func TestSaveConfigNeverWritesEnvSecret(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  staging: /downloads/anime
  downloader:
    kind: qbittorrent
    qbittorrent_url: http://localhost:8080
  notifier:
    kind: gotify
    gotify_url: https://gotify.test
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(config.EnvQBittorrentPass, "super-secret-pass")
	t.Setenv(config.EnvGotifyToken, "super-secret-token")

	body := `{"library":"/media/anime","staging":"/downloads/anime","interval":"5m",
	  "keep":2,"dry_run":true,
	  "downloader":{"kind":"qbittorrent","qbittorrent_url":"http://localhost:8080","qbittorrent_pass":"` + SecretMask + `"},
	  "notifier":{"kind":"gotify","gotify_url":"https://gotify.test","gotify_token":"` + SecretMask + `"},
	  "indexer":{"base":"https://nyaa.si","category":"1_2","user_agent":"kishizu","min_interval":"1s"},
	  "quality":{"resolution_floor":"1080p","group_order":["VARYG"]},
	  "naming":{"preset":"kishizu","season_folder":false}}`
	rec := post(t, srv, "/api/config", body)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret-pass", "super-secret-token"} {
		if strings.Contains(string(onDisk), secret) {
			t.Errorf("the env-supplied secret %q was written to the config file", secret)
		}
	}
}

// TestGetConfigMarksEnvSecrets: the UI must be able to tell that a credential
// came from the environment, so it can show the field as read-only rather
// than as an empty field the user might try to fill in.
func TestGetConfigMarksEnvSecrets(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  staging: /downloads/anime
  downloader:
    kind: qbittorrent
  notifier:
    kind: gotify
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvQBittorrentPass, "super-secret-pass")
	t.Setenv(config.EnvGotifyToken, "super-secret-token")

	rec := httptestGet(t, srv, "/api/config")
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"super-secret-pass", "super-secret-token"} {
		if strings.Contains(body, secret) {
			t.Errorf("config response leaks the env secret %q", secret)
		}
	}
	if !strings.Contains(body, `"qbittorrent_pass_env":true`) {
		t.Error("qbittorrent_pass_env should be true when set by environment")
	}
	if !strings.Contains(body, `"gotify_token_env":true`) {
		t.Error("gotify_token_env should be true when set by environment")
	}
}

// TestGetConfigEnvFlagsFalseWhenFromFile: a credential in the file is not
// marked as environment-supplied, so the UI leaves the field editable.
func TestGetConfigEnvFlagsFalseWhenFromFile(t *testing.T) {
	srv, _, path := testServerWithConfig(t)
	src := `server:
  library: /media/anime
  staging: /downloads/anime
  downloader:
    kind: qbittorrent
    qbittorrent_pass: from-file
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvQBittorrentPass, "")
	t.Setenv(config.EnvGotifyToken, "")

	rec := httptestGet(t, srv, "/api/config")
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"qbittorrent_pass_env":true`) {
		t.Error("a file-supplied credential must not be marked as from-env")
	}
	if !strings.Contains(rec.Body.String(), SecretMask) {
		t.Error("a file-supplied credential must still be masked")
	}
}

// httptestGet is a GET against the server's handler.
func httptestGet(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}
