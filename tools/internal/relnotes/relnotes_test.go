package relnotes

import (
	"strings"
	"testing"
)

// record builds one git-log record in the shape Changes reads: subject and
// body separated by NUL, records by RS.
func record(subject, body string) string {
	return subject + "\x00" + body + "\x1e"
}

// Every PR in kx lands as a merge commit: the subject carries the number and
// the body's first line is the PR title, which is the conventional-commit
// summary the categories are read from.
func TestChangesReadsMergeCommits(t *testing.T) {
	log := record(
		"Merge pull request #365 from jzills/fix/completion-example-per-shell",
		"fix(completion): the install examples say which shell each is for",
	)

	changes := Changes(log)
	if len(changes) != 1 {
		t.Fatalf("parsed %d changes, want 1: %+v", len(changes), changes)
	}
	got := changes[0]
	if got.Number != 365 {
		t.Errorf("Number = %d, want 365", got.Number)
	}
	if got.Type != "fix" {
		t.Errorf("Type = %q, want fix", got.Type)
	}
	if got.Summary != "the install examples say which shell each is for" {
		t.Errorf("Summary = %q, want the title with its prefix stripped", got.Summary)
	}
}

// Older PRs were squash-merged, so the number is in the subject and there is
// no merge commit at all. Dropping those would silently shorten the notes for
// any release that mixes the two, which kx's history does.
func TestChangesReadsSquashMerges(t *testing.T) {
	log := record("chore(deps): bump k8s.io/cli-runtime from 0.36.4 to 0.37.0 (#341)", "")

	changes := Changes(log)
	if len(changes) != 1 {
		t.Fatalf("parsed %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Number != 341 || changes[0].Type != "chore" || changes[0].Scope != "deps" {
		t.Errorf("got %+v, want #341 typed chore, scoped deps", changes[0])
	}
	if strings.Contains(changes[0].Summary, "#341") {
		t.Errorf("Summary = %q kept the PR number, which the bullet adds itself", changes[0].Summary)
	}
}

// A merge commit and the feature commit under it both appear in the range. The
// feature commit carries no number, so it must not become a second bullet for
// work already listed.
func TestChangesDoesNotDoubleCountAMergedBranch(t *testing.T) {
	log := record(
		"Merge pull request #361 from jzills/feat/fail-on-completion",
		"feat(completion): --fail-on completes per command",
	) + record("feat(completion): --fail-on completes per command", "")

	changes := Changes(log)
	if len(changes) != 1 {
		t.Fatalf("parsed %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Number != 361 {
		t.Errorf("Number = %d, want the merge commit's 361", changes[0].Number)
	}
}

// The release-branch merge-back and the version bump are repo bookkeeping, not
// changes anyone reading release notes is looking for.
func TestChangesSkipsReleaseBookkeeping(t *testing.T) {
	log := record("Merge pull request #367 from jzills/release/v0.5.2", "chore: merge release/v0.5.2 back into develop") +
		record("chore: bump version to 0.5.2 [skip ci]", "")

	if changes := Changes(log); len(changes) != 0 {
		t.Errorf("parsed %+v, want nothing — both are release bookkeeping", changes)
	}
}

func TestSectionsBucketsByType(t *testing.T) {
	sections := Sections([]Change{
		{Number: 361, Type: "feat", Summary: "--fail-on completes per command"},
		{Number: 365, Type: "fix", Summary: "the install examples say which shell each is for"},
		{Number: 362, Type: "chore", Scope: "deps", Summary: "bump golang.org/x/term from 0.45.0 to 0.46.0"},
		{Number: 364, Type: "docs", Summary: "name both ways to set a window"},
	})

	var titles []string
	for _, section := range sections {
		titles = append(titles, section.Title)
	}
	if strings.Join(titles, ",") != "Features,Fixes,Dependencies" {
		t.Errorf("sections = %v, want Features,Fixes,Dependencies in that order", titles)
	}
}

// docs, style, test, refactor and plain chores fall through to the generated
// commit list below rather than growing a heading of their own — the bullets
// exist to be scanned, and a block that reprints every PR is the list it sits
// above.
func TestSectionsOmitsUncategorisedTypes(t *testing.T) {
	sections := Sections([]Change{
		{Number: 364, Type: "docs", Summary: "name both ways to set a window"},
		{Number: 300, Type: "style", Summary: "thicken the swatch ring"},
		{Number: 301, Type: "chore", Summary: "pin the toolchain"},
		// A dependency bump's scope is what separates it from a plain chore.
		{Number: 302, Type: "chore", Scope: "release", Summary: "tidy the pipeline"},
	})
	if len(sections) != 0 {
		t.Errorf("sections = %+v, want none — every type here falls through", sections)
	}
}

// An empty category is left out rather than rendered as a heading with nothing
// under it: a release with no fixes should not advertise a Fixes section.
func TestSectionsSkipsEmptyCategories(t *testing.T) {
	sections := Sections([]Change{{Number: 1, Type: "feat", Summary: "a feature"}})
	if len(sections) != 1 || sections[0].Title != "Features" {
		t.Errorf("sections = %+v, want Features alone", sections)
	}
}

func TestAssembleOrdersTheThreeTiers(t *testing.T) {
	notes := Assemble(
		"Sharpens shell completion.\n",
		Sections([]Change{{Number: 361, Type: "feat", Summary: "--fail-on completes per command"}}),
		"## What's Changed\n* something by @someone in https://github.com/jzills/kx/pull/361\n",
	)

	highlights := strings.Index(notes, "Sharpens shell completion")
	features := strings.Index(notes, "### Features")
	changed := strings.Index(notes, "## What's Changed")
	if highlights < 0 || features < 0 || changed < 0 {
		t.Fatalf("a tier is missing entirely:\n%s", notes)
	}
	if !(highlights < features && features < changed) {
		t.Errorf("tiers out of order — highlights=%d features=%d changed=%d:\n%s",
			highlights, features, changed, notes)
	}
	if !strings.Contains(notes, "- --fail-on completes per command (#361)") {
		t.Errorf("bullet missing or misformatted:\n%s", notes)
	}
}

// The paragraph is the one part a person wrote. It must survive verbatim
// rather than being reflowed or re-headed.
func TestAssembleKeepsTheHighlightsVerbatim(t *testing.T) {
	highlights := "One line.\n\nAnd a second paragraph with a `code span`.\n"
	notes := Assemble(highlights, nil, "## What's Changed\n")
	if !strings.Contains(notes, highlights) {
		t.Errorf("highlights were altered:\n%s", notes)
	}
}

// A release with nothing categorisable still gets its paragraph and the
// generated list — no stray heading between them.
func TestAssembleWithNoSections(t *testing.T) {
	notes := Assemble("Docs only.\n", nil, "## What's Changed\n* docs\n")
	if strings.Contains(notes, "###") {
		t.Errorf("rendered an empty category block:\n%s", notes)
	}
}
