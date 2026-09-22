package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/download"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/seadex"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// adoptSeason adopts a finished season from a SeaDex entry. Dry run unless
// confirm is given; episodeList (-adopt-episodes) overrides the classifier's
// proposal.
func adoptSeason(ctx context.Context, st *store.Store, rawURL, episodeList, staging, library string, dl download.Downloader, confirm bool) error {
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
	title := plan.Title
	if title == "" {
		return fmt.Errorf("could not derive a title from the filenames; refusing to adopt")
	}
	show := release.Sanitise(title)
	stagingDir := filepath.Join(staging, show)
	libraryDir := filepath.Join(library, show)

	fmt.Printf("Adopting %d episode(s): %s\n", len(eps), joinInts(eps))
	// The magnet's display name is the torrent's own name, which the client
	// shows in its list.
	fmt.Printf("Magnet:      %s\n", download.Magnet(plan.Torrent.InfoHash, title))
	fmt.Printf("Staging dir: %s\n", stagingDir)
	fmt.Printf("Library dir: %s\n", libraryDir)
	fmt.Println()

	if !confirm {
		fmt.Printf("Dry run. Re-run with -adopt-confirm to hand this to %s.\n", dl.Name())
		return nil
	}

	// Source seadex: the airing pipeline skips it and the UI does not ask
	// whether it has aired or needs training.
	sh, err := st.CreateShow(title, nil, maxOf(eps))
	if err != nil {
		return fmt.Errorf("create show: %w", err)
	}
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		return fmt.Errorf("set source: %w", err)
	}

	// Marking downloading is what makes Reconcile pick the files up: it only
	// finalises episodes already in flight.
	for _, n := range eps {
		if err := st.UpsertEpisode(sh.ID, n, episode.Downloading, plan.Torrent.InfoHash, plan.Torrent.ReleaseGroup); err != nil {
			return fmt.Errorf("mark episode %d: %w", n, err)
		}
	}
	// Record the infohash so a later re-grab skips this release.
	if err := st.MarkSeen(plan.Torrent.InfoHash, sh.ID, 0); err != nil {
		return fmt.Errorf("mark seen: %w", err)
	}

	// One staging directory per show, created here so it is owned by our uid.
	if err := os.MkdirAll(stagingDir, 0o775); err != nil {
		return fmt.Errorf("staging mkdir %s: %w", stagingDir, err)
	}
	if err := dl.Add(ctx, download.Magnet(plan.Torrent.InfoHash, title), stagingDir); err != nil {
		return fmt.Errorf("%s add: %w", dl.Name(), err)
	}

	fmt.Printf("Adopted %q: %d episodes downloading.\n", title, len(eps))
	fmt.Printf("Reconcile will file them into %s as they complete.\n", libraryDir)
	return nil
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
