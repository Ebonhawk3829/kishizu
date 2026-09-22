package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
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
//
// The client carries a cookie jar. Without one the SID cookie that login
// obtains is discarded by Go's http.Client, so the following /torrents/add
// is unauthenticated and the WebUI answers 403 — which surfaces only as an
// add failure with no obvious cause. The bug is invisible on a localhost
// deployment with auth bypassed, which is why it survived.
func NewQBittorrent(base, user, pass string) Downloader {
	jar, err := cookiejar.New(nil)
	if err != nil {
		// cookiejar.New only fails on a bad PublicSuffixList, and nil is
		// always valid. Unreachable, but the error is not the caller's to
		// guess about, so it is reported rather than dropped.
		panic("download: cookiejar.New: " + err.Error())
	}
	return &qBittorrent{
		base: strings.TrimRight(base, "/"),
		user: user,
		pass: pass,
		hc: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
	}
}

func (q *qBittorrent) Name() string { return "qBittorrent" }

// Add starts a download from a magnet link into dir.
//
// savepath is qBittorrent's per-torrent download directory, which is the
// whole reason kishizu can use it: without it files land in the client's
// default and the reconciler never sees them.
func (q *qBittorrent) Add(ctx context.Context, magnet, dir string) error {
	if err := q.login(ctx); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("urls", magnet)
	form.Set("savepath", dir)
	// Not auto-managed: kishizu decides what to grab, so the client must not
	// also apply its own rules about where things go.
	form.Set("autoTMM", "false")

	resp, err := q.postForm(ctx, q.base+"/api/v2/torrents/add", form)
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
func (q *qBittorrent) login(ctx context.Context) error {
	form := url.Values{}
	form.Set("username", q.user)
	form.Set("password", q.pass)
	resp, err := q.postForm(ctx, q.base+"/api/v2/auth/login", form)
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

// postForm posts a form with the context attached, so a cancelled or expired
// context abandons the request rather than letting it run to the client's
// 30-second timeout.
func (q *qBittorrent) postForm(ctx context.Context, url string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return q.hc.Do(req)
}
