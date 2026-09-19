package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/seadex"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// adoptSeason prepares an adoption of a finished season from a SeaDex entry
// and prints the plan.
//
// This is the MVP skeleton: it resolves the entry, classifies every file,
// and reports exactly what would be handed to Transmission and what episode
// rows would be created. Nothing is downloaded and nothing is written to the
// database yet — the review step (confirming or correcting each file's
// episode) belongs in the UI, and until that exists the proposals must not
// be trusted silently.
//
// -adopt-episodes overrides the classifier's proposal, which is how the
// override is exercised before the UI exists: a comma-separated list of the
// numbers to adopt, in the same order as the listed files.
func adoptSeason(st *store.Store, rawURL, episodeList, staging, library string) error {
	id := seadex.AniListIDFromURL(rawURL)
	if id == 0 {
		return fmt.Errorf("could not read a SeaDex entry id from %q", rawURL)
	}

	fmt.Printf("SeaDex entry %d\n", id)
	entry, err := seadex.New().FetchEntry(id)
	if err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("SeaDex has no entry for AniList %d (it only lists finished seasons)", id)
	}

	plan, err := seadex.PlanEntry(entry)
	if err != nil {
		return err
	}

	fmt.Printf("  title:    %s\n", orUnknown(plan.Title))
	fmt.Printf("  release:  %s · %s\n", plan.Torrent.ReleaseGroup, plan.Torrent.Tracker)
	fmt.Printf("  infohash: %s\n", plan.Torrent.InfoHash)
	fmt.Printf("  url:      %s\n", plan.Torrent.URL)
	if plan.Incomplete {
		fmt.Printf("  WARNING:  entry is marked incomplete — this release does not contain every episode\n")
	}
	if plan.TheoreticalBest != "" {
		fmt.Printf("  WARNING:  best release does not exist yet: %s\n", plan.TheoreticalBest)
	}
	if plan.Notes != "" {
		fmt.Printf("  notes:    %s\n", plan.Notes)
	}
	fmt.Println()

	// Apply the override if given, otherwise take the classifier's proposal.
	sel, err := selectionFor(plan, episodeList)
	if err != nil {
		return err
	}
	if err := seadex.Validate(sel, plan.MaxEpisode); err != nil {
		return fmt.Errorf("invalid selection: %w", err)
	}

	fmt.Printf("Files (%d media, dropdown range 1..%d):\n", len(plan.Files), plan.MaxEpisode)
	for i, c := range plan.Files {
		mark := " "
		if sel[i].Episode > 0 {
			mark = "x"
		}
		ep := "--"
		if sel[i].Episode > 0 {
			ep = fmt.Sprintf("%02d", sel[i].Episode)
		}
		fmt.Printf("  [%s] %-3s %s\n", mark, ep, c.Name)
		if !c.Include && sel[i].Episode == 0 {
			fmt.Printf("            (%s)\n", c.Why)
		}
	}
	fmt.Println()

	eps := seadex.Episodes(sel)
	fmt.Printf("Would adopt %d episode(s): %s\n", len(eps), joinInts(eps))
	// The magnet's display name is the torrent's own name, not the group:
	// Transmission shows it in its list, and "sam" alone says nothing.
	fmt.Printf("Would hand to Transmission: %s\n",
		magnetFor(plan.Torrent.InfoHash, plan.Title))
	fmt.Printf("Staging dir: %s\n", filepath.Join(staging, release.Sanitise(plan.Title)))
	fmt.Printf("Library dir: %s\n", filepath.Join(library, release.Sanitise(plan.Title)))
	fmt.Println()
	fmt.Println("Dry run only: nothing was downloaded and no episode rows were created.")
	fmt.Println("The review step (confirming each file's episode) is not built yet.")

	_ = st
	return nil
}

// selectionFor builds the confirmed rows: either the classifier's proposal,
// or the -adopt-episodes override applied positionally to the file list.
func selectionFor(plan *seadex.Plan, episodeList string) ([]seadex.Selected, error) {
	out := make([]seadex.Selected, len(plan.Files))
	for i, c := range plan.Files {
		out[i] = seadex.Selected{Name: c.Name}
		if c.Include {
			out[i].Episode = c.Episode
		}
	}
	if strings.TrimSpace(episodeList) == "" {
		return out, nil
	}
	parts := strings.Split(episodeList, ",")
	if len(parts) != len(plan.Files) {
		return nil, fmt.Errorf("-adopt-episodes has %d entries, but there are %d files; give one number per file (0 = download but do not track)",
			len(parts), len(plan.Files))
	}
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "-" || p == "--" {
			out[i].Episode = 0
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("-adopt-episodes entry %q is not a number", p)
		}
		out[i].Episode = n
	}
	return out, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "(could not derive — would need to be entered)"
	}
	return s
}

func joinInts(ns []int) string {
	if len(ns) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ", ")
}

var _ = os.Stdout
