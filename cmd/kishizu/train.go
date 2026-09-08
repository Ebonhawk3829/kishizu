package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/nyaa"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/train"
)

// nextUnwatched returns the first episode with no terminal state, which is what
// training should target by default.
func nextUnwatched(st *store.Store, sh *store.Show) int {
	eps, err := st.EpisodesForShow(sh.ID)
	if err != nil {
		return 1
	}
	next := 1
	for _, e := range eps {
		if e.Number == next && (e.State == episode.Watched || e.State == episode.Deleted) {
			next++
		}
	}
	return next
}

// trainShowCmd runs the propose-and-confirm loop interactively.
//
// Flow: the user supplies one seed release for a known episode, then answers
// yes/no (with a reason) for whatever the tool proposes. Each answer refits the
// model. Nothing is downloaded.
func trainShowCmd(st *store.Store, sh *store.Show, targetEp int) error {
	items, err := nyaa.Fetch(nil, nyaa.FeedURL(sh.CanonicalName))
	if err != nil {
		return fmt.Errorf("fetch feed: %w", err)
	}
	if len(items) == 0 {
		return fmt.Errorf("no results for %q", sh.CanonicalName)
	}

	fmt.Printf("\n=== %s (episode %d)\n", sh.CanonicalName, targetEp)
	fmt.Printf("    %d releases found\n\n", len(items))

	// Show a few to seed from.
	fmt.Println("Pick a release you would accept for this episode:")
	for i, it := range items {
		if i >= 8 {
			break
		}
		fmt.Printf("  [%d] %s\n", i+1, truncate(it.Title, 76))
	}
	fmt.Printf("\nEnter number (or a release title, or blank to skip this show): ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		fmt.Println("skipped")
		return nil
	}

	var seedTitle string
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(items) {
		seedTitle = items[n-1].Title
	} else {
		seedTitle = line
	}

	s, err := train.NewSession(st, sh, targetEp)
	if err != nil {
		return err
	}
	if err := s.Seed(seedTitle); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	fmt.Printf("\nseed accepted: %s\n", truncate(seedTitle, 70))

	// Propose-and-confirm rounds.
	for round := 1; round <= 6; round++ {
		cands := s.Propose(items, 3)
		if len(cands) == 0 {
			fmt.Println("\nnothing more to ask")
			break
		}

		fmt.Printf("\n--- round %d: are these also episode %d?\n", round, targetEp)
		for i, c := range cands {
			ep := "?"
			if c.Episode > 0 {
				ep = strconv.Itoa(c.Episode)
			}
			fmt.Printf("  [%d] ep%-3s %s\n", i+1, ep, truncate(c.Item.Title, 70))
		}
		fmt.Printf("\nAnswer like 'y', 'n', 'y n y', or 'done': ")

		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "done" || ans == "d" || ans == "" {
			break
		}

		parts := strings.Fields(ans)
		for i, p := range parts {
			if i >= len(cands) {
				break
			}
			switch {
			case strings.HasPrefix(p, "y"):
				if err := s.Accept(cands[i]); err != nil {
					return err
				}
			case strings.HasPrefix(p, "n"):
				reason := askReason(reader, cands[i])
				if err := s.Reject(cands[i], reason); err != nil {
					return err
				}
			}
		}
	}

	if err := s.Commit(); err != nil {
		return err
	}

	fmt.Printf("\nlearned: %d accepted, %d rejected\n", s.Accepted, s.Rejected)
	offsets, _ := st.GroupOffsets(sh.ID)
	for g, off := range offsets {
		fmt.Printf("  offset %-16s %d\n", g, off)
	}
	return nil
}

// askReason asks why a candidate was rejected. The reason determines whether
// the matcher is corrected or only a filter/preference is added.
func askReason(reader *bufio.Reader, c train.Candidate) train.Reason {
	fmt.Printf("  why was %q wrong?\n", truncate(c.Item.Title, 50))
	fmt.Printf("    [1] wrong episode  [2] wrong show  [3] batch  [4] dub\n")
	fmt.Printf("    [5] codec/quality  [6] other: ")

	line, _ := reader.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		return train.ReasonWrongEpisode
	case "2":
		return train.ReasonWrongShow
	case "3":
		return train.ReasonBatch
	case "4":
		return train.ReasonDub
	case "5":
		return train.ReasonCodec
	default:
		return train.ReasonOther
	}
}
