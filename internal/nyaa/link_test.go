package nyaa

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTitleFromPagePrefersOgTitle: the og:title meta tag is the release name
// without the site's suffix. The <title> tag is the fallback, and needs
// ":: Nyaa" stripped.
func TestTitleFromPagePrefersOgTitle(t *testing.T) {
	cases := []struct {
		name string
		page string
		want string
	}{
		{
			name: "og:title in property-then-content order",
			page: `<meta property="og:title" content="[VARYG] Show - 07 [1080p]">`,
			want: "[VARYG] Show - 07 [1080p]",
		},
		{
			name: "og:title in content-then-property order",
			page: `<meta content="[VARYG] Show - 07 [1080p]" property="og:title">`,
			want: "[VARYG] Show - 07 [1080p]",
		},
		{
			name: "falls back to the title tag, suffix stripped",
			page: `<title>[VARYG] Show - 07 [1080p] :: Nyaa</title>`,
			want: "[VARYG] Show - 07 [1080p]",
		},
		{
			name: "HTML entities are unescaped",
			page: `<meta property="og:title" content="Show &amp; Friends - 01">`,
			want: "Show & Friends - 01",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := TitleFromPage(c.page)
			if err != nil {
				t.Fatalf("TitleFromPage: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestTitleFromPageReportsNoTitle: a page with no title must be an error.
// Returning "" would look like a release with an empty name, which would
// then fail to match in a confusing way.
func TestTitleFromPageReportsNoTitle(t *testing.T) {
	if _, err := TitleFromPage("<html><body>nothing</body></html>"); err == nil {
		t.Error("expected an error when no title is present")
	}
}

// TestResolveLink: the user pastes a link instead of transcribing a title,
// which is the difference between grading a release being worth it and not.
func TestResolveLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<meta property="og:title" content="[VARYG] Show - 07 [1080p]">`))
	}))
	defer srv.Close()

	got, err := ResolveLink(nil, srv.URL)
	if err != nil {
		t.Fatalf("ResolveLink: %v", err)
	}
	if got != "[VARYG] Show - 07 [1080p]" {
		t.Errorf("got %q", got)
	}
}

// TestResolveLinkReportsNonOK: a 404 must be an error, not an empty title.
func TestResolveLinkReportsNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := ResolveLink(nil, srv.URL); err == nil {
		t.Error("expected an error for a non-200 response")
	}
}

// TestIsLink: distinguishes a pasted URL from a typed title, so the UI knows
// whether to fetch or to parse.
func TestIsLink(t *testing.T) {
	cases := map[string]bool{
		"https://nyaa.si/view/1": true,
		"http://nyaa.si/view/1":  true,
		"[VARYG] Show - 07":      false,
		"nyaa.si/view/1":         false, // no scheme: not a link
		"":                       false,
	}
	for in, want := range cases {
		if got := IsLink(in); got != want {
			t.Errorf("IsLink(%q) = %v, want %v", in, got, want)
		}
	}
}
