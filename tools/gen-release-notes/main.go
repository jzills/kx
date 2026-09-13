// Command gen-release-notes assembles a release's notes: the paragraph written
// for this release, bullets derived from what merged since the last one, and
// GitHub's own generated commit list.
//
//	go run ./tools/gen-release-notes --version 0.5.3 --out notes.md
//
// Run by release.yml between tagging and creating the GitHub release. The
// paragraph is required and its absence is already fatal by then — the
// workflow checks for the file before it builds or publishes anything, so a
// missing one costs a re-push rather than a half-finished release.
//
// The generated list comes from the GitHub API rather than being rebuilt here,
// so the tier a reader scrolls to for detail stays exactly what GitHub would
// have produced on its own.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jzills/kx/tools/internal/relnotes"
)

func main() {
	version := flag.String("version", "", "release version, with or without a leading v")
	previous := flag.String("previous", "", "previous release tag (default: the highest tag below --version)")
	out := flag.String("out", "", "file to write the notes to (default: stdout)")
	flag.Parse()

	if *version == "" {
		fail("--version is required")
	}
	tag := "v" + strings.TrimPrefix(*version, "v")

	prior := *previous
	if prior == "" {
		found, err := previousTag(tag)
		if err != nil {
			fail("finding the previous tag: %v", err)
		}
		prior = found
	}

	highlights, err := os.ReadFile(notesPath(tag))
	if err != nil {
		fail("reading the release summary: %v", err)
	}

	log, err := run("git", "log", "--format=%s%x00%b%x1e", prior+".."+tag)
	if err != nil {
		// Before the tag exists — a local dry run — the range still resolves
		// against the branch, which is what someone previewing wants.
		if log, err = run("git", "log", "--format=%s%x00%b%x1e", prior+"..HEAD"); err != nil {
			fail("reading the commit range: %v", err)
		}
	}

	generated, err := generatedNotes(tag, prior)
	if err != nil {
		fail("asking GitHub for the commit list: %v", err)
	}

	notes := relnotes.Assemble(
		string(highlights), relnotes.Sections(relnotes.Changes(log)), generated)

	if *out == "" {
		fmt.Print(notes)
		return
	}
	if err := os.WriteFile(*out, []byte(notes), 0o644); err != nil {
		fail("writing %s: %v", *out, err)
	}
}

// notesPath is where a release's paragraph lives. One file per release, named
// by tag, so it is reviewed in the release pull request alongside the code it
// describes rather than written into a box on a web page afterwards.
//
// Deliberately not under docs/: .gitignore anchors "/docs/" for the design
// specs kept there, so a summary written to docs/releases/ sits happily on the
// author's disk, never reaches the repository, and fails the workflow's gate
// from a clean checkout — the one place the mistake is expensive.
func notesPath(tag string) string {
	return filepath.Join("release-notes", tag+".md")
}

// previousTag is the highest release tag below this one, which is the range the
// notes cover. Read from git rather than computed, so a skipped or yanked
// version cannot produce a range that never shipped.
func previousTag(tag string) (string, error) {
	listed, err := run("git", "tag", "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	for _, candidate := range strings.Fields(listed) {
		if candidate != tag {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no tag below %s — is this the first release?", tag)
}

// generatedNotes is GitHub's own "What's Changed" body for the range.
func generatedNotes(tag, previous string) (string, error) {
	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		repo = "jzills/kx"
	}
	return run("gh", "api", "--method", "POST",
		"repos/"+repo+"/releases/generate-notes",
		"-f", "tag_name="+tag,
		"-f", "previous_tag_name="+previous,
		"--jq", ".body")
}

func run(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(output), nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-release-notes: "+format+"\n", args...)
	os.Exit(1)
}
