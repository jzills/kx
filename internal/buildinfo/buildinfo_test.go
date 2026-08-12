package buildinfo

import (
	"runtime"
	"runtime/debug"
	"testing"
)

// A stamped release must report exactly what the release stamped, never a
// value inferred from whatever the toolchain happened to embed.
func TestResolveKeepsAStampedVersion(t *testing.T) {
	if got := Resolve("0.3.2").Version; got != "0.3.2" {
		t.Errorf("Resolve(\"0.3.2\").Version = %q, want 0.3.2", got)
	}
}

// Everything the caller didn't supply still has to be filled in, or --version
// reports less than the binary knows about itself.
func TestResolveFillsInTheToolchain(t *testing.T) {
	info := Resolve("dev")
	if info.Go != runtime.Version() {
		t.Errorf("Go = %q, want %q", info.Go, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; info.Platform != want {
		t.Errorf("Platform = %q, want %q", info.Platform, want)
	}
}

// An unstamped build — `go build`, or `go install ...@latest` — has to report
// what the toolchain embedded, or it claims to be "dev" with nothing to say
// about where it came from.
func TestResolveFallsBackToEmbeddedMetadata(t *testing.T) {
	build := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.3.2"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "f80aeb8c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a"},
			{Key: "vcs.time", Value: "2026-08-11T14:32:07Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	info := resolve("dev", "", "", build)
	if info.Version != "0.3.2" {
		t.Errorf("Version = %q, want 0.3.2 from the module metadata", info.Version)
	}
	if info.Commit != "f80aeb8" {
		t.Errorf("Commit = %q, want f80aeb8 from vcs.revision", info.Commit)
	}
	if info.Date != "2026-08-11" {
		t.Errorf("Date = %q, want 2026-08-11 from vcs.time", info.Date)
	}
}

// What the release stamped always wins over what the toolchain guessed, or a
// release built from a tagged checkout could report the wrong thing.
func TestResolvePrefersStampedValues(t *testing.T) {
	build := &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0000000000000000000000000000000000000000"},
			{Key: "vcs.time", Value: "2020-01-01T00:00:00Z"},
		},
	}
	info := resolve("0.3.2", "f80aeb8", "2026-08-11", build)
	if info.Version != "0.3.2" || info.Commit != "f80aeb8" || info.Date != "2026-08-11" {
		t.Errorf("resolve stamped build = %+v, want the stamped values kept", info)
	}
}

// A binary built from a dirty tree is not the commit it names, and a bug
// reported against it needs to say so.
func TestResolveMarksAModifiedTree(t *testing.T) {
	build := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "f80aeb8c1d2e"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	if got := resolve("dev", "", "", build).Commit; got != "f80aeb8 (modified)" {
		t.Errorf("Commit = %q, want it marked as modified", got)
	}
}

// Nothing to read from is the `go run` case; it must not panic or invent
// values.
func TestResolveWithoutBuildInfo(t *testing.T) {
	info := resolve("dev", "", "", nil)
	if info.Commit != "" || info.Date != "" {
		t.Errorf("resolve(nil build) = %+v, want no commit or date", info)
	}
	if info.Version != "dev" {
		t.Errorf("Version = %q, want dev", info.Version)
	}
}

func TestShortCommitAbbreviates(t *testing.T) {
	cases := map[string]string{
		"f80aeb8c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a": "f80aeb8",
		"f80aeb8": "f80aeb8",
		"abc":     "abc",
		"":        "",
	}
	for revision, want := range cases {
		if got := shortCommit(revision); got != want {
			t.Errorf("shortCommit(%q) = %q, want %q", revision, got, want)
		}
	}
}

// vcs.time is RFC 3339; a release stamps whatever it likes. The timestamp is
// reduced to the date, and anything unparseable is passed through rather than
// dropped.
func TestNormalizeDate(t *testing.T) {
	cases := map[string]string{
		"2026-08-11T14:32:07Z":      "2026-08-11",
		"2026-08-11T23:30:00-05:00": "2026-08-12",
		"2026-08-11":                "2026-08-11",
		"nightly":                   "nightly",
		"":                          "",
	}
	for value, want := range cases {
		if got := normalizeDate(value); got != want {
			t.Errorf("normalizeDate(%q) = %q, want %q", value, got, want)
		}
	}
}
