// Package debug provides a toggleable verbose mode for live testing.
//
// The pipeline touches three external systems (Nyaa, Transmission, ntfy) and
// the filesystem. When something misbehaves in a live run, the question is
// almost always "what did it decide, and why" — so debug mode makes every
// decision visible without rebuilding or restarting.
//
// It is off by default and safe to leave compiled in: the checks are a boolean
// read, and the extra work only happens when enabled.
package debug

import (
	"log"
	"os"
	"strconv"
	"sync/atomic"
)

// enabled is atomic so it can be toggled at runtime from the API without
// racing the poll loop.
var enabled atomic.Bool

func init() {
	// KISHIZU_DEBUG=1 in the environment turns it on at startup, which is
	// handy in a container where flags are awkward to change.
	if v, _ := strconv.ParseBool(os.Getenv("KISHIZU_DEBUG")); v {
		enabled.Store(true)
	}
}

// Enabled reports whether debug logging is on.
func Enabled() bool { return enabled.Load() }

// Set turns debug logging on or off at runtime.
func Set(on bool) { enabled.Store(on) }

// Log writes a message only when debug mode is on.
//
// Used for the high-volume detail — every candidate considered, every filter
// evaluated — that would drown the log in normal operation but is exactly what
// is needed when a release is not being grabbed.
func Log(format string, args ...any) {
	if !enabled.Load() {
		return
	}
	log.Printf("[debug] "+format, args...)
}

// Trace logs a decision with its reason. Every rejection in the pipeline
// carries a why, and this is what surfaces it.
func Trace(what, why string) {
	if !enabled.Load() {
		return
	}
	log.Printf("[debug] %s: %s", what, why)
}
