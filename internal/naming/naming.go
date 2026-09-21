// Package naming decides where a finished episode is filed and what it is
// called.
//
// The layout is configurable because people arrive with a library already
// shaped by another tool. kishizu's own form is one option, not the only one.
//
// Two things must agree or the pipeline breaks: the writer (which names a
// file on completion) and the reader (which recognises that name when the
// watch signal arrives). Both go through this package, so a custom layout
// cannot work for one and not the other.
package naming

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Ebonhawk3829/kishizu/internal/release"
)

// Preset is a named layout.
type Preset string

const (
	// PresetKishizu is kishizu's own form:
	//   <library>/<Show>/<Show> - E09.mkv
	PresetKishizu Preset = "kishizu"
	// PresetSonarr is the Sonarr/Jellyfin convention:
	//   <library>/<Show>/Season 01/<Show> - S01E09.mkv
	PresetSonarr Preset = "sonarr"
	// PresetPlex is the Plex convention:
	//   <library>/<Show>/Season 01/<Show> - s01e09.mkv
	PresetPlex Preset = "plex"
	// PresetCustom uses an explicit pattern.
	PresetCustom Preset = "custom"
)

// Presets lists the layouts that can be configured.
var Presets = []Preset{PresetKishizu, PresetSonarr, PresetPlex, PresetCustom}

// Patterns for each preset. {ext} is always appended by the caller, since the
// extension comes from the downloaded file rather than from configuration.
const (
	patternKishizu = "{show} - E{episode:2}"
	patternSonarr  = "{show} - S{season:2}E{episode:2}"
	patternPlex    = "{show} - s{season:2}e{episode:2}"
)

// Scheme is a resolved naming layout.
type Scheme struct {
	// Pattern is the filename layout, without the extension.
	Pattern string
	// SeasonFolder, when true, files into <library>/<Show>/Season NN/.
	SeasonFolder bool
}

// ParsePreset reads a preset name. Empty means kishizu, which is what every
// existing library uses — an unset value must keep working rather than
// silently renaming someone's files.
func ParsePreset(s string) (Preset, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "kishizu":
		return PresetKishizu, nil
	case "sonarr", "sonarr-style":
		return PresetSonarr, nil
	case "plex", "plex-style":
		return PresetPlex, nil
	case "custom":
		return PresetCustom, nil
	}
	return "", fmt.Errorf("unknown naming preset %q (want one of: kishizu, sonarr, plex, custom)", s)
}

// Resolve turns a preset and an optional custom pattern into a Scheme.
//
// A custom pattern is required when the preset is custom, and ignored
// otherwise — otherwise a stale pattern left in the file would silently
// override the chosen preset.
func Resolve(preset Preset, pattern string, seasonFolder *bool) (*Scheme, error) {
	if preset == "" {
		preset = PresetKishizu
	}
	s := &Scheme{}
	switch preset {
	case PresetKishizu:
		s.Pattern = patternKishizu
		s.SeasonFolder = false
	case PresetSonarr:
		s.Pattern = patternSonarr
		s.SeasonFolder = true
	case PresetPlex:
		s.Pattern = patternPlex
		s.SeasonFolder = true
	case PresetCustom:
		if strings.TrimSpace(pattern) == "" {
			return nil, fmt.Errorf("naming preset %q requires a pattern", PresetCustom)
		}
		if err := ValidatePattern(pattern); err != nil {
			return nil, err
		}
		s.Pattern = strings.TrimSpace(pattern)
		s.SeasonFolder = false
	default:
		return nil, fmt.Errorf("unknown naming preset %q", preset)
	}
	// An explicit season_folder wins over the preset's default, so a user can
	// take the sonarr filename without the season directory.
	if seasonFolder != nil {
		s.SeasonFolder = *seasonFolder
	}
	return s, nil
}

// placeholders are the tokens a pattern may contain.
var placeholders = []string{"{show}", "{season}", "{season:2}", "{episode}", "{episode:2}"}

// ValidatePattern rejects a pattern that cannot produce a usable filename.
//
// {show} and an episode placeholder are both required: without the show the
// name is not identifiable, and without the episode the watch signal cannot
// read a number back out of it — which is the failure that leaves files on
// disk forever.
func ValidatePattern(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("pattern is empty")
	}
	if !strings.Contains(p, "{show}") {
		return fmt.Errorf("pattern must contain {show}")
	}
	if !strings.Contains(p, "{episode}") && !strings.Contains(p, "{episode:2}") {
		return fmt.Errorf("pattern must contain {episode} or {episode:2}")
	}
	// Reject unknown placeholders rather than leaving them literal: a typo
	// like {epsode} would silently produce a broken name for every file.
	//
	// Checked BEFORE the illegal-character scan, because a placeholder's own
	// syntax uses ":" ({episode:2}) — which is illegal in a filename but
	// fine here, since it is consumed rather than emitted.
	stripped := p
	for _, ph := range placeholders {
		stripped = strings.ReplaceAll(stripped, ph, "")
	}
	if i := strings.Index(stripped, "{"); i >= 0 {
		end := strings.Index(stripped[i:], "}")
		if end >= 0 {
			return fmt.Errorf("unknown placeholder %q", stripped[i:i+end+1])
		}
		return fmt.Errorf("unclosed '{' in pattern")
	}
	// Characters that are illegal in filenames on Linux or Windows, checked
	// against what is LEFT after the placeholders are removed.
	for _, c := range []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\x00"} {
		if strings.Contains(stripped, c) {
			return fmt.Errorf("pattern contains %q, which is not allowed in a filename", c)
		}
	}
	return nil
}

// Filename builds the filename for one episode, including the extension.
func (s *Scheme) Filename(show string, season, episode int, ext string) string {
	if !strings.HasPrefix(ext, ".") && ext != "" {
		ext = "." + ext
	}
	name := s.Pattern
	name = strings.ReplaceAll(name, "{show}", release.Sanitise(show))
	name = strings.ReplaceAll(name, "{season:2}", fmt.Sprintf("%02d", season))
	name = strings.ReplaceAll(name, "{episode:2}", fmt.Sprintf("%02d", episode))
	name = strings.ReplaceAll(name, "{season}", strconv.Itoa(season))
	name = strings.ReplaceAll(name, "{episode}", strconv.Itoa(episode))
	return name + ext
}

// Dir is the directory an episode is filed into.
func (s *Scheme) Dir(library, show string, season int) string {
	base := filepath.Join(library, release.Sanitise(show))
	if !s.SeasonFolder {
		return base
	}
	return filepath.Join(base, fmt.Sprintf("Season %02d", season))
}

// Path is the full destination for one episode.
func (s *Scheme) Path(library, show string, season, episode int, ext string) string {
	return filepath.Join(s.Dir(library, show, season), s.Filename(show, season, episode, ext))
}

// Extensions are the video extensions the healer tries when it needs to find
// a file whose exact extension it does not know.
var Extensions = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".mov", ".webm"}

// Candidates returns every path an episode could be filed under, one per
// known extension.
//
// The healer uses this: after a crash between the rename and the database
// write, the episode is stuck in "downloading" and the file is already in the
// library, but the extension is not recorded anywhere. Trying each one is how
// it is found.
func (s *Scheme) Candidates(library, show string, season, episode int) []string {
	dir := s.Dir(library, show, season)
	out := make([]string, 0, len(Extensions))
	for _, ext := range Extensions {
		out = append(out, filepath.Join(dir, s.Filename(show, season, episode, ext)))
	}
	return out
}

// rePlaceholder matches a placeholder with an optional width, capturing the
// name and the width.
var rePlaceholder = regexp.MustCompile(`\{(show|season|episode)(?::(\d+))?\}`)

// Matcher builds a regex that reads an episode number back out of a filename
// written by this scheme.
//
// This is the half that must agree with Filename. A scheme that writes a name
// it cannot read leaves every watch signal unmatched, and files pile up on
// disk because nothing ever marks them watched.
func (s *Scheme) Matcher() (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`(?i)`)
	last := 0
	for _, m := range rePlaceholder.FindAllStringSubmatchIndex(s.Pattern, -1) {
		start, end := m[0], m[1]
		b.WriteString(regexp.QuoteMeta(s.Pattern[last:start]))
		name := s.Pattern[m[2]:m[3]]
		width := 0
		if m[4] >= 0 {
			width, _ = strconv.Atoi(s.Pattern[m[4]:m[5]])
		}
		switch name {
		case "show":
			// The show name is anything up to the next literal. Non-greedy so
			// a show whose name contains the separator still matches.
			b.WriteString(`(.+?)`)
		case "season":
			b.WriteString(`\d{1,4}`)
		case "episode":
			// The width is a MINIMUM, not a maximum: {episode:2} pads 9 to "09"
			// but must still match 100, which is three digits. Anime seasons
			// pass 100 routinely on long-running shows.
			//
			// Reading is more lenient still: a file written by an older build,
			// or by hand, may carry "E1" where the scheme would write "E01".
			// Padding is a display concern and must not make a name unreadable.
			_ = width
			b.WriteString(`(\d{1,4})`)
		}
		last = end
	}
	b.WriteString(regexp.QuoteMeta(s.Pattern[last:]))
	// The extension is appended by Filename, so it is not in the pattern.
	b.WriteString(`\.[A-Za-z0-9]+$`)
	return regexp.Compile(b.String())
}

// EpisodeFrom reads the episode number out of a filename written by this
// scheme. Returns 0 when the name does not match.
func (s *Scheme) EpisodeFrom(name string) int {
	re, err := s.Matcher()
	if err != nil {
		return 0
	}
	m := re.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	// The last capture group is the episode: {show} is the only other
	// capturing group, and it comes first in every valid pattern.
	n, err := strconv.Atoi(m[len(m)-1])
	if err != nil {
		return 0
	}
	return n
}
