package nyaa

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var (
	reOgTitle     = regexp.MustCompile(`property=["']og:title["']\s+content=["']([^"']+)["']`)
	reOgTitleRev  = regexp.MustCompile(`content=["']([^"']+)["']\s+property=["']og:title["']`)
	reTitleTag    = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	reViewURL     = regexp.MustCompile(`nyaa\.si/view/(\d+)`)
	reInfoHashURL = regexp.MustCompile(`(?i)\b([0-9a-f]{40})\b`)
)

// ResolveLink fetches a Nyaa view page and returns the release title.
//
// Lets the user paste a link instead of transcribing a title by hand, which is
// the difference between grading a release being worth it and not.
func ResolveLink(client *http.Client, rawURL string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nyaa returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return TitleFromPage(string(body))
}

// TitleFromPage extracts the release title from a Nyaa view page.
// Split out from ResolveLink so it can be tested against saved pages.
func TitleFromPage(page string) (string, error) {
	if m := reOgTitle.FindStringSubmatch(page); m != nil {
		return cleanTitle(m[1]), nil
	}
	if m := reOgTitleRev.FindStringSubmatch(page); m != nil {
		return cleanTitle(m[1]), nil
	}
	if m := reTitleTag.FindStringSubmatch(page); m != nil {
		return cleanTitle(m[1]), nil
	}
	return "", fmt.Errorf("no title found on page")
}

func cleanTitle(s string) string {
	s = html.UnescapeString(s)
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ":: Nyaa")
	return strings.TrimSpace(s)
}

// IsLink reports whether the input looks like a Nyaa URL rather than a title.
func IsLink(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
