package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/anilist"
	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/seadex"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

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
	if s.adopt.downloader == nil || s.adopt.staging == "" || s.adopt.library == "" {
		writeErr(w, http.StatusNotImplemented,
			fmt.Errorf("adoption is not configured on this server (needs staging, library and a downloader)"))
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
	// Cover art. An adopted show never touches animeschedule.net, so it has
	// none from the usual path, and SeaDex's API exposes no image. But the
	// entry URL carries the AniList id, and AniList serves the same poster
	// the SeaDex page shows. Best effort: a failure costs a missing poster,
	// not the adoption.
	if id := seadex.AniListIDFromURL(req.URL); id > 0 {
		if m, err := anilist.New().FetchMedia(id); err == nil && m != nil && m.CoverURL != "" {
			if err := s.st.SetImageURL(sh.ID, m.CoverURL); err != nil {
				log.Printf("adopt: set image: %v", err)
			} else if s.art != nil {
				if _, err := s.art.Ensure(m.CoverURL); err != nil {
					log.Printf("adopt: cache art: %v", err)
				}
			}
		} else if err != nil {
			log.Printf("adopt: anilist lookup: %v", err)
		}
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
	if err := s.adopt.downloader.Add(download.Magnet(req.InfoHash, req.Title), stagingDir); err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("%s add: %w", s.adopt.downloader.Name(), err))
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
