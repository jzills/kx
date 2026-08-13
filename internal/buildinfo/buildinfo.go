// Package buildinfo reports how the running kx binary was built.
//
// Release builds stamp the values in with -ldflags. Everything else — a local
// `go build`, a `go install github.com/jzills/kx/cmd/kx@latest` — falls back to
// the module and VCS metadata the toolchain embeds on its own, so a binary
// nobody stamped still reports something more useful than "dev".
package buildinfo

import (
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// commit and date are stamped at build time:
//
//	-X github.com/jzills/kx/internal/buildinfo.commit=abc1234
//	-X github.com/jzills/kx/internal/buildinfo.date=2026-08-11
var (
	commit string
	date   string
)

// Info is what `kx --version` reports.
type Info struct {
	// Version is the release version, or "dev" for an unstamped build.
	Version string
	// Commit is the source revision, with " (modified)" appended when the
	// working tree it was built from had uncommitted changes.
	Commit string
	// Date is the build date, empty when nothing recorded one.
	Date string
	// Go is the toolchain that compiled the binary.
	Go string
	// Platform is the GOOS/GOARCH it was compiled for.
	Platform string
}

// pseudoTail matches what the toolchain appends when it has to name a commit
// no tag does: a 14-digit UTC timestamp and a 12-character commit, behind the
// base version and any prerelease segment it carries.
//
//	0.3.4-0.20260813184656-8ec57390b220
//
// Anchored and shaped tightly enough that a real prerelease — 0.4.0-rc.1 — is
// left whole, where cutting at the first dash would report an unreleased
// 0.4.0 as shipped.
var pseudoTail = regexp.MustCompile(`-(?:[0-9A-Za-z.-]+\.)?[0-9]{14}-[0-9a-f]{12}$`)

// Tag spells the version the way its tag and its release do, with the leading
// "v". Version is stored bare because that is what the release stamps and what
// the wheel metadata carries, so the "v" belongs at the point of display.
func (i Info) Tag() string { return tag(i.Version) }

// ShortTag is Tag with a pseudo-version's tail cut off — the version alone.
// The help screen signs off with this: there, the question is which kx this is,
// and the timestamp and commit answering "built from what" belong under
// `kx --version`, which reports them as their own labelled lines anyway.
//
// Semver build metadata goes first, and not only so pseudoTail's anchor still
// reaches the commit it matches on: installing from a modified tree appends
// "+dirty", which is provenance rather than a different version of kx.
func (i Info) ShortTag() string {
	version, _, _ := strings.Cut(i.Version, "+")
	return tag(pseudoTail.ReplaceAllString(version, ""))
}

// tag adds the "v", except to a version that isn't one: an unstamped build
// reports "dev", and "vdev" names nothing anybody could look up.
func tag(version string) string {
	if version == "" || version[0] < '0' || version[0] > '9' {
		return version
	}
	return "v" + version
}

// Resolve fills in everything the build didn't stamp.
//
// The version argument is main's own -X target, kept because that is where the
// release script already writes it.
func Resolve(version string) Info {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		build = nil
	}
	return resolve(version, commit, date, build)
}

// resolve is Resolve with its inputs passed in, so the fallbacks can be tested.
// They can't be exercised through Resolve: the toolchain omits the vcs.*
// settings from test binaries, so a test that ran against the real build info
// would be asserting they are absent.
func resolve(version, commit, date string, build *debug.BuildInfo) Info {
	info := Info{
		Version:  version,
		Commit:   commit,
		Date:     normalizeDate(date),
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}

	if build == nil {
		return info
	}

	// `go install module@version` records the version it resolved, which is
	// the only place a binary installed that way learns it isn't "dev".
	if isUnstamped(info.Version) && build.Main.Version != "" && build.Main.Version != "(devel)" {
		info.Version = strings.TrimPrefix(build.Main.Version, "v")
	}

	settings := map[string]string{}
	for _, setting := range build.Settings {
		settings[setting.Key] = setting.Value
	}
	if info.Commit == "" {
		info.Commit = shortCommit(settings["vcs.revision"])
	}
	if info.Commit != "" && settings["vcs.modified"] == "true" {
		info.Commit += " (modified)"
	}
	if info.Date == "" {
		info.Date = normalizeDate(settings["vcs.time"])
	}
	return info
}

func isUnstamped(version string) bool {
	return version == "" || version == "dev"
}

// shortCommit abbreviates a revision to the length people actually quote.
func shortCommit(revision string) string {
	if len(revision) > 7 {
		return revision[:7]
	}
	return revision
}

// normalizeDate reduces an embedded RFC 3339 timestamp to the date, which is
// the part anyone comparing two builds reads. Anything else is passed through
// as given, since a release may stamp its own format.
func normalizeDate(value string) string {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format("2006-01-02")
	}
	return value
}
