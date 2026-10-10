// Package notify defines what kishizu needs from a notification service, and
// provides the services it knows how to post to.
//
// Notifications are best-effort throughout: a missed ping must never stop a
// download. A failure still has to be visible — a silently discarded error
// hides a wrong topic URL, and the only symptom is that nothing arrives.
package notify

import (
	"fmt"
	"strings"
)

// Priority is how urgent a notification is.
//
// These are kishizu's own levels, so a caller never has to know one
// service's vocabulary and can be pointed at any backend. Each backend maps
// them to whatever it supports; one that has no notion of priority ignores
// the field.
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
