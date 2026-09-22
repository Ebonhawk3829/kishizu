package seadex

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Media extensions. Anything else in a torrent (scans, booklets, .png
// adverts, subtitle files) is never an episode and is dropped before the
// review list is built.
var reMedia = regexp.MustCompile(`(?i)\.(mkv|mp4|m4v|avi|ts|mov|webm)$`)

// Tokens that mark a file as not an episode: openings, endings, specials and
// the other extras that ship inside a season pack.
//
// Matched as whole words so that a show with "Ending" in its title is not
// excluded by accident.
var reExtra = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(` +
	`NCOP|NCED|OP|ED|OVA|OAD|ONA|SPECIAL|SPECIALS|PREVIEW|PV|CM|` +
	`MENU|EXTRA|EXTRAS|BONUS|TRAILER|TEASER|MAKING|INTERVIEW|LIVE|` +
	`CONCERT|RECAP|DIGEST|PROMO|COMMERCIAL|LOGO|SCAN|SCANS|ART|` +
	`BOOKLET|OST|SOUNDTRACK|MUSIC|INSERT|ENDING|OPENING|CREDITLESS` +
	`)(?:[^a-z0-9]|$)`)

// reEpisode reads the episode number from a filename: the number after the
// last " - " separator, with an optional version suffix (01v2).
var reEpisode = regexp.MustCompile(`^(.*?)\s+-\s+(\d{1,3})(v\d+)?\b`)

// FileClass is what the classifier thinks one file is.
type FileClass struct {
	// Name is the original filename, unmodified.
	Name string
	// Episode is the proposed episode number, 0 when the file does not look
	// like an episode.
	Episode int
	// Include is the proposed checkbox state: true for files that look like
	// episodes, false for extras and unreadable files.
	Include bool
	// Why explains the proposal, so the review screen can show its reasoning
	// rather than asking the user to trust it.
	Why string
}

// ClassifyFiles proposes an episode number and an include flag for every
// media file in a torrent.
//
// Non-media files are dropped entirely: a 274-file pack may contain 234
// scans and booklets, and listing those would make the review screen
// unusable.
//
// The proposals are defaults for a human to confirm, not decisions. The
// classifier is right often enough to save the tedium and wrong often
// enough that it must never be trusted silently.
func ClassifyFiles(files []File) []FileClass {
	var out []FileClass
	for _, f := range files {
		s := stripDecorations(f.Name)
		if !reMedia.MatchString(s) {
			continue
		}
		body := reMedia.ReplaceAllString(s, "")

		if m := reExtra.FindString(body); m != "" {
			out = append(out, FileClass{
				Name:    f.Name,
				Include: false,
				Why:     "extra: " + strings.TrimSpace(m),
			})
			continue
		}
		if m := reEpisode.FindStringSubmatch(body); m != nil {
			n, err := strconv.Atoi(m[2])
			if err == nil {
				out = append(out, FileClass{
					Name:    f.Name,
					Episode: n,
					Include: true,
					Why:     "episode " + m[2] + m[3],
				})
				continue
			}
		}
		out = append(out, FileClass{
			Name:    f.Name,
			Include: false,
			Why:     "no episode number readable",
		})
	}
	return out
}

// stripDecorations removes the bracketed group prefix and any trailing
// bracketed tag (CRC, quality) so the episode pattern has something clean to
// match against.
func stripDecorations(name string) string {
	s := strings.TrimSpace(name)
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	// Trailing [CRC] / [tags].
	for {
		trimmed := strings.TrimSpace(s)
		if !strings.HasSuffix(trimmed, "]") {
			break
		}
		i := strings.LastIndex(trimmed, "[")
		if i < 0 {
			break
		}
		s = strings.TrimSpace(trimmed[:i])
	}
	return s
}

// DeriveTitle recovers the show title from the classified files: the text
// before the episode separator, which every file in a pack shares.
//
// Returns "" when no file yielded a title, in which case the caller should
// ask the user rather than invent one.
func DeriveTitle(classes []FileClass) string {
	counts := map[string]int{}
	for _, c := range classes {
		s := stripDecorations(c.Name)
		if !reMedia.MatchString(s) {
			continue
		}
		body := reMedia.ReplaceAllString(s, "")
		if m := reEpisode.FindStringSubmatch(body); m != nil {
			t := strings.TrimSpace(m[1])
			if t != "" {
				counts[t]++
			}
		}
	}
	best, n := "", 0
	for t, c := range counts {
		if c > n || (c == n && len(t) > len(best)) {
			best, n = t, c
		}
	}
	return best
}

// Plan is the result of preparing an adoption: everything the review screen
// needs, and everything the handoff needs once confirmed.
type Plan struct {
	// AniListID is the SeaDex entry that was read.
	AniListID int
	// Title is derived from the filenames. Empty when it could not be read.
	Title string
	// Torrent is the release that will be handed to Transmission.
	Torrent Torrent
	// Files is one row per media file, with proposed episode and include
	// state. The user edits these before adopting.
	Files []FileClass
	// MaxEpisode is the upper bound for the episode dropdown: the number of
	// media files, since a pack cannot contain more episodes than files.
	MaxEpisode int
	// Incomplete and TheoreticalBest are surfaced from the entry so the
	// review screen can warn that the listed release is not the whole story.
	Incomplete      bool
	TheoreticalBest string
	Notes           string
}

// PlanEntry builds an adoption plan from a SeaDex entry.
//
// It picks the first best Nyaa torrent. When several exist (different groups
// or versions of the same judgement) the caller should let the user choose;
// for the MVP the first is used and the alternatives are reported.
func PlanEntry(e *Entry) (*Plan, error) {
	if e == nil {
		return nil, fmt.Errorf("seadex: no entry")
	}
	best := e.BestNyaa()
	if len(best) == 0 {
		return nil, fmt.Errorf("seadex: no best release on Nyaa for AniList %d", e.AniListID)
	}
	t := best[0]
	files := ClassifyFiles(t.Files)
	if len(files) == 0 {
		return nil, fmt.Errorf("seadex: %s has no media files", t.ReleaseGroup)
	}
	return &Plan{
		AniListID:       e.AniListID,
		Title:           DeriveTitle(files),
		Torrent:         t,
		Files:           files,
		MaxEpisode:      len(files),
		Incomplete:      e.Incomplete,
		TheoreticalBest: e.TheoreticalBest,
		Notes:           e.Notes,
	}, nil
}

// Selected is one confirmed row: a file the user wants, and the episode it
// should be filed as. Episode 0 means "download it but do not track it as an
// episode", which is how specials you actually want are handled.
type Selected struct {
	Name    string
	Episode int
}

// Validate checks a set of confirmed rows before anything is handed to
// Transmission.
//
// Two rules: an episode number must be within the dropdown range, and no two
// ticked rows may claim the same episode. The second is the one that matters
// — two files both tagged episode 1 would race in the reconciler, and which
// one won would be down to directory order.
func Validate(sel []Selected, maxEpisode int) error {
	seen := map[int]string{}
	for _, s := range sel {
		if s.Episode < 0 || s.Episode > maxEpisode {
			return fmt.Errorf("episode %d out of range 1..%d (%s)", s.Episode, maxEpisode, s.Name)
		}
		if s.Episode == 0 {
			continue
		}
		if other, ok := seen[s.Episode]; ok {
			return fmt.Errorf("episode %d assigned twice: %q and %q", s.Episode, other, s.Name)
		}
		seen[s.Episode] = s.Name
	}
	return nil
}

// Episodes returns the sorted episode numbers a confirmed selection will
// create, excluding the untracked (0) rows.
func Episodes(sel []Selected) []int {
	var out []int
	for _, s := range sel {
		if s.Episode > 0 {
			out = append(out, s.Episode)
		}
	}
	sort.Ints(out)
	return out
}
