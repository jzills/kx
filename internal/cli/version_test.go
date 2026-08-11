package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/buildinfo"
)

// The release workflow compares the binary's first line against "kx <version>"
// exactly, and anything parsing `kx --version` was written when that was the
// whole output. The detail below it may grow; that line may not.
func TestVersionFirstLineIsTheVersionAlone(t *testing.T) {
	text := versionText(buildinfo.Info{
		Version: "0.3.2", Commit: "f80aeb8", Date: "2026-08-11",
		Go: "go1.24.4", Platform: "linux/amd64",
	})
	first, _, found := strings.Cut(text, "\n")
	if !found {
		t.Fatalf("versionText produced no newline: %q", text)
	}
	if first != "kx 0.3.2" {
		t.Errorf("first line = %q, want %q", first, "kx 0.3.2")
	}
	if !strings.HasSuffix(text, "\n") {
		t.Errorf("versionText = %q, want it to end with a newline", text)
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
	if !strings.HasPrefix(text, "kx 0.3.2\n") {
		t.Errorf("versionText = %q, want it to still lead with the version", text)
	}
	if !strings.Contains(text, docsURL) {
		t.Errorf("versionText =\n%s\nlost the docs line", text)
	}
}
