package notify

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ntfyNotifier posts to an ntfy topic.
type ntfyNotifier struct {
	url string
	hc  *http.Client
}

// NewNtfy builds a notifier for an ntfy topic URL, e.g.
// https://ntfy.sh/kishizu or http://host:8085/kishizu.
func NewNtfy(url string) Notifier {
	return &ntfyNotifier{url: url, hc: &http.Client{Timeout: 10 * time.Second}}
}

func (n *ntfyNotifier) Name() string { return "ntfy" }

// ntfyLevels maps kishizu's priorities onto ntfy's 1-5 scale.
//
// ntfy has five levels and kishizu has three, so the mapping is not one to
// one. Low maps to 2 rather than 1 because 1 ("min") does not produce a
// notification on some clients at all, which would make a routine ping
// silently invisible.
var ntfyLevels = map[Priority]int{
	PriorityLow:     2,
	PriorityDefault: 3,
	PriorityHigh:    4,
}

func (n *ntfyNotifier) Send(title, message string, priority Priority) error {
	req, err := http.NewRequest(http.MethodPost, n.url, strings.NewReader(message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", fmt.Sprint(ntfyLevels[priority]))
	resp, err := n.hc.Do(req)
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
