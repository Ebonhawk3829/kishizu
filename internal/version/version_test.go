package version

import (
	"strings"
	"testing"
)

// TestStringPrefersInjectedVersion: the ldflags value is authoritative. A
// release build must report its tag, not whatever the toolchain guessed.
func TestStringPrefersInjectedVersion(t *testing.T) {
	defer func(old string) { Version = old }(Version)
	Version = "v1.2.3"
	if got := String(); got != "v1.2.3" {
		t.Errorf("String() = %q, want v1.2.3", got)
	}
}

// TestStringIgnoresBlankInjection: an empty ldflags value must fall through
// rather than report "". A build with no version is still identifiable via
// the toolchain's build info.
func TestStringIgnoresBlankInjection(t *testing.T) {
	defer func(old string) { Version = old }(Version)
	for _, v := range []string{"", "   "} {
		Version = v
		if got := String(); strings.TrimSpace(got) == "" {
			t.Errorf("String() = %q with Version=%q; must never be blank", got, v)
		}
	}
}

// TestStringNeverBlank: every build must report something. An anonymous
// binary makes a bug report unactionable.
func TestStringNeverBlank(t *testing.T) {
	defer func(old string) { Version = old }(Version)
	Version = ""
	got := String()
	if strings.TrimSpace(got) == "" {
		t.Error("String() is blank; every build must report something")
	}
	// Locally built binaries fall back to a commit or "dev".
	if got != "dev" && !strings.HasPrefix(got, "dev-") && got != "(devel)" {
		// A module build may report a pseudo-version; that is fine too.
		t.Logf("String() = %q (build-info fallback)", got)
	}
}

// TestGoVersionIsSet: reported alongside the version because a runtime bug is
// often a toolchain bug.
func TestGoVersionIsSet(t *testing.T) {
	if strings.TrimSpace(GoVersion()) == "" {
		t.Error("GoVersion() is empty")
	}
}
