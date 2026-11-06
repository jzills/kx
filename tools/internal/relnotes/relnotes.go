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
	"unicode"
	"unicode/utf8"
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
	// Breaking is the conventional-commit "!" on the prefix, the one
	// machine-readable thing a title says about what upgrading costs. Read
	// from the title alone: this tier is built from pull request titles, so
	// the "BREAKING CHANGE:" footer form has no body here to be found in.
	Breaking bool
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
//
// Breaking changes lead, and they are matched on the marker rather than on a
// type: a reader deciding whether to upgrade needs them before the features,
// and breakage can arrive as any type — a chore that raises the minimum Go
// version costs its readers something a feat often does not. The marker
// promotes a change into this section rather than the type letting it through,
// so a type that earns no heading of its own still surfaces when it is marked.
var categories = []struct {
	title string
	match func(Change) bool
}{
	{"Breaking changes", func(c Change) bool { return c.Breaking }},
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
	// categorized apart from a plain "chore", and the "!" is captured rather
	// than merely tolerated — matched and discarded, it let `feat!: …` render
	// as an ordinary Features bullet, indistinguishable from a change that
	// asks nothing of anyone.
	conventional = regexp.MustCompile(`^([a-z]+)(?:\(([^)]*)\))?(!)?:\s+(.*)$`)
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
			Number:   number,
			Type:     parts[1],
			Scope:    parts[2],
			Breaking: parts[3] == "!",
			Summary:  strings.TrimSpace(parts[4]),
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
//
// The first matching category wins, so a change appears once. A breaking fix
// listed under both Breaking changes and Fixes reads as two changes, and the
// second telling leaves out the thing that mattered about it.
func Sections(changes []Change) []Section {
	var sections []Section
	claimed := make(map[int]bool, len(changes))
	for _, category := range categories {
		var matched []Change
		for _, change := range changes {
			if claimed[change.Number] || !category.match(change) {
				continue
			}
			claimed[change.Number] = true
			matched = append(matched, change)
		}
		if len(matched) > 0 {
			sections = append(sections, Section{Title: category.title, Changes: matched})
		}
	}
	return sections
}

// Assemble writes the notes in the order a reader needs them: why this
// release exists, what changed by kind, then what GitHub adds that nothing
// above gives (withoutCommitList) — new contributors and the link to every
// commit.
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
			out.WriteString("- " + sentence(change.Summary,
				" (#"+strconv.Itoa(change.Number)+")") + "\n")
		}
		out.WriteString("\n")
	}
	out.WriteString(spaceHeadings(strings.TrimLeft(withoutCommitList(generated), "\n")))
	return out.String()
}

// commitListHeading opens the list of every merged PR in GitHub's block.
const commitListHeading = "## What's Changed"

// withoutCommitList is GitHub's block with its commit list dropped. The
// list repeated the Features, Fixes and Dependencies bullets nearly line for
// line: 16 of v0.12.0's 18 entries, the other two a merge-back and a
// refactor. What stays is what nothing else in the notes gives, New
// Contributors, which credits a first-time contributor, and the Full
// Changelog link, which still reaches every commit.
func withoutCommitList(generated string) string {
	var kept []string
	dropping := false
	for _, line := range strings.Split(generated, "\n") {
		switch {
		case strings.TrimSpace(line) == commitListHeading:
			dropping = true
			continue
		case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "**Full Changelog**"):
			dropping = false
		}
		if dropping {
			continue
		}
		// One blank line between parts: the list leaves two behind it.
		if strings.TrimSpace(line) == "" && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// sentence is a PR title's subject as a release-note bullet: capitalized,
// with ref before the closing period, as in "A plugin pack that runs kx on
// the row under k9s's cursor (#667).". A title is lowercase with no period
// by convention, and the bullets under Highlights are sentences like every
// other line in the notes. A subject that opens with a flag or "kx" is left
// lowercase, since neither takes a capital; the kx-pr skill keeps titles
// from opening that way.
func sentence(subject, ref string) string {
	subject = namedOpening(codeFlags(subject))
	first, size := utf8.DecodeRuneInString(subject)
	if unicode.IsLower(first) && !lowercaseName.MatchString(subject) {
		subject = string(unicode.ToUpper(first)) + subject[size:]
	}
	subject = strings.TrimRight(subject, ".")
	return subject + ref + "."
}

// flagWord is a flag as a PR title writes it, bare: --context, -o,
// --since=1h. Only at the start of a word, so Shift-T and not-found are
// words, not flags.
var flagWord = regexp.MustCompile(`(^|[\s(])(--?[A-Za-z][A-Za-z0-9-]*(=[^\s,;)]+)?)`)

// codeFlags puts each flag in a subject in backticks. A title carries none
// (STYLE.md), and in the notes a flag is code like every other one there.
func codeFlags(subject string) string {
	return flagWord.ReplaceAllString(subject, "$1`$2`")
}

// openingFlag and openingCommand are a subject that opens with a flag, as
// codeFlags has written it, or with a kx command.
var (
	openingFlag    = regexp.MustCompile("^(`--?[^`]+`)")
	openingCommand = regexp.MustCompile(`^kx ([a-z][a-z-]*)\b`)
)

// namedOpening gives a subject that opens with a flag or a kx command a word
// to open with, as the notes write either mid-sentence: "--context reaches
// diag" is "The `--context` flag reaches diag", and "kx tree is Shift-T" is
// "The `kx tree` command is Shift-T". A command is kx and the one word after
// it, so a subcommand would be read short — pr-title.yml refuses a title
// opening either way, and this only rewrites ones merged before it did.
func namedOpening(subject string) string {
	if match := openingFlag.FindString(subject); match != "" {
		return "The " + match + " flag" + subject[len(match):]
	}
	if match := openingCommand.FindStringSubmatch(subject); match != nil {
		return "The `kx " + match[1] + "` command" + subject[len(match[0]):]
	}
	return subject
}

// lowercaseName is a subject whose first word is a name written lowercase:
// kx or kubectl, possessive included, or an identifier such as
// list_resources. Capitalized, each is a different word, Kubectl or
// List_resources, so it is left as written; the title check refuses a title
// that opens with one.
var lowercaseName = regexp.MustCompile(`^(kx|kubectl)($|[\s'’,.:;])|^[a-z0-9]+_[a-z0-9_]+\b`)

// spaceHeadings puts a blank line after each heading in GitHub's generated
// block that runs straight into its list, as "## What's Changed" does. Every
// heading this tool writes has one, and the commit list's was the only one
// without it. Nothing else in the block is touched: it stays what GitHub
// produced, one blank line apart.
func spaceHeadings(generated string) string {
	lines := strings.Split(generated, "\n")
	var out []string
	for i, line := range lines {
		out = append(out, line)
		if strings.HasPrefix(line, "#") && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}
