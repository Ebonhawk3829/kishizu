// Package version reports what build of kishizu is running.
//
// The value is injected at build time with -ldflags. It exists because a
// container image tag is not enough to answer "what is actually running":
// a pinned tag can be re-pushed, and a locally built binary has no tag at
// all. The API and the startup log both report this, so a bug report can
// say which build it came from.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is the build version, set with:
//
//	go build -ldflags "-X github.com/Ebonhawk3829/kishizu/internal/version.Version=v1.2.3"
//
// Empty when built without it, which is the normal case for a local build.
var Version string

// String returns the version, falling back to what the Go toolchain recorded
// and finally to "dev".
//
// The fallback matters: a binary built from a git checkout with no ldflags
// still knows its commit via the build info embedded since Go 1.18, so a
// locally built binary is identifiable rather than anonymous.
func String() string {
	if v := strings.TrimSpace(Version); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
			return v
		}
		// A VCS-tagged build: report the revision, which is more useful than
		// "unknown" when reproducing a bug.
		var rev, dirty string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "+dirty"
				}
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return "dev-" + rev + dirty
		}
	}
	return "dev"
}

// GoVersion is the toolchain the binary was built with. Reported alongside
// the version because a runtime bug is often a toolchain bug.
func GoVersion() string { return runtime.Version() }
