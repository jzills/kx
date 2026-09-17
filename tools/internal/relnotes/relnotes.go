// Package relnotes assembles a release's notes from three tiers: a paragraph a
// person wrote, bullets derived from what actually merged, and GitHub's own
// generated commit list.
//
// The middle tier exists because the generated list alone answers "what
// commits landed" when the question a reader arrives with is "what changed for
// me". The bullets are derived rather than written so they cannot claim a
// feature that never shipped; the paragraph is written rather than derived
// because no tool can say why a release matters.
package relnotes

import (
	"regexp"
	"strconv"
	"strings"
)

// Change is one merged pull request.
type Change struct {
	Number int
	// Type and Scope are the conventional-commit prefix split apart, as the
	// convention itself defines them: "chore(deps)" is Type "chore", Scope
	// "deps". Kept separate so a category can match a type whatever its scope
	// — every fix is a fix, whether it is fix(config) or fix(render) — while
	// Dependencies can require the scope as well as the type.
	Type    string
	Scope   string
	Summary string // the title with its prefix and any trailing (#N) removed
}

// summaryHeading names the tier a person wrote. "Highlights" rather than
// "Summary" because the paragraph is meant to pick out what matters, not to
// restate the list underneath it.
const summaryHeading = "Highlights"

// Section is one category of the bullet block.
type Section struct {
	Title   string
	Changes []Change
}

// categories are the types that earn a heading, in the order they are rendered.
//
// Everything else — docs, style, test, refactor, plain chores — falls through
// to the generated list below. The block exists to be scanned, and one that
// reprints every pull request is the list it sits above.
var categories = []struct {
	title string
	match func(Change) bool
}{
	{"Features", func(c Change) bool { return c.Type == "feat" }},
	{"Fixes", func(c Change) bool { return c.Type == "fix" || c.Type == "perf" }},
	{"Dependencies", func(c Change) bool {
		return c.Scope == "deps" && (c.Type == "chore" || c.Type == "build")
	}},
}

var (
	// A merge commit: "Merge pull request #365 from jzills/fix/…", whose body
	// opens with the pull request's title.
	mergeSubject = regexp.MustCompile(`^Merge pull request #(\d+) `)
	// A squash merge: the title itself, with the number appended. Older kx
	// releases were merged this way, so a range spanning both styles has to
	// read both.
	squashSubject = regexp.MustCompile(`\s\(#(\d+)\)$`)
	// A conventional-commit prefix: type, optional scope, optional breaking
	// "!", then ": ". The scope is kept on the type so "chore(deps)" can be
	// categorised apart from a plain "chore".
	conventional = regexp.MustCompile(`^([a-z]+)(?:\(([^)]*)\))?!?:\s+(.*)$`)
	// A release branch's own merge-back, and the version bump inside it. Both
	// are the release machinery recording itself; neither is a change anyone
	// reading the notes came looking for.
	releaseMerge = regexp.MustCompile(`^Merge pull request #\d+ from \S+/release/`)
	versionBump  = regexp.MustCompile(`^chore: bump version to `)
)

// Changes reads the records Git produced for a release range.
//
// The format is subject NUL body RS, per commit. Anything that is neither a
// merge commit nor a numbered squash merge is skipped: the branch commits under
// a merge carry no number, and counting them would list the same work twice.
func Changes(log string) []Change {
	var changes []Change
	seen := map[int]bool{}

	for _, entry := range strings.Split(log, "\x1e") {
		subject, body, _ := strings.Cut(strings.TrimSpace(entry), "\x00")
		subject = strings.TrimSpace(subject)
		if subject == "" || releaseMerge.MatchString(subject) || versionBump.MatchString(subject) {
			continue
		}

		number, title := 0, ""
		if match := mergeSubject.FindStringSubmatch(subject); match != nil {
			number, _ = strconv.Atoi(match[1])
			title = firstLine(body)
		} else if match := squashSubject.FindStringSubmatch(subject); match != nil {
			number, _ = strconv.Atoi(match[1])
			title = strings.TrimSpace(squashSubject.ReplaceAllString(subject, ""))
		}
		if number == 0 || seen[number] {
			continue
		}

		parts := conventional.FindStringSubmatch(title)
		if parts == nil {
			continue
		}
		seen[number] = true
		changes = append(changes, Change{
			Number:  number,
			Type:    parts[1],
			Scope:   parts[2],
			Summary: strings.TrimSpace(parts[3]),
		})
	}
	return changes
}

func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Sections buckets changes into the categories that earn a heading, dropping
// any category nothing landed in — a release with no fixes should not
// advertise a Fixes section.
func Sections(changes []Change) []Section {
	var sections []Section
	for _, category := range categories {
		var matched []Change
		for _, change := range changes {
			if category.match(change) {
				matched = append(matched, change)
			}
		}
		if len(matched) > 0 {
			sections = append(sections, Section{Title: category.title, Changes: matched})
		}
	}
	return sections
}

// Assemble writes the three tiers in the order a reader needs them: why this
// release exists, what changed by kind, then every commit for anyone who wants
// them.
//
// Every section is an h2, including the summary's, because GitHub's own
// generated block opens with "## What's Changed" and these are its siblings
// rather than its children. Rendered at h3 they were orphans — three headings
// one level down from a parent that did not exist — and the summary, with no
// heading at all, read as a preamble to the Features list rather than as the
// release's own statement.
//
// The paragraph itself is copied verbatim under that heading. It is the one
// part a person wrote, and reflowing it would be this tool editing prose it
// did not author.
func Assemble(highlights string, sections []Section, generated string) string {
	var out strings.Builder
	if trimmed := strings.TrimSpace(highlights); trimmed != "" {
		out.WriteString("## " + summaryHeading + "\n\n")
		out.WriteString(highlights)
		if !strings.HasSuffix(highlights, "\n") {
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	for _, section := range sections {
		out.WriteString("## " + section.Title + "\n\n")
		for _, change := range section.Changes {
			out.WriteString("- " + change.Summary +
				" (#" + strconv.Itoa(change.Number) + ")\n")
		}
		out.WriteString("\n")
	}
	out.WriteString(strings.TrimLeft(generated, "\n"))
	return out.String()
}
