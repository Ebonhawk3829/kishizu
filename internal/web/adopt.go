package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/seadex"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/transmission"
)

// magnetFor builds a magnet link from an infohash. The display name is kept so
// the torrent has a readable name in Transmission.
func magnetFor(infohash, title string) string {
	return "magnet:?xt=urn:btih:" + infohash + "&dn=" + title
}

// ---------- adopting a finished season ----------
//
// Adoption is a separate entry point from the airing pipeline. It reads one
// SeaDex entry, proposes which file is which episode, and hands the confirmed
// result to the same download-and-file machinery. Nothing here touches the
// hunt loop, the cycle states or the air-date guard.

// adoptPreview is what the review screen renders: the plan, plus the range the
// episode dropdowns offer.
type adoptPreview struct {
	Title      string             `json:"title"`
	Release    string             `json:"release"`
	Tracker    string             `json:"tracker"`
	InfoHash   string             `json:"info_hash"`
	URL        string             `json:"url"`
	Files      []adoptFilePreview `json:"files"`
	MaxEpisode int                `json:"max_episode"`
	// Warnings the user should see before confirming.
	Incomplete      bool   `json:"incomplete"`
	TheoreticalBest string `json:"theoretical_best"`
	Notes           string `json:"notes"`
}

type adoptFilePreview struct {
	Name    string `json:"name"`
	Episode int    `json:"episode"`
	Include bool   `json:"include"`
	Why     string `json:"why"`
}

// handleAdoptPreview resolves a SeaDex URL and returns the proposed plan.
//
// Nothing is written and nothing is downloaded. The proposals are defaults for
// the user to confirm or correct in the review screen.
func (s *Server) handleAdoptPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := seadex.AniListIDFromURL(req.URL)
	if id == 0 {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("could not read a SeaDex entry id from %q", strings.TrimSpace(req.URL)))
		return
	}

	entry, err := seadex.New().FetchEntry(id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("could not read that entry: %w", err))
		return
	}
	if entry == nil {
		writeErr(w, http.StatusNotFound,
			fmt.Errorf("SeaDex has no entry for AniList %d (it only lists finished seasons)", id))
		return
	}
	plan, err := seadex.PlanEntry(entry)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err)
		return
	}

	out := adoptPreview{
		Title:           plan.Title,
		Release:         plan.Torrent.ReleaseGroup,
		Tracker:         plan.Torrent.Tracker,
		InfoHash:        plan.Torrent.InfoHash,
		URL:             plan.Torrent.URL,
		MaxEpisode:      plan.MaxEpisode,
		Incomplete:      plan.Incomplete,
		TheoreticalBest: plan.TheoreticalBest,
		Notes:           plan.Notes,
	}
	for _, c := range plan.Files {
		out.Files = append(out.Files, adoptFilePreview{
			Name:    c.Name,
			Episode: c.Episode,
			Include: c.Include,
			Why:     c.Why,
		})
	}
	writeJSON(w, out)
}

// handleAdopt performs a confirmed adoption.
//
// The client sends the confirmed rows: which files to take and which episode
// each one is. This is the review step's output, so the classifier's proposals
// are never trusted silently.
func (s *Server) handleAdopt(w http.ResponseWriter, r *http.Request) {
	if s.adopt.rpcURL == "" || s.adopt.staging == "" || s.adopt.library == "" {
		writeErr(w, http.StatusNotImplemented,
			fmt.Errorf("adoption is not configured on this server (needs staging, library and Transmission RPC)"))
		return
	}

	var req struct {
		URL      string             `json:"url"`
		Title    string             `json:"title"`
		InfoHash string             `json:"info_hash"`
		Files    []adoptFilePreview `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("title is required"))
		return
	}
	if req.InfoHash == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("no release selected"))
		return
	}

	// Rebuild the selection from what the user confirmed.
	sel := make([]seadex.Selected, 0, len(req.Files))
	for _, f := range req.Files {
		if !f.Include {
			continue
		}
		sel = append(sel, seadex.Selected{Name: f.Name, Episode: f.Episode})
	}
	if len(sel) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("no files selected"))
		return
	}
	// The dropdown bound comes from the media file count, so it is the same
	// bound the review screen offered.
	if err := seadex.Validate(sel, len(req.Files)); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	eps := seadex.Episodes(sel)
	if len(eps) == 0 {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("nothing to track: every selected file is marked as not an episode"))
		return
	}

	show := release.Sanitise(req.Title)
	stagingDir := filepath.Join(s.adopt.staging, show)

	sh, err := s.st.CreateShow(req.Title, nil, maxOf(eps))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("create show: %w", err))
		return
	}
	// Source seadex: the airing pipeline skips it, and the UI does not ask
	// whether it has aired or needs training.
	if err := s.st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Mark the episodes downloading. This is what makes Reconcile pick the
	// files up: it only finalises episodes already in flight.
	for _, n := range eps {
		if err := s.st.UpsertEpisode(sh.ID, n, episode.Downloading, req.InfoHash, req.Title); err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("mark episode %d: %w", n, err))
			return
		}
	}
	// Record the infohash so a later re-grab skips this release.
	if err := s.st.MarkSeen(req.InfoHash, sh.ID, 0); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if err := os.MkdirAll(stagingDir, 0o775); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("staging mkdir: %w", err))
		return
	}
	tc := transmission.New(s.adopt.rpcURL)
	if err := tc.AddWithDir(magnetFor(req.InfoHash, req.Title), stagingDir); err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("transmission add: %w", err))
		return
	}

	log.Printf("adopt: %q from %s: %d episodes downloading", req.Title, req.InfoHash, len(eps))
	writeJSON(w, map[string]any{
		"show_id":   sh.ID,
		"name":      req.Title,
		"episodes":  eps,
		"staging":   stagingDir,
		"info_hash": req.InfoHash,
	})
}

func maxOf(ns []int) int {
	best := 0
	for _, n := range ns {
		if n > best {
			best = n
		}
	}
	return best
}
