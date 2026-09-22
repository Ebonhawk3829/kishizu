// Package transmission hands magnet links to the Transmission RPC.
//
// kishizu is a coordinator, not a BitTorrent client: it decides WHAT to grab,
// Transmission does the grabbing. On completion the existing
// configs/transmission/done-remove.sh removes the torrent, leaving the file on
// disk — download without seeding, already proven in this stack.
package transmission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a Transmission RPC endpoint.
type Client struct {
	url string
	hc  *http.Client
	// session is cached after the first 409 round trip.
	session string
}

// New builds a client. url is the RPC endpoint, e.g.
// http://transmission:9091/transmission/rpc
func New(url string) *Client {
	return &Client{url: url, hc: &http.Client{Timeout: 30 * time.Second}}
}

// URL is the RPC endpoint this client was built with. Transmission serves
// its web UI from the same address, so this is also where a user goes to
// look at a download directly.
func (c *Client) URL() string { return c.url }

// call performs one RPC request, handling the CSRF session token.
//
// Transmission answers the first unauthenticated request with 409 and a fresh
// X-Transmission-Session-Id; the token must then accompany every call. This is
// the documented handshake, so it is handled here rather than by callers.
func (c *Client) call(ctx context.Context, method string, args any, out any) error {
	body, err := json.Marshal(map[string]any{"method": method, "arguments": args})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.session != "" {
			req.Header.Set("X-Transmission-Session-Id", c.session)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusConflict {
			// Fresh token; retry once with it.
			c.session = resp.Header.Get("X-Transmission-Session-Id")
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
			resp.Body.Close()
			return fmt.Errorf("%s: %d %s", method, resp.StatusCode, strings.TrimSpace(string(b)))
		}
		defer resp.Body.Close()
		var envelope struct {
			Result    string          `json:"result"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			return err
		}
		if envelope.Result != "success" {
			return fmt.Errorf("%s: %s", method, envelope.Result)
		}
		if out != nil {
			return json.Unmarshal(envelope.Arguments, out)
		}
		return nil
	}
	return fmt.Errorf("%s: could not obtain a session token", method)
}

// AddWithDir is Add with an explicit download directory, for tests and for a
// future per-show folder layout.
func (c *Client) AddWithDir(ctx context.Context, magnet, dir string) error {
	return c.call(ctx, "torrent-add", map[string]any{
		"filename":     magnet,
		"download-dir": dir,
	}, nil)
}
