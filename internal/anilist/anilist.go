// Package anilist reads read-only metadata from AniList's public GraphQL API.
//
// It exists for one purpose: cover art for a season adopted from SeaDex. An
// adopted show never touches animeschedule.net, so it has no art from the
// usual path, and SeaDex's own API does not expose an image. But a SeaDex
// entry URL carries the AniList id, and AniList serves the same poster the
// SeaDex page displays.
//
// This is a lookup at adoption time, not a runtime dependency: nothing here
// is consulted while kishizu is running, and a failure only costs a missing
// poster.
package anilist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Endpoint is AniList's public GraphQL API. No authentication is required for
// queries.
//
// A variable rather than a constant so tests can point it at a stub.
var Endpoint = "https://graphql.anilist.co"

// Media is the slice of an AniList media record kishizu needs.
type Media struct {
	// ID is the AniList media id, which is also a SeaDex entry's URL path.
	ID int
	// CoverURL is the large cover image. Empty when AniList has none.
	CoverURL string
}

// Client queries AniList.
type Client struct {
	hc *http.Client
}

// New builds a client.
func New() *Client {
	return &Client{hc: &http.Client{Timeout: 20 * time.Second}}
}

// FetchMedia retrieves one media record by AniList id.
//
// Returns (nil, nil) when AniList has no such id, so callers can treat a miss
// as "no art available" rather than an error worth failing an adoption over.
func (c *Client) FetchMedia(id int) (*Media, error) {
	if id <= 0 {
		return nil, fmt.Errorf("anilist: invalid media id %d", id)
	}
	query := `query ($id: Int) {
		Media(id: $id, type: ANIME) {
			id
			coverImage { large }
		}
	}`
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"id": id},
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.hc.Post(Endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anilist returned %d", resp.StatusCode)
	}
	return ParseMedia(resp.Body)
}

// mediaResponse is the wire shape of a Media query.
type mediaResponse struct {
	Data struct {
		Media *struct {
			ID         int `json:"id"`
			CoverImage struct {
				Large string `json:"large"`
			} `json:"coverImage"`
		} `json:"Media"`
	} `json:"data"`
}

// ParseMedia decodes a GraphQL response.
//
// Returns (nil, nil) when the response contains no Media, which is how AniList
// reports an unknown id.
func ParseMedia(r io.Reader) (*Media, error) {
	var out mediaResponse
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, fmt.Errorf("anilist: decode: %w", err)
	}
	m := out.Data.Media
	if m == nil {
		return nil, nil
	}
	return &Media{ID: m.ID, CoverURL: m.CoverImage.Large}, nil
}
