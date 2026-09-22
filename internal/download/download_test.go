package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestParseKindAcceptsCommonSpellings: the downloader is named in config, so
// the spellings a user is likely to write must all resolve. An empty value
// means Transmission, because that is what every existing deployment uses —
// an unset value must keep working rather than fail to start.
func TestParseKindAcceptsCommonSpellings(t *testing.T) {
	cases := map[string]Kind{
		"":              KindTransmission,
		"transmission":  KindTransmission,
		"Transmission":  KindTransmission,
		"qbittorrent":   KindQBittorrent,
		"qBittorrent":   KindQBittorrent,
		"qbit":          KindQBittorrent,
		" qbittorrent ": KindQBittorrent,
	}
	for in, want := range cases {
		got, err := ParseKind(in)
		if err != nil {
			t.Errorf("ParseKind(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseKind(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseKind("deluge"); err == nil {
		t.Error("an unknown downloader must be an error, not a silent default")
	}
}

// TestMagnetKeepsDisplayName: without a name the client shows a bare hash,
// which makes a stuck download impossible to identify.
func TestMagnetKeepsDisplayName(t *testing.T) {
	got := Magnet("ABC123", "Show - 01")
	if !strings.HasPrefix(got, "magnet:?xt=urn:btih:ABC123") {
		t.Errorf("magnet = %q, want it to start with the infohash", got)
	}
	// Read the name back the way a client would, rather than matching the
	// raw string: the name is escaped, so the literal is not what a client
	// sees.
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("magnet %q does not parse: %v", got, err)
	}
	if dn := u.Query().Get("dn"); dn != "Show - 01" {
		t.Errorf("dn = %q, want %q", dn, "Show - 01")
	}
}

// TestMagnetEscapesTheDisplayName: a release title is full of characters that
// are structural in a URI. Unescaped, an ampersand terminates dn and injects
// a bogus parameter, and a space leaves the magnet malformed.
func TestMagnetEscapesTheDisplayName(t *testing.T) {
	cases := []struct {
		name  string
		title string
	}{
		{"spaces", "[Group] Show - 01 (1080p)"},
		{"ampersand", "Show & Friends - 01"},
		{"hash", "Show #01"},
		{"question mark", "Show? - 01"},
		{"equals", "Show - 01 (a=b)"},
	}
	for _, tc := range cases {
		got := Magnet("ABC123", tc.title)
		u, err := url.Parse(got)
		if err != nil {
			t.Errorf("%s: magnet %q does not parse: %v", tc.name, got, err)
			continue
		}
		if got, want := u.Query().Get("dn"), tc.title; got != want {
			t.Errorf("%s: dn = %q, want %q (magnet %q)", tc.name, got, want, u)
		}
		if got, want := u.Query().Get("xt"), "urn:btih:ABC123"; got != want {
			t.Errorf("%s: xt = %q, want %q", tc.name, got, want)
		}
	}
}

// TestQBittorrentSendsSavepath: the per-torrent download directory is the one
// thing kishizu requires of a client. Without it files land in the client's
// default and the reconciler never sees them, so every episode sits in
// "downloading" forever.
func TestQBittorrentSendsSavepath(t *testing.T) {
	var got url.Values
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		r.ParseForm()
		if r.URL.Path == "/api/v2/torrents/add" {
			got = r.PostForm
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	q := NewQBittorrent(srv.URL, "u", "p")
	if err := q.Add(context.Background(), "magnet:?xt=urn:btih:ABC", "/downloads/anime/Show"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if path != "/api/v2/torrents/add" {
		t.Errorf("last path = %q, want the add endpoint", path)
	}
	if got.Get("savepath") != "/downloads/anime/Show" {
		t.Errorf("savepath = %q, want the per-show directory", got.Get("savepath"))
	}
	if got.Get("urls") != "magnet:?xt=urn:btih:ABC" {
		t.Errorf("urls = %q, want the magnet", got.Get("urls"))
	}
	// Not auto-managed: kishizu decides where things go, so the client must
	// not also apply its own rules.
	if got.Get("autoTMM") != "false" {
		t.Errorf("autoTMM = %q, want false", got.Get("autoTMM"))
	}
}

// TestQBittorrentSendsTheSessionCookie: the WebUI authenticates with a SID
// cookie from /auth/login, and Go's http.Client only retains a cookie when
// the client has a jar. Without one the add request goes out unauthenticated
// and the WebUI answers 403, which surfaces only as an add failure.
//
// This is the regression test for that: the server here records whether the
// cookie actually arrived, rather than answering 200 to everything.
func TestQBittorrentSendsTheSessionCookie(t *testing.T) {
	var gotSID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session-token", Path: "/"})
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/add":
			if c, err := r.Cookie("SID"); err == nil {
				gotSID = c.Value
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	q := NewQBittorrent(srv.URL, "u", "p")
	if err := q.Add(context.Background(), "magnet:?xt=urn:btih:ABC", "/x"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if gotSID != "session-token" {
		t.Errorf("SID on /torrents/add = %q, want %q: the login cookie was not retained",
			gotSID, "session-token")
	}
}

// TestQBittorrentReportsFailure: a failed add must be an error. Silence here
// would leave an episode marked downloading with nothing behind it.
func TestQBittorrentReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/torrents/add") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	q := NewQBittorrent(srv.URL, "", "")
	if err := q.Add(context.Background(), "magnet:?xt=urn:btih:ABC", "/x"); err == nil {
		t.Error("expected an error for a failed add")
	}
}

// TestFakeRecordsAdds: the fake is what lets other packages assert on handoff
// without standing up a client.
func TestFakeRecordsAdds(t *testing.T) {
	f := &Fake{}
	if f.Count() != 0 {
		t.Fatalf("Count = %d, want 0", f.Count())
	}
	_ = f.Add(context.Background(), "m1", "/a")
	_ = f.Add(context.Background(), "m2", "/b")
	if f.Count() != 2 {
		t.Errorf("Count = %d, want 2", f.Count())
	}
	if f.Last().Magnet != "m2" || f.Last().Dir != "/b" {
		t.Errorf("Last = %+v, want m2//b", f.Last())
	}
}
