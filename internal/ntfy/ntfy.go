// Package ntfy sends notifications to an ntfy server.
//
// Used for the events worth knowing about without opening the UI: a download
// finished, an episode was watched and deleted, or the pipeline failed
// repeatedly. Single events are logged; only persistent failures alert, since
// a Nyaa blip should not ping the user's phone.
package ntfy

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client posts to an ntfy topic.
type Client struct {
	url string
	hc  *http.Client
}

// New builds a client. url is the full topic URL, e.g.
// http://100.64.0.1:8085/kishizu
func New(url string) *Client {
	return &Client{url: url, hc: &http.Client{Timeout: 10 * time.Second}}
}

// Priority levels as ntfy defines them.
const (
	PriorityMin     = 1
	PriorityLow     = 2
	PriorityDefault = 3
	PriorityHigh    = 4
	PriorityUrgent  = 5
)

// Send posts a message with a title and priority. Best-effort: errors are
// returned but callers decide whether they matter.
func (c *Client) Send(title, message string, priority int) error {
	req, err := http.NewRequest(http.MethodPost, c.url, strings.NewReader(message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", fmt.Sprint(priority))
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("ntfy %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
