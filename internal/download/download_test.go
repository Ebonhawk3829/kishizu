package download

import (
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
	if !strings.Contains(got, "dn=Show - 01") {
		t.Errorf("magnet = %q, want it to carry the display name", got)
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
	if err := q.Add("magnet:?xt=urn:btih:ABC", "/downloads/anime/Show"); err != nil {
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
	if err := q.Add("magnet:?xt=urn:btih:ABC", "/x"); err == nil {
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
	_ = f.Add("m1", "/a")
	_ = f.Add("m2", "/b")
	if f.Count() != 2 {
		t.Errorf("Count = %d, want 2", f.Count())
	}
	if f.Last().Magnet != "m2" || f.Last().Dir != "/b" {
		t.Errorf("Last = %+v, want m2//b", f.Last())
	}
}
