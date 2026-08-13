package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/buildinfo"
)

// The release workflow compares the binary's first line against "kx v<version>"
// exactly. The detail below it may grow; that line may not — and if its
// spelling changes again, the check in release.yml changes with it.
func TestVersionFirstLineIsTheVersionAlone(t *testing.T) {
	text := versionText(buildinfo.Info{
		Version: "0.3.2", Commit: "f80aeb8", Date: "2026-08-11",
		Go: "go1.24.4", Platform: "linux/amd64",
	})
	first, _, found := strings.Cut(text, "\n")
	if !found {
		t.Fatalf("versionText produced no newline: %q", text)
	}
	if first != "kx v0.3.2" {
		t.Errorf("first line = %q, want %q", first, "kx v0.3.2")
	}
	if !strings.HasSuffix(text, "\n") {
		t.Errorf("versionText = %q, want it to end with a newline", text)
	}
}

// --version is the one place the whole version belongs: an untagged build's
// timestamp and commit say which build is being reported, which is the
// question anyone running --version is asking. The help screen shows the short
// form instead.
func TestVersionReportsTheWholePseudoVersion(t *testing.T) {
	text := versionText(buildinfo.Info{
		Version: "0.3.4-0.20260813184656-8ec57390b220",
		Go:      "go1.24.4", Platform: "linux/amd64",
	})
	first, _, _ := strings.Cut(text, "\n")
	if first != "kx v0.3.4-0.20260813184656-8ec57390b220" {
		t.Errorf("first line = %q, want the full version kept", first)
	}
}

// An unstamped build reports "dev", which is not a version and must not be
// dressed as one — "kx vdev" names a tag that doesn't exist.
func TestVersionLeavesAnUnstampedBuildAlone(t *testing.T) {
	text := versionText(buildinfo.Info{Version: "dev", Go: "go1.24.4", Platform: "linux/amd64"})
	first, _, _ := strings.Cut(text, "\n")
	if first != "kx dev" {
		t.Errorf("first line = %q, want %q", first, "kx dev")
	}
}

// The detail exists to make a bug report answerable: which build, from which
// source, against which config.
func TestVersionReportsTheBuildDetail(t *testing.T) {
	text := versionText(buildinfo.Info{
		Version: "0.3.2", Commit: "f80aeb8", Date: "2026-08-11",
		Go: "go1.24.4", Platform: "linux/amd64",
	})
	for _, want := range []string{"f80aeb8", "2026-08-11", "go1.24.4 linux/amd64", docsURL, "config"} {
		if !strings.Contains(text, want) {
			t.Errorf("versionText =\n%s\nmissing %q", text, want)
		}
	}
}

// A plain `go build` in a directory with no VCS data knows neither, and a
// label with nothing after it reads as a broken build rather than an
// unremarkable one.
func TestVersionOmitsUnknownFields(t *testing.T) {
	text := versionText(buildinfo.Info{Version: "dev", Go: "go1.24.4", Platform: "linux/amd64"})
	for _, absent := range []string{"commit", "built"} {
		if strings.Contains(text, absent) {
			t.Errorf("versionText =\n%s\nwant no %q line when the build did not record one", text, absent)
		}
	}
	if !strings.Contains(text, "go1.24.4") {
		t.Errorf("versionText =\n%s\nlost the toolchain line", text)
	}
}

// --version is the one output that must work on a machine kx cannot read a
// home directory on, since that is exactly the broken setup someone runs it to
// diagnose.
func TestVersionRendersWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	text := versionText(buildinfo.Info{Version: "0.3.2", Go: "go1.24.4", Platform: "linux/amd64"})
	if !strings.HasPrefix(text, "kx v0.3.2\n") {
		t.Errorf("versionText = %q, want it to still lead with the version", text)
	}
	if !strings.Contains(text, docsURL) {
		t.Errorf("versionText =\n%s\nlost the docs line", text)
	}
}

// The state file is half of what kx keeps on disk, and the half a bug report
// usually turns on — which listing an index was resolving against. --help names
// both files; --version named only the config one.
func TestVersionReportsTheStateFile(t *testing.T) {
	text := versionText(buildinfo.Info{Version: "0.3.2", Go: "go1.24.4", Platform: "linux/amd64"})

	if !strings.Contains(text, "state") {
		t.Errorf("versionText =\n%s\nmissing the state file line", text)
	}
	if !strings.Contains(text, "state.json") {
		t.Errorf("versionText =\n%s\nwant the state file's path, not just its label", text)
	}
}

// Both file lines come from the packages that own those paths, so neither can
// name a file kx does not use — and a home directory kx cannot locate drops
// them rather than guessing, the way the help screen's file list does.
func TestVersionOmitsBothFilesWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	text := versionText(buildinfo.Info{Version: "0.3.2", Go: "go1.24.4", Platform: "linux/amd64"})

	// The labels have to go too, not just the paths. A row whose value resolved
	// to the empty string still prints its label, and a bare "state" with
	// nothing after it reads as a broken build rather than an absent file.
	for _, absent := range []string{"config", "state"} {
		if strings.Contains(text, absent) {
			t.Errorf("versionText =\n%s\nkept the %q row with no home directory to resolve it", text, absent)
		}
	}
}
