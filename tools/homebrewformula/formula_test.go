package homebrewformula

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const scriptPath = "../../scripts/homebrew_formula.sh"

// sums writes a SHA256SUMS laid out as the release workflow writes one: every
// archive of the release, versioned and version-free, each with the hash of
// the bytes it holds. The hashes are fabricated but distinct, so a formula
// that paired a platform with another platform's hash fails rather than
// passing on a coincidence.
func sums(t *testing.T, version string, omit ...string) string {
	t.Helper()
	targets := []struct{ target, hash string }{
		{"darwin_amd64", strings.Repeat("a", 64)},
		{"darwin_arm64", strings.Repeat("b", 64)},
		{"linux_amd64", strings.Repeat("c", 64)},
		{"linux_arm64", strings.Repeat("d", 64)},
	}
	var lines []string
	for _, entry := range targets {
		if slicesContains(omit, entry.target) {
			continue
		}
		lines = append(lines,
			entry.hash+"  kx_v"+version+"_"+entry.target+".tar.gz",
			entry.hash+"  kx_"+entry.target+".tar.gz")
	}
	// The windows archives are in the real file too, and no formula may name
	// them: Homebrew would have nothing to do with a .zip of an .exe.
	windows := strings.Repeat("e", 64)
	lines = append(lines,
		windows+"  kx_v"+version+"_windows_amd64.zip",
		windows+"  kx_windows_amd64.zip")

	path := filepath.Join(t.TempDir(), "SHA256SUMS")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func slicesContains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func render(t *testing.T, version, sumsPath string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command("bash", scriptPath, version, sumsPath)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// Each platform's block has to carry its own archive and its own hash. Written
// by hand, the formula paired four urls with four hashes and nothing checked
// the pairing; a transposition installs a working binary for the wrong
// architecture, which fails at exec time on a user's machine.
func TestFormulaPairsEachArchiveWithItsOwnHash(t *testing.T) {
	formula, stderr, err := render(t, "1.2.3", sums(t, "1.2.3"))
	if err != nil {
		t.Fatalf("render: %v\n%s", err, stderr)
	}
	for _, tc := range []struct{ target, hash string }{
		{"darwin_amd64", strings.Repeat("a", 64)},
		{"darwin_arm64", strings.Repeat("b", 64)},
		{"linux_amd64", strings.Repeat("c", 64)},
		{"linux_arm64", strings.Repeat("d", 64)},
	} {
		url := `url "https://github.com/jzills/kx/releases/download/v1.2.3/kx_v1.2.3_` +
			tc.target + `.tar.gz"`
		block := url + "\n      sha256 \"" + tc.hash + "\""
		if !strings.Contains(formula, block) {
			t.Errorf("formula does not pair %s with its own hash:\n%s", tc.target, formula)
		}
	}
	// The version-free copies install whatever the latest release holds, and
	// the windows archive is not a formula's business.
	for _, unwanted := range []string{`/kx_darwin_arm64.tar.gz"`, `windows`, `.zip`} {
		if strings.Contains(formula, unwanted) {
			t.Errorf("formula names %q, which no formula may install:\n%s", unwanted, formula)
		}
	}
}

// `brew audit --strict` refuses a version field as redundant with the one it
// scans from the archive's name, and the first run of the tap's CI failed on
// exactly that. The control is the fields the formula does carry, so this
// cannot pass against an empty rendering.
func TestFormulaDeclaresNoVersion(t *testing.T) {
	formula, stderr, err := render(t, "1.2.3", sums(t, "1.2.3"))
	if err != nil {
		t.Fatalf("render: %v\n%s", err, stderr)
	}
	for _, line := range strings.Split(formula, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "version ") {
			t.Errorf("formula declares %q, which brew audit --strict refuses", strings.TrimSpace(line))
		}
	}
	for _, want := range []string{
		"class Kx < Formula", `license "MIT"`, `bin.install "kx"`,
		"generate_completions_from_executable", "scripts/homebrew_formula.sh",
	} {
		if !strings.Contains(formula, want) {
			t.Errorf("formula is missing %q:\n%s", want, formula)
		}
	}
}

// A release that published no archive for a platform would otherwise render a
// blank sha256, which Homebrew only rejects once a user runs brew install. The
// generator refuses instead, naming the archive it looked for, and prints no
// formula at all — a half-rendered one must never reach the tap.
func TestFormulaRefusesAMissingArchive(t *testing.T) {
	formula, stderr, err := render(t, "1.2.3", sums(t, "1.2.3", "linux_arm64"))
	if err == nil {
		t.Fatalf("render succeeded without the linux_arm64 archive:\n%s", formula)
	}
	if want := "lists no kx_v1.2.3_linux_arm64.tar.gz"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to name %q", stderr, want)
	}
	if strings.Contains(formula, "class Kx") {
		t.Errorf("a formula was printed despite the missing archive:\n%s", formula)
	}

	// A checksum file that isn't there at all reads the same way.
	formula, stderr, err = render(t, "1.2.3", filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatalf("render succeeded without a checksum file:\n%s", formula)
	}
	if want := "no such checksum file"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to name %q", stderr, want)
	}
}
