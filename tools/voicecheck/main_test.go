package main

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// findings runs Check and fails the test if the count is not want, returning
// the findings so a case can assert on what they say.
func findings(t *testing.T, path, content string, want int) []Finding {
	t.Helper()
	got := Check(path, []byte(content))
	if len(got) != want {
		t.Fatalf("Check(%s) found %d, want %d:\n%v", path, len(got), want, got)
	}
	return got
}

// Each rule is driven with a file that breaks it and the same file fixed, so
// a rule that matched nothing at all would fail the first half rather than
// pass both.
func TestBritishSpellingInGoCommentsAndStrings(t *testing.T) {
	got := findings(t, "internal/x/x.go", `package x

// The colour of a row.
var s = "behaviour"

func TestLastLineStripsScannerColour() {}
`, 2)
	if got[0].Line != 3 || !strings.Contains(got[0].Message, "'colour' — write 'color'") {
		t.Errorf("first finding = %v, want colour on line 3", got[0])
	}
	if got[1].Line != 4 || !strings.Contains(got[1].Message, "'behaviour'") {
		t.Errorf("second finding = %v, want behaviour on line 4", got[1])
	}
	// The test name is an identifier, not a comment or string, so it is not
	// read: the spelling rule covers prose, and a renamed identifier fails to
	// compile on its own if anything still calls it by the old name.
	findings(t, "internal/x/x.go", "package x\n\n// The color of a row.\nvar s = \"behavior\"\n", 0)
}

// A camel-cased name inside a comment is split, so the British part is found.
func TestBritishSpellingInsideACamelCaseNameInAComment(t *testing.T) {
	got := findings(t, "internal/x/x.go", "package x\n\n// See TestHonoursInstallDir.\n", 1)
	if !strings.Contains(got[0].Message, "'Honours' — write 'honors'") {
		t.Errorf("finding = %v, want Honours", got[0])
	}
}

// Words that merely end like a British spelling are not one.
func TestLookalikesAreNotBritish(t *testing.T) {
	findings(t, "internal/x/x.go", "package x\n\n// A promise, an exercise, otherwise a premise.\n", 0)
	// The plural of analysis is the same in both.
	findings(t, "internal/x/x.go", "package x\n\n// The two analyses agree.\n", 0)
}

// MCP names a method notifications/cancelled; kx has to spell it MCP's way.
func TestProtocolNamesAreExempt(t *testing.T) {
	findings(t, "internal/cli/x_test.go", "package cli\n\n// It sends notifications/cancelled.\n", 0)
	findings(t, "internal/cli/x_test.go", "package cli\n\n// The scan was cancelled.\n", 1)
}

func TestLabelPrefixInAMessage(t *testing.T) {
	got := findings(t, "internal/cli/x.go", "package cli\n\nvar m = \"note: index 3 is from an agent\"\n", 1)
	if !strings.Contains(got[0].Message, `"note: "`) {
		t.Errorf("finding = %v, want it to quote the label", got[0])
	}
	findings(t, "internal/cli/x.go", "package cli\n\nvar m = \"Index 3 is from an agent.\"\n", 0)
	// Only kx's own message packages are held to it, and not their tests,
	// which quote rejected wording on purpose.
	findings(t, "internal/scanner/x.go", "package scanner\n\nvar m = \"warning: from grype\"\n", 0)
	findings(t, "internal/cli/x_test.go", "package cli\n\nvar m = \"note: old\"\n", 0)
}

func TestSentenceOpeningWithCodeInTheReadme(t *testing.T) {
	got := findings(t, "README.md", "# kx\n\n`--html` serves a report.\n- `kx scan` finds images.\n", 2)
	for i, line := range []int{3, 4} {
		if got[i].Line != line || !strings.Contains(got[i].Message, "inline code") {
			t.Errorf("finding %d = %v, want inline code on line %d", i, got[i], line)
		}
	}
	findings(t, "README.md", "# kx\n\nThe `--html` flag serves a report.\n", 0)
}

func TestSentenceOpeningWithLowercaseKx(t *testing.T) {
	got := findings(t, "site/content/docs/guides/x.md", "kx reads your kubeconfig.\n", 1)
	if !strings.Contains(got[0].Message, `lowercase "kx"`) {
		t.Errorf("finding = %v, want the lowercase kx rule", got[0])
	}
	findings(t, "site/content/docs/guides/x.md", "Settings live in your kubeconfig.\n", 0)
}

// Code, front matter, tables and headings are not sentences, and the command
// reference is generated from help strings, which have rules of their own.
func TestNonProseIsNotReadAsASentence(t *testing.T) {
	findings(t, "README.md", "---\ntitle: kx\n---\n\n```bash\nkx get pods\n`x`\n```\n\n| `kx` | x |\n## `kx get`\n", 0)
	findings(t, "site/content/docs/reference/commands/get.md", "`kx get` lists resources.\n", 0)
	// Docs that aren't the pitch or the site are not held to sentence
	// openings, though their spelling still is.
	findings(t, "CONTRIBUTING.md", "`go test` runs the suite.\n", 0)
	findings(t, "CONTRIBUTING.md", "The colour of a row.\n", 1)
}

func TestHardWrappedReleaseNotes(t *testing.T) {
	got := findings(t, "release-notes/v9.9.9.md", "A patch that fixes\nthe banner order.\n\nUpgrading asks nothing of you.\n", 1)
	if got[0].Line != 2 || got[0].Section != "Release notes" {
		t.Errorf("finding = %v, want the wrap on line 2", got[0])
	}
	findings(t, "release-notes/v9.9.9.md", "A patch that fixes the banner order.\n\nUpgrading asks nothing of you.\n", 0)
	// A list is one item per line, not a wrapped paragraph; an item wrapped
	// onto a second line still is one.
	findings(t, "release-notes/v9.9.9.md", "Two fixes:\n\n- one\n- two\n1. three\n", 0)
	got = findings(t, "release-notes/v9.9.9.md", "- one item that\n  wraps\n", 1)
	if got[0].Line != 2 {
		t.Errorf("finding = %v, want the wrapped item on line 2", got[0])
	}
	// Consecutive lines are only a wrap in a release note; README prose is
	// one sentence per line by design.
	findings(t, "README.md", "One sentence.\nAnother sentence.\n", 0)
}

func TestVendoredFilesAreSkipped(t *testing.T) {
	findings(t, "internal/web/vendor/x/x.go", "package x\n\n// colour\n", 0)
	findings(t, "site/_vendor/x.md", "colour\n", 0)
	findings(t, "tools/voicecheck/words.go", "package main\n\n// colour\n", 0)
}

func TestFindingNamesItsSection(t *testing.T) {
	got := findings(t, "internal/x/x.go", "package x\n\n// grey\n", 1)
	if want := "internal/x/x.go:3: British spelling 'grey' — write 'gray' (STYLE.md: The voice)"; got[0].String() != want {
		t.Errorf("String() = %q, want %q", got[0].String(), want)
	}
}

// A tracked path with a space or a non-ASCII character is read whole. Split on
// whitespace, or read in git's quoting, it named no file, and every rule
// skipped it without a word.
func TestTrackedFilesReadsEveryPathWhole(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	want := []string{"My Guide.md", "naïve.md"}
	for _, name := range want {
		if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	got, err := trackedFiles()
	if err != nil {
		t.Fatalf("trackedFiles: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("trackedFiles() = %q, want %q", got, want)
	}
}
