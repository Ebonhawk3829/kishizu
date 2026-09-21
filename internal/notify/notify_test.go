package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseKindDefaultsToNone: notifications are opt-in, so an unset value
// must not start posting somewhere the user did not choose.
func TestParseKindDefaultsToNone(t *testing.T) {
	cases := map[string]Kind{
		"":         KindNone,
		"none":     KindNone,
		"off":      KindNone,
		"ntfy":     KindNtfy,
		"Ntfy":     KindNtfy,
		"gotify":   KindGotify,
		" gotify ": KindGotify,
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
	if _, err := ParseKind("pushover"); err == nil {
		t.Error("an unknown notifier must be an error, not a silent default")
	}
}

// TestNtfySendsTitleAndPriority: ntfy takes the title and priority as headers,
// not in the body.
func TestNtfySendsTitleAndPriority(t *testing.T) {
	var title, priority, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title = r.Header.Get("Title")
		priority = r.Header.Get("Priority")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()

	n := NewNtfy(srv.URL)
	if err := n.Send("kishizu: downloading", "Show ep1", PriorityLow); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if title != "kishizu: downloading" {
		t.Errorf("title = %q", title)
	}
	if body != "Show ep1" {
		t.Errorf("body = %q, want the message", body)
	}
	// Low maps to 2, not 1: ntfy's "min" produces no notification at all on
	// some clients, which would make a routine ping invisible.
	if priority != "2" {
		t.Errorf("priority = %q, want 2", priority)
	}
}

// TestNtfyReportsFailure: a wrong topic URL must surface as an error.
// Discarding it is how a misconfiguration went unnoticed, with every ping
// failing silently and the only symptom being that nothing arrived.
func TestNtfyReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := NewNtfy(srv.URL).Send("t", "m", PriorityDefault); err == nil {
		t.Error("expected an error for a failed send")
	}
}

// TestGotifySendsTokenAndPriority: Gotify authenticates with an app token in a
// header, and takes priority in the JSON body.
func TestGotifySendsTokenAndPriority(t *testing.T) {
	var token string
	var payload struct {
		Title    string `json:"title"`
		Message  string `json:"message"`
		Priority int    `json:"priority"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("X-Gotify-Key")
		json.NewDecoder(r.Body).Decode(&payload)
	}))
	defer srv.Close()

	g := NewGotify(srv.URL, "TOKEN123")
	if err := g.Send("kishizu: file missing", "1 vanished", PriorityHigh); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if token != "TOKEN123" {
		t.Errorf("token = %q", token)
	}
	if payload.Title != "kishizu: file missing" || payload.Message != "1 vanished" {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Priority == 0 {
		t.Error("priority must be set; 0 means it was dropped")
	}
}

// TestNopDiscards: the no-op notifier lets callers skip nil checks without
// risking a panic on a nil interface.
func TestNopDiscards(t *testing.T) {
	var n Notifier = Nop{}
	if err := n.Send("t", "m", PriorityHigh); err != nil {
		t.Errorf("Nop.Send returned %v", err)
	}
	if n.Name() != "none" {
		t.Errorf("Name = %q, want none", n.Name())
	}
}

// TestPrioritiesAreOrdered: the enum is compared by callers, so the order is
// part of the contract.
func TestPrioritiesAreOrdered(t *testing.T) {
	if PriorityLow >= PriorityDefault || PriorityDefault >= PriorityHigh {
		t.Error("priorities must ascend: low < default < high")
	}
	// Every backend must map every priority, or a notification silently
	// loses its urgency.
	for _, p := range []Priority{PriorityLow, PriorityDefault, PriorityHigh} {
		if _, ok := ntfyLevels[p]; !ok {
			t.Errorf("ntfy has no level for priority %d", p)
		}
		if _, ok := gotifyLevels[p]; !ok {
			t.Errorf("gotify has no level for priority %d", p)
		}
	}
}

// TestNamesAreSet: the name appears in log lines, so an empty one makes a
// failure unattributable.
func TestNamesAreSet(t *testing.T) {
	for _, n := range []Notifier{NewNtfy("http://x"), NewGotify("http://x", "t"), Nop{}} {
		if strings.TrimSpace(n.Name()) == "" {
			t.Errorf("%T has an empty name", n)
		}
	}
}
