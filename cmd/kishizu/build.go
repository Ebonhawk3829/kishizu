package main

import (
	"fmt"
	"time"

	"github.com/Ebonhawk3829/kishizu/internal/config"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/naming"
	"github.com/Ebonhawk3829/kishizu/internal/notify"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/quality"
)

// buildDownloader picks the torrent client from configuration.
//
// Transmission stays the default because it is what every existing deployment
// uses; an unset value must keep working rather than fail to start.
func buildDownloader(kind, rpcURL, qbitURL, qbitUser, qbitPass string) (download.Downloader, error) {
	k, err := download.ParseKind(kind)
	if err != nil {
		return nil, err
	}
	switch k {
	case download.KindQBittorrent:
		if qbitURL == "" {
			return nil, fmt.Errorf("-qbittorrent is required when -downloader=qbittorrent")
		}
		return download.NewQBittorrent(qbitURL, qbitUser, qbitPass), nil
	default:
		return download.NewTransmission(rpcURL), nil
	}
}

// buildNotifier picks the notification backend from configuration.
//
// Returns nil when notifications are disabled, which the caller treats as
// "no notifier" rather than installing a no-op: the distinction matters
// because a nil check is what keeps the UI from reporting a backend that
// does not exist.
func buildNotifier(kind, ntfyURL, gotifyURL, gotifyToken string) (notify.Notifier, error) {
	k, err := notify.ParseKind(kind)
	if err != nil {
		return nil, err
	}
	switch k {
	case notify.KindNone:
		return nil, nil
	case notify.KindGotify:
		if gotifyURL == "" || gotifyToken == "" {
			return nil, fmt.Errorf("-gotify and -gotify-token are required when -notifier=gotify")
		}
		return notify.NewGotify(gotifyURL, gotifyToken), nil
	default:
		// ntfy is the default, but it needs a topic URL. An empty one means
		// the user never configured it, which is not an error — it is the
		// documented way to run without notifications.
		if ntfyURL == "" {
			return nil, nil
		}
		return notify.NewNtfy(ntfyURL), nil
	}
}

// buildQuality turns the configured quality section into a policy.
//
// Only the fields that were set are applied, so a partial section keeps the
// shipped defaults. That is what makes a minimal config file work.
func buildQuality(q config.QualityConfig) *quality.Policy {
	p := quality.Default()
	if q.ResolutionFloor != "" {
		p.ResolutionFloor = q.ResolutionFloor
	}
	if len(q.GroupOrder) > 0 {
		p.GroupOrder = q.GroupOrder
	}
	if len(q.CodecRank) > 0 {
		p.CodecRank = q.CodecRank
	}
	if len(q.ResolutionPenalty) > 0 {
		p.ResolutionPenalty = q.ResolutionPenalty
	}
	if q.PenaltyDub != nil {
		p.PenaltyDub = *q.PenaltyDub
	}
	if q.PenaltyUncensored != nil {
		p.PenaltyUncensored = *q.PenaltyUncensored
	}
	if q.RejectBatch != nil {
		p.RejectBatch = *q.RejectBatch
	}
	return p
}

// buildScheme turns the configured naming section into a naming scheme.
func buildScheme(n config.NamingConfig) (*naming.Scheme, error) {
	preset, err := naming.ParsePreset(n.Preset)
	if err != nil {
		return nil, err
	}
	return naming.Resolve(preset, n.Pattern, n.SeasonFolder)
}

// buildIndexer turns the configured indexer section into a nyaa client.
//
// The client is returned rather than installed as package state. It used to be
// the latter, which meant every query depended on this having run first, and a
// call site that ran earlier silently queried the default indexer instead of
// the configured one. Returning it makes that impossible: a caller cannot
// query without a client, and the client carries its own configuration.
func buildIndexer(ix config.IndexerConfig) (*nyaa.Client, error) {
	out := nyaa.DefaultIndexer()
	if ix.Base != "" {
		out.Base = ix.Base
	}
	if ix.Category != "" {
		out.Category = ix.Category
	}
	if ix.UserAgent != "" {
		out.UserAgent = ix.UserAgent
	}
	if ix.MinInterval != "" {
		d, err := time.ParseDuration(ix.MinInterval)
		if err != nil {
			return nil, fmt.Errorf("indexer.min_interval %q: %w", ix.MinInterval, err)
		}
		out.MinInterval = d
	}
	return nyaa.New(out), nil
}
