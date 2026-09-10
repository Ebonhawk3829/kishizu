// Package config loads the user's show seed file.
//
// Deliberately minimal YAML: only the subset the seed file uses, parsed by
// hand. A full YAML parser would be the project's only third-party dependency
// outside the SQLite driver, and the file format is ours to define.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Show is one entry in the seed file.
type Show struct {
	Name    string
	Aliases []string
	Watched int
	Max     int
}

// Load parses the seed file.
//
// The expected shape is:
//
//	shows:
//	  - name: Some Show
//	    aliases:
//	      - Alias One
//	      - Alias Two
//	    watched: 3
//	    max: 12
//
// Parsing is line-based rather than recursive-descent because the file has
// exactly one list of flat maps.
func Load(path string) ([]Show, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(string(b))
}

func parse(s string) ([]Show, error) {
	var out []Show
	var cur *Show
	inAliases := false

	for i, raw := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineNo := i + 1

		switch {
		case trimmed == "shows:":
			continue

		case inAliases && strings.HasPrefix(trimmed, "- "):
			// An alias item. Checked before the new-show case because both
			// start with "- "; the aliases flag disambiguates.
			if cur == nil {
				return nil, fmt.Errorf("line %d: alias outside a show", lineNo)
			}
			cur.Aliases = append(cur.Aliases, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))

		case strings.HasPrefix(trimmed, "- "):
			// New show entry.
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &Show{}
			inAliases = false
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")); strings.HasPrefix(rest, "name:") {
				cur.Name = strings.TrimSpace(strings.TrimPrefix(rest, "name:"))
			} else if rest != "" {
				return nil, fmt.Errorf("line %d: expected name after '- ', got %q", lineNo, trimmed)
			}

		case strings.HasPrefix(trimmed, "name:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: %q outside a show", lineNo, trimmed)
			}
			cur.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			inAliases = false

		case strings.HasPrefix(trimmed, "aliases:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: %q outside a show", lineNo, trimmed)
			}
			inAliases = true

		case strings.HasPrefix(trimmed, "watched:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: watched outside a show", lineNo)
			}
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "watched:")))
			if err != nil {
				return nil, fmt.Errorf("line %d: bad watched value: %w", lineNo, err)
			}
			cur.Watched = n
			inAliases = false

		case strings.HasPrefix(trimmed, "max:"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: max outside a show", lineNo)
			}
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "max:")))
			if err != nil {
				return nil, fmt.Errorf("line %d: bad max", lineNo)
			}
			cur.Max = n
			inAliases = false

		default:
			return nil, fmt.Errorf("line %d: unexpected %q", lineNo, trimmed)
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}

	for _, sh := range out {
		if sh.Name == "" {
			return nil, fmt.Errorf("a show is missing its name")
		}
	}
	return out, nil
}
