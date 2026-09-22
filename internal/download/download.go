// Package download defines what kishizu needs from a BitTorrent client, and
// provides the clients it knows how to talk to.
//
// kishizu is a coordinator, not a BitTorrent client: it decides WHAT to grab
// and leaves the grabbing to something else. That split is why this is an
// interface rather than a concrete type — the choice of client is a
// deployment decision, not a design one.
package download

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Downloader hands a torrent to a BitTorrent client.
//
// There is deliberately no way to query progress, list torrents or remove
// them. kishizu reconciles completion by scanning the staging directory, not
// by asking the client, because the client's view is not authoritative: a
// done-script may already have removed the torrent, and a torrent the client
// reports may not have produced the file kishizu is waiting for.
type Downloader interface {
	// Add starts a download from a magnet link, into dir.
	//
	// dir is per-show, and is the only reason this is not just Add(magnet).
	// kishizu needs to know where a completed file will land so it can
	// reconcile it, and it needs each show's files kept apart so a file's
	// show can be read from its location rather than guessed from its name.
	//
	// An implementation MUST honour dir. One that ignores it and downloads
	// to its own default will leave files where kishizu never looks, and
	// every episode will sit in "downloading" forever.
	//
	// ctx bounds the request. It is a parameter rather than a field on the
	// client because the deadline belongs to the caller's operation — a
	// shutdown mid-add should abandon that add, not every future one.
	Add(ctx context.Context, magnet, dir string) error

	// Name is the client's name, for logs and error messages.
	Name() string
}

// Kind identifies a downloader implementation, for configuration.
type Kind string

const (
	// KindTransmission is Transmission's RPC interface.
	KindTransmission Kind = "transmission"
	// KindQBittorrent is qBittorrent's WebUI API.
	KindQBittorrent Kind = "qbittorrent"
)

// Kinds lists the downloaders that can be configured.
var Kinds = []Kind{KindTransmission, KindQBittorrent}

// ParseKind reads a downloader name, accepting the spellings a user is likely
// to write. Empty means Transmission, which is what every existing deployment
// uses, so an unset value keeps working rather than failing to start.
func ParseKind(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "transmission", "transmission-rpc":
		return KindTransmission, nil
	case "qbittorrent", "qbit", "qbt":
		return KindQBittorrent, nil
	}
	return "", fmt.Errorf("unknown downloader %q (want one of: transmission, qbittorrent)", s)
}

// Magnet builds a magnet link from an infohash and a display name.
//
// The name is kept so the torrent has something readable in the client's list;
// without it a client shows a bare hash, which makes a stuck download
// impossible to identify.
//
// The name is query-escaped because release titles are full of spaces and
// brackets, and occasionally an ampersand or a hash. Unescaped, an ampersand
// terminates dn and injects a bogus parameter, and a space makes the magnet
// malformed — clients tolerate it today, but the display name is silently
// truncated at the first separator.
func Magnet(infohash, title string) string {
	return "magnet:?xt=urn:btih:" + url.QueryEscape(infohash) + "&dn=" + url.QueryEscape(title)
}
