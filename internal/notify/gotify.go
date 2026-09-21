package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// gotifyNotifier posts to a Gotify server.
//
// Gotify authenticates with an app token rather than a URL, so the token is
// part of the configuration and must be kept out of logs.
type gotifyNotifier struct {
	url   string
	token string
	hc    *http.Client
}

// NewGotify builds a notifier for a Gotify server. url is the server root,
// e.g. https://gotify.example.com; token is an app token.
func NewGotify(url, token string) Notifier {
	return &gotifyNotifier{
		url:   strings.TrimRight(url, "/"),
		token: token,
		hc:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (g *gotifyNotifier) Name() string { return "gotify" }

// gotifyLevels maps kishizu's priorities onto Gotify's 0-10 scale.
var gotifyLevels = map[Priority]int{
	PriorityLow:     1,
	PriorityDefault: 4,
	PriorityHigh:    8,
}

func (g *gotifyNotifier) Send(title, message string, priority Priority) error {
	body, err := json.Marshal(map[string]any{
		"title":    title,
		"message":  message,
		"priority": gotifyLevels[priority],
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, g.url+"/message", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", g.token)
	resp, err := g.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("gotify %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
