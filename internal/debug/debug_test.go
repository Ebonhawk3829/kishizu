package debug

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// capture redirects the logger for one test and returns what was written.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(old)
		log.SetFlags(oldFlags)
		Set(false)
	}()
	fn()
	return buf.String()
}

// TestLogIsSilentByDefault: debug output is high-volume by design — every
// candidate considered, every filter evaluated. Left on, it would drown the
// log in normal operation.
func TestLogIsSilentByDefault(t *testing.T) {
	Set(false)
	out := capture(t, func() {
		Log("considered %d", 42)
		Trace("release", "below floor")
	})
	if out != "" {
		t.Errorf("debug wrote %q while disabled", out)
	}
}

// TestLogWritesWhenEnabled: when on, every decision must be visible. This is
// the whole point — "what did it decide, and why" is the question a live run
// cannot otherwise answer.
func TestLogWritesWhenEnabled(t *testing.T) {
	Set(true)
	out := capture(t, func() {
		Log("considered %d", 42)
	})
	if !strings.Contains(out, "considered 42") {
		t.Errorf("Log wrote %q, want it to contain the formatted message", out)
	}
	if !strings.HasPrefix(out, "[debug] ") {
		t.Errorf("Log wrote %q, want the [debug] prefix so it is greppable", out)
	}
}

// TestTraceWritesWhenEnabled: a rejection without its reason is not
// debuggable, so Trace must carry both halves.
func TestTraceWritesWhenEnabled(t *testing.T) {
	Set(true)
	out := capture(t, func() {
		Trace("[Group] Show - 01 [720p]", "resolution below floor")
	})
	if !strings.Contains(out, "resolution below floor") {
		t.Errorf("Trace wrote %q, want the reason", out)
	}
}

// TestSetIsRuntimeToggleable: the API can switch this on during a live run,
// so a misbehaving pipeline can be diagnosed without a restart.
func TestSetIsRuntimeToggleable(t *testing.T) {
	Set(false)
	if Enabled() {
		t.Fatal("Enabled = true after Set(false)")
	}
	Set(true)
	if !Enabled() {
		t.Error("Enabled = false after Set(true)")
	}
	Set(false)
}

// TestEnabledReflectsSet: Enabled is what callers branch on, so it must
// track Set exactly.
func TestEnabledReflectsSet(t *testing.T) {
	defer Set(false)
	for _, want := range []bool{true, false, true} {
		Set(want)
		if Enabled() != want {
			t.Errorf("Enabled = %v, want %v", Enabled(), want)
		}
	}
}
