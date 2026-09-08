// Package anilist provides a one-time import of a user's list, used to
// bootstrap the database.
//
// This is deliberately NOT a runtime dependency. kishizu must work with AniList
// permanently unreachable — that is the entire reason it exists. The import runs
// once, on demand, and everything it produces is stored locally.
package anilist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const Endpoint = "https://graphql.anilist.co"

// Entry is one anime on the user's list, in the shape the importer needs.
type Entry struct {
	MediaID  int
	Title    string // preferred title (english, else romaji)
	Romaji   string
	Native   string
	Synonyms []string
	Status   string // CURRENT | PLANNING | COMPLETED | REPEATING | PAUSED | DROPPED
	Progress int    // episodes watched
	Episodes int    // total episodes, 0 when unknown
	Format   string // TV | MOVIE | OVA | ...
}

// Client talks to the AniList GraphQL API.
type Client struct {
	HTTP     *http.Client
	Endpoint string
}

func NewClient() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		Endpoint: Endpoint,
	}
}

// listQuery pulls everything the importer needs in one request per page.
//
// mediaList is keyed by userName and filtered by status_in, so a single call can
// fetch CURRENT + PLANNING together. Titles come in all three romanisations
// because release groups use any of them.
const listQuery = `
query ($userName: String, $statuses: [MediaListStatus], $page: Int) {
  Page (page: $page, perPage: 50) {
    pageInfo { hasNextPage currentPage }
    mediaList (userName: $userName, type: ANIME, status_in: $statuses) {
      status
      progress
      media {
        id
        episodes
        format
        title { romaji english native }
        synonyms
      }
    }
  }
}`

type listResponse struct {
	Data struct {
		Page struct {
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
				CurrentPage int  `json:"currentPage"`
			} `json:"pageInfo"`
			MediaList []struct {
				Status   string `json:"status"`
				Progress int    `json:"progress"`
				Media    struct {
					ID       int    `json:"id"`
					Episodes int    `json:"episodes"`
					Format   string `json:"format"`
					Title    struct {
						Romaji  string `json:"romaji"`
						English string `json:"english"`
						Native  string `json:"native"`
					} `json:"title"`
					Synonyms []string `json:"synonyms"`
				} `json:"media"`
			} `json:"mediaList"`
		} `json:"Page"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
		Status  int    `json:"status"`
	} `json:"errors"`
}

// FetchList retrieves every entry with one of the given statuses.
//
// Statuses follow AniList's vocabulary: CURRENT, PLANNING, COMPLETED, REPEATING,
// PAUSED, DROPPED.
func (c *Client) FetchList(username string, statuses []string) ([]Entry, error) {
	if username == "" {
		return nil, fmt.Errorf("username required")
	}
	if len(statuses) == 0 {
		statuses = []string{"CURRENT", "PLANNING"}
	}

	var out []Entry
	for page := 1; ; page++ {
		vars := map[string]any{
			"userName": username,
			"statuses": statuses,
			"page":     page,
		}
		var resp listResponse
		if err := c.post(listQuery, vars, &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			e := resp.Errors[0]
			return nil, fmt.Errorf("anilist error %d: %s", e.Status, e.Message)
		}

		for _, ml := range resp.Data.Page.MediaList {
			m := ml.Media
			title := m.Title.English
			if title == "" {
				title = m.Title.Romaji
			}
			out = append(out, Entry{
				MediaID:  m.ID,
				Title:    title,
				Romaji:   m.Title.Romaji,
				Native:   m.Title.Native,
				Synonyms: m.Synonyms,
				Status:   ml.Status,
				Progress: ml.Progress,
				Episodes: m.Episodes,
				Format:   m.Format,
			})
		}

		if !resp.Data.Page.PageInfo.HasNextPage {
			break
		}
		// AniList allows 30 req/min; be polite across pages.
		time.Sleep(300 * time.Millisecond)
	}
	return out, nil
}

func (c *Client) post(query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	// A 200 can still carry an error envelope; the caller inspects Errors.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anilist returned %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	return json.Unmarshal(raw, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
