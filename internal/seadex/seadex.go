// Package seadex reads release metadata from releases.moe (SeaDex).
//
// SeaDex is a community index of the highest-quality release for a given
// anime, chosen by image and subtitle comparisons. It only lists completed
// shows, which makes it the counterpart to kishizu's airing pipeline: where
// the schedule drives weekly hunting, SeaDex answers "this season is over,
// what is the definitive version of it".
//
// This package is a read-only client. It is used once, at adoption time, and
// nothing at runtime depends on it.
package seadex

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// URL is the site root. Entry pages are <URL>/<alID>/, where alID is the
// AniList media id.
const URL = "https://releases.moe"

// API is the PocketBase REST root.
const API = URL + "/api/collections"

// Entry is one SeaDex record: a finished show and the torrents judged best
// for it.
type Entry struct {
	// AniListID is the AniList media id, and the entry page's URL path.
	AniListID int
	// ID is SeaDex's own record id. Not the same as the URL path.
	ID string
	// Incomplete is true when the listed release does not contain every
	// episode, because a better source exists for some of them.
	Incomplete bool
	// TheoreticalBest is set when the best release does not exist yet and
	// someone would have to mux it. Empty means a real release is listed.
	TheoreticalBest string
	// Notes is the editors' free-text explanation for the choice.
	Notes string
	// Torrents are the candidate releases, best and alt, across trackers.
	Torrents []Torrent
}

// Torrent is one candidate release for an entry.
type Torrent struct {
	InfoHash     string
	URL          string
	Tracker      string
	ReleaseGroup string
	IsBest       bool
	DualAudio    bool
	Tags         []string
	Files        []File
}

// File is one file inside a torrent.
type File struct {
	Name   string
	Length int64
}

// ---------- URL parsing ----------

var reEntryPath = regexp.MustCompile(`^/(\d+)/?$`)

// AniListIDFromURL extracts the AniList id from a SeaDex entry URL.
//
// The entry page's path IS the AniList id, so no lookup is needed to get
// from a pasted link to an API query. Accepts the forms a user is likely to
// paste:
//
//	https://releases.moe/112124/
//	releases.moe/112124
//	/112124
//	112124
//
// Returns 0 when there is no id to be found.
func AniListIDFromURL(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	// Drop query and fragment.
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	// Strip scheme and host if present.
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			s = rest[j:]
		} else {
			s = ""
		}
	} else if strings.HasPrefix(s, "releases.moe/") {
		s = s[len("releases.moe/"):]
		if !strings.HasPrefix(s, "/") {
			s = "/" + s
		}
	}
	if m := reEntryPath.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return 0
		}
		return n
	}
	// A bare number is also accepted.
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return 0
}

// ---------- API ----------

// Client talks to the SeaDex API.
type Client struct {
	hc *http.Client
}

// New builds a client.
func New() *Client {
	return &Client{hc: &http.Client{Timeout: 30 * time.Second}}
}

// FetchEntry retrieves one entry by AniList id, with its torrents expanded.
//
// Returns nil when no entry exists for that id, which is the normal case for
// a show SeaDex has not covered (it only lists finished seasons).
func (c *Client) FetchEntry(anilistID int) (*Entry, error) {
	if anilistID <= 0 {
		return nil, fmt.Errorf("seadex: invalid AniList id %d", anilistID)
	}
	u := fmt.Sprintf("%s/entries/records?filter=alID=%d&expand=trs&perPage=1", API, anilistID)
	resp, err := c.hc.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("seadex returned %d", resp.StatusCode)
	}
	return ParseEntry(resp.Body)
}

// ---------- wire types ----------

type rawList struct {
	TotalItems int        `json:"totalItems"`
	Items      []rawEntry `json:"items"`
}

type rawEntry struct {
	ID              string     `json:"id"`
	AniListID       int        `json:"alID"`
	Incomplete      bool       `json:"incomplete"`
	TheoreticalBest string     `json:"theoreticalBest"`
	Notes           string     `json:"notes"`
	Expand          *rawExpand `json:"expand"`
}

type rawExpand struct {
	Torrents []rawTorrent `json:"trs"`
}

type rawTorrent struct {
	InfoHash     string    `json:"infoHash"`
	URL          string    `json:"url"`
	Tracker      string    `json:"tracker"`
	ReleaseGroup string    `json:"releaseGroup"`
	IsBest       bool      `json:"isBest"`
	DualAudio    bool      `json:"dualAudio"`
	Tags         []string  `json:"tags"`
	Files        []rawFile `json:"files"`
}

type rawFile struct {
	Name   string `json:"name"`
	Length int64  `json:"length"`
}

// ParseEntry decodes an API response into an Entry.
//
// Returns (nil, nil) when the response contains no items, so callers can
// treat "SeaDex has not covered this show" as a normal outcome rather than
// an error.
func ParseEntry(r io.Reader) (*Entry, error) {
	var list rawList
	if err := json.NewDecoder(r).Decode(&list); err != nil {
		return nil, fmt.Errorf("seadex: decode: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	re := list.Items[0]

	e := &Entry{
		AniListID:       re.AniListID,
		ID:              re.ID,
		Incomplete:      re.Incomplete,
		TheoreticalBest: re.TheoreticalBest,
		Notes:           re.Notes,
	}
	if re.Expand != nil {
		for _, rt := range re.Expand.Torrents {
			t := Torrent{
				InfoHash:     rt.InfoHash,
				URL:          rt.URL,
				Tracker:      rt.Tracker,
				ReleaseGroup: rt.ReleaseGroup,
				IsBest:       rt.IsBest,
				DualAudio:    rt.DualAudio,
				Tags:         rt.Tags,
			}
			for _, rf := range rt.Files {
				t.Files = append(t.Files, File{Name: rf.Name, Length: rf.Length})
			}
			e.Torrents = append(e.Torrents, t)
		}
	}
	return e, nil
}

// BestNyaa returns the torrents flagged best that are on Nyaa, which are the
// ones kishizu can actually fetch. SeaDex also lists private-tracker
// releases that are unreachable without an account.
func (e *Entry) BestNyaa() []Torrent {
	var out []Torrent
	for _, t := range e.Torrents {
		if t.IsBest && strings.EqualFold(t.Tracker, "nyaa") && t.InfoHash != "" {
			out = append(out, t)
		}
	}
	return out
}
