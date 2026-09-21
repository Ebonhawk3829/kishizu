// Package notify defines what kishizu needs from a notification service, and
// provides the services it knows how to post to.
//
// Notifications are best-effort throughout: a missed ping must never stop a
// download. But a failure has to be visible — discarding the error is how a
// wrong topic URL went unnoticed, with every ping failing silently and the
// only symptom being that nothing arrived.
package notify

import (
	"fmt"
	"strings"
)

// Priority is how urgent a notification is.
//
// Deliberately not ntfy's integer levels. Those are one service's vocabulary,
// and a caller that has to know them cannot be pointed at a different
// service. Each backend maps these to whatever it supports; one that has no
// notion of priority ignores the field.
type Priority int

const (
	// PriorityLow is routine: an episode started downloading.
	PriorityLow Priority = iota
	// PriorityDefault is worth knowing but not urgent: episodes were deleted.
	PriorityDefault
	// PriorityHigh needs attention: the downloader is unreachable, or a file
	// vanished before its watch signal.
	PriorityHigh
)

// Notifier sends a user-visible alert.
type Notifier interface {
	// Send posts a message. Errors are returned but callers decide whether
	// they matter; a notification is never worth failing a download over.
	Send(title, message string, priority Priority) error

	// Name is the service's name, for logs.
	Name() string
}

// Kind identifies a notification backend, for configuration.
type Kind string

const (
	// KindNtfy is an ntfy server, self-hosted or public.
	KindNtfy Kind = "ntfy"
	// KindGotify is a Gotify server.
	KindGotify Kind = "gotify"
	// KindNone disables notifications. Explicit rather than an empty URL, so
	// "I chose not to be notified" is distinguishable from "I forgot".
	KindNone Kind = "none"
)

// Kinds lists the notification backends that can be configured.
var Kinds = []Kind{KindNtfy, KindGotify, KindNone}

// ParseKind reads a backend name. Empty means none, which is the current
// default: notifications are opt-in, and an unset value must not start
// posting somewhere the user did not choose.
func ParseKind(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none", "off", "disabled":
		return KindNone, nil
	case "ntfy":
		return KindNtfy, nil
	case "gotify":
		return KindGotify, nil
	}
	return "", fmt.Errorf("unknown notifier %q (want one of: ntfy, gotify, none)", s)
}

// Nop is a Notifier that discards everything.
//
// Used when notifications are disabled, so callers can be written without a
// nil check at every site. A nil Notifier is a panic waiting to happen; this
// is the same thing without the risk.
type Nop struct{}

func (Nop) Send(title, message string, priority Priority) error { return nil }
func (Nop) Name() string                                        { return "none" }
