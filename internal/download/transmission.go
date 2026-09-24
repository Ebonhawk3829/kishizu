package download

import (
	"context"

	"github.com/Ebonhawk3829/kishizu/internal/transmission"
)

// transmissionDownloader adapts the Transmission RPC client to Downloader.
//
// The adapter exists so the RPC client stays a plain Transmission client and
// the rest of kishizu stays ignorant of which client it is talking to.
type transmissionDownloader struct {
	c *transmission.Client
}

// NewTransmission builds a Downloader backed by a Transmission RPC endpoint.
func NewTransmission(rpcURL string) Downloader {
	return &transmissionDownloader{c: transmission.New(rpcURL)}
}

func (t *transmissionDownloader) Name() string { return "Transmission" }

// URL is the WebUI address (the RPC endpoint with /rpc swapped for /web/),
// where a user can look at a download directly. Browsing to the RPC endpoint
// itself answers 409 — the CSRF handshake — so the link-out points here.
func (t *transmissionDownloader) URL() string { return t.c.URL() }

func (t *transmissionDownloader) Add(ctx context.Context, magnet, dir string) error {
	return t.c.AddWithDir(ctx, magnet, dir)
}
