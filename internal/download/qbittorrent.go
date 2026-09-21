package download

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// qBittorrent talks to qBittorrent's WebUI API.
//
// The API is form-encoded rather than JSON, and authentication is a cookie
// obtained once from /api/v2/auth/login. Both are handled here so callers
// only ever call Add.
type qBittorrent struct {
	base string
	user string
	pass string
	hc   *http.Client
}

// NewQBittorrent builds a qBittorrent client. base is the WebUI root, e.g.
// http://localhost:8080. user and pass may be empty when the WebUI has
// "Bypass authentication for clients on localhost" enabled, which is the
// common case for a client on the same host.
func NewQBittorrent(base, user, pass string) Downloader {
	return &qBittorrent{
		base: strings.TrimRight(base, "/"),
		user: user,
		pass: pass,
		hc:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (q *qBittorrent) Name() string { return "qBittorrent" }

// Add starts a download from a magnet link into dir.
//
// savepath is qBittorrent's per-torrent download directory, which is the
// whole reason kishizu can use it: without it files land in the client's
// default and the reconciler never sees them.
func (q *qBittorrent) Add(magnet, dir string) error {
	if err := q.login(); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("urls", magnet)
	form.Set("savepath", dir)
	// Not auto-managed: kishizu decides what to grab, so the client must not
	// also apply its own rules about where things go.
	form.Set("autoTMM", "false")

	resp, err := q.hc.PostForm(q.base+"/api/v2/torrents/add", form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("qbittorrent add: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// login obtains the session cookie. It is cheap and idempotent, so it runs
// before every add rather than being cached: a WebUI restart or an expired
// cookie then costs one extra request instead of a permanently broken client.
func (q *qBittorrent) login() error {
	form := url.Values{}
	form.Set("username", q.user)
	form.Set("password", q.pass)
	resp, err := q.hc.PostForm(q.base+"/api/v2/auth/login", form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Draining the body lets the connection go back to the pool.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	// qBittorrent answers 200 with the body "Fails." on a bad password, and
	// 403 when the IP is banned for too many failures. Both are auth
	// failures; only the status distinguishes them.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent login: %s", resp.Status)
	}
	return nil
}
