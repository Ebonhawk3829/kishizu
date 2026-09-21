package main

import (
	"flag"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/config"
)

// flagOverrides carries the flag values that can override the config file.
type flagOverrides struct {
	library     string
	staging     string
	keep        int
	interval    time.Duration
	dryRun      bool
	rpc         string
	downloader  string
	qbitURL     string
	qbitUser    string
	qbitPass    string
	ntfyURL     string
	notifier    string
	gotifyURL   string
	gotifyToken string
}

// applyFlagOverrides lets an explicitly-set flag win over the config file.
//
// Only flags the user actually typed are applied. Every flag has a default, so
// applying them unconditionally would let a default silently overwrite a
// configured value — the config file would appear to be ignored, which is
// worse than either mechanism being broken on its own.
//
// This is why the check is flag.Visit rather than a comparison against the
// default: a user who genuinely wants the default value has still "set" the
// flag, and that intent must win.
func applyFlagOverrides(cfg *config.File, o flagOverrides) {
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	s := cfg.Server
	if set["library"] && o.library != "" {
		s.Library = o.library
	}
	if set["staging"] && o.staging != "" {
		s.Staging = o.staging
	}
	if set["keep"] {
		s.Keep = &o.keep
	}
	if set["interval"] && o.interval > 0 {
		s.Interval = o.interval.String()
	}
	if set["dry-run"] {
		s.DryRun = &o.dryRun
	}
	if set["downloader"] && o.downloader != "" {
		s.Downloader.Kind = o.downloader
	}
	if set["transmission"] && o.rpc != "" {
		s.Downloader.TransmissionRPC = o.rpc
	}
	if set["qbittorrent"] && o.qbitURL != "" {
		s.Downloader.QBittorrentURL = o.qbitURL
	}
	if set["qbittorrent-user"] && o.qbitUser != "" {
		s.Downloader.QBittorrentUser = o.qbitUser
	}
	if set["qbittorrent-pass"] && o.qbitPass != "" {
		s.Downloader.QBittorrentPass = o.qbitPass
	}
	if set["notifier"] && o.notifier != "" {
		s.Notifier.Kind = o.notifier
	}
	if set["ntfy"] && o.ntfyURL != "" {
		s.Notifier.NtfyTopic = o.ntfyURL
	}
	if set["gotify"] && o.gotifyURL != "" {
		s.Notifier.GotifyURL = o.gotifyURL
	}
	if set["gotify-token"] && o.gotifyToken != "" {
		s.Notifier.GotifyToken = o.gotifyToken
	}
}
