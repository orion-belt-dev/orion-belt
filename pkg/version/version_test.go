package version

import "testing"

// The build vars are package-level and set via -ldflags, so tests must restore
// them or they leak into every later test in the package.
func withBuildInfo(t *testing.T, version, commit, date string) {
	t.Helper()
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })
	Version, Commit, Date = version, commit, date
}

func TestStringOmitsCommitWhenUnset(t *testing.T) {
	// "none" is the compiled-in default when -ldflags didn't supply a commit;
	// both it and the empty string mean "no commit to show".
	for _, commit := range []string{"", "none"} {
		t.Run("commit="+commit, func(t *testing.T) {
			withBuildInfo(t, "1.2.3", commit, "2026-07-28")
			if got := String(); got != "1.2.3" {
				t.Errorf("String() = %q, want %q", got, "1.2.3")
			}
		})
	}
}

func TestStringTruncatesLongCommitToShortSHA(t *testing.T) {
	withBuildInfo(t, "1.2.3", "abcdef1234567890abcdef1234567890abcdef12", "2026-07-28")

	if got, want := String(), "1.2.3+abcdef12"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestStringKeepsShortCommitIntact(t *testing.T) {
	// Exactly 8 characters is the boundary: it must not be truncated further.
	withBuildInfo(t, "1.2.3", "abcdef12", "2026-07-28")
	if got, want := String(), "1.2.3+abcdef12"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	// Fewer than 8 characters passes through unchanged rather than panicking
	// on the slice bound.
	withBuildInfo(t, "1.2.3", "abc", "2026-07-28")
	if got, want := String(), "1.2.3+abc"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDefaultBuildInfoIsUsableWithoutLdflags(t *testing.T) {
	// An un-stamped `go build` must still produce something printable rather
	// than an empty version string.
	if String() == "" {
		t.Error("String() is empty for the default build vars")
	}
}

func TestInfoReportsAllFieldsIncludingDisplay(t *testing.T) {
	withBuildInfo(t, "2.0.0", "0123456789abcdef", "2026-07-28")

	info := Info()
	want := map[string]string{
		"version": "2.0.0",
		"commit":  "0123456789abcdef",
		"date":    "2026-07-28",
		"display": "2.0.0+01234567",
	}
	if len(info) != len(want) {
		t.Errorf("Info() has %d keys (%v), want %d", len(info), info, len(want))
	}
	for k, v := range want {
		if got := info[k]; got != v {
			t.Errorf("Info()[%q] = %q, want %q", k, got, v)
		}
	}
}

// Info() must report the untruncated commit but the truncated display string;
// conflating the two would lose information in the API response.
func TestInfoKeepsFullCommitButShortDisplay(t *testing.T) {
	full := "abcdef1234567890abcdef1234567890abcdef12"
	withBuildInfo(t, "1.0.0", full, "2026-07-28")

	info := Info()
	if info["commit"] != full {
		t.Errorf("Info()[\"commit\"] = %q, want the full SHA %q", info["commit"], full)
	}
	if info["display"] != "1.0.0+abcdef12" {
		t.Errorf("Info()[\"display\"] = %q, want the short form", info["display"])
	}
}

func TestInfoReturnsIndependentMaps(t *testing.T) {
	// Callers serialize this straight to JSON; a shared map would let one
	// handler's mutation bleed into another's response.
	a, b := Info(), Info()
	a["version"] = "mutated"

	if b["version"] == "mutated" {
		t.Error("Info() returned a shared map; mutating one result affected another")
	}
}
