package cli

import (
	"strings"
	"testing"
)

// kx docs is the one command that answers without a cluster, a kubeconfig or
// a saved listing. Driven through real argv, because a RunE call with the
// topic as an argument would skip cobra's parse and prove nothing about the
// command as a user runs it.
func docsPrinted(t *testing.T, argv ...string) string {
	t.Helper()
	sink := captureRender(t)
	if err := Execute(NewRoot(argvServices(t), "test"), argv); err != nil {
		t.Fatalf("kx %s: %v", strings.Join(argv, " "), err)
	}
	return sink.String()
}

// The shortest spelling has to work: a reference page reached by the command
// it documents, not by the path it sits at.
func TestDocsPrintsAPageByItsLastSegment(t *testing.T) {
	out := docsPrinted(t, "docs", "top")

	if !strings.Contains(out, "kx top") {
		t.Errorf("the page's title is missing:\n%s", out)
	}
	if !strings.Contains(out, "--sort") {
		t.Errorf("the page's body is missing — only its heading was printed:\n%s", out)
	}
	// The URL is how a reader gets the formatted page, and the control that
	// the right page was found rather than a plausible other one.
	if !strings.Contains(out, "/docs/reference/commands/top/") {
		t.Errorf("the page does not carry where it is published:\n%s", out)
	}

	// The full path reaches the same page, so neither spelling is the only one.
	if byPath := docsPrinted(t, "docs", "reference/commands/top"); byPath != out {
		t.Errorf("'top' and 'reference/commands/top' printed different pages")
	}
}

// 'state' is a concept and a command. Answering one of them silently is the
// wrong half of the answer as often as not, so both are offered.
func TestDocsOffersBothPagesForAnAmbiguousTopic(t *testing.T) {
	out := docsPrinted(t, "docs", "state")

	for _, want := range []string{"concepts/state", "reference/commands/state"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not among the pages offered:\n%s", want, out)
		}
	}
	// The control: an offer is a list, not a page. A page would carry the
	// prose of one of them and settle the ambiguity by accident.
	if strings.Contains(out, "── State and history ──") {
		t.Errorf("an ambiguous topic printed a page instead of offering both:\n%s", out)
	}
}

// A half-remembered word should still reach the documentation, so a topic
// that names no page is searched for rather than refused outright.
func TestDocsSuggestsPagesForATopicThatNamesNone(t *testing.T) {
	out := docsPrinted(t, "docs", "homebrew")

	if !strings.Contains(out, "getting-started/install") {
		t.Errorf("the search did not reach the install page:\n%s", out)
	}
	if !strings.Contains(out, "No page named 'homebrew'") {
		t.Errorf("the output does not say the topic named no page:\n%s", out)
	}
}

// A word that matches nothing at all is an error naming the way out, in the
// shape STYLE.md gives a value kx cannot resolve.
func TestDocsRefusesAWordThatMatchesNothing(t *testing.T) {
	err := Execute(NewRoot(argvServices(t), "test"), []string{"docs", "xyzzy"})
	if err == nil {
		t.Fatal("a topic matching nothing was accepted")
	}
	if want := "No documentation for 'xyzzy' — run 'kx docs' to list the topics."; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// With no topic the command lists what there is to read. Every page has to be
// on it, or a page exists that nobody at a terminal can find.
func TestDocsListsEveryTopic(t *testing.T) {
	out := docsPrinted(t, "docs")

	for _, want := range []string{
		"getting-started/install", "concepts/marks", "guides/triage-a-namespace",
		"reference/commands/top", "reference/configuration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from the topic list:\n%s", want, out)
		}
	}
	for _, section := range []string{"Getting started", "Concepts", "Guides", "Commands"} {
		if !strings.Contains(out, section) {
			t.Errorf("the list has no %q section:\n%s", section, out)
		}
	}
	// The documentation's own front page publishes at /docs/ and so has no
	// topic to name it by. Listing it offers a reader a page they cannot ask
	// for, which is what a blank left-hand column looked like.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Install kx, learn the index workflow") {
			t.Errorf("the unaddressable front page is on the topic list: %q", line)
		}
	}
}

// A '#' inside a fenced block is a comment in a shell example. Styling it as
// a heading would misdescribe the shape of the page, and the example's lines
// have to survive in the order they were written either way.
func TestDocsKeepsAFencedExampleIntact(t *testing.T) {
	out := docsPrinted(t, "docs", "quickstart")

	at := strings.Index(out, "kx get pods")
	if at < 0 {
		t.Fatalf("the quickstart's first command is missing:\n%s", out)
	}
	// A fence marker is scaffolding for a renderer that does not style code,
	// and printing it would show the reader Markdown rather than a page.
	if strings.Contains(out, "```") {
		t.Errorf("a fence marker reached the terminal:\n%s", out)
	}
}

// A documentation page's tables are drawn as kx tables. Markdown's own shape
// is unreadable in a terminal — the reference pages run to 600 characters a
// row, so every one of them wraps — and kx has a table renderer that fits the
// width and ellipsizes instead.
func TestDocsDrawsAMarkdownTableAsAKxTable(t *testing.T) {
	out := docsPrinted(t, "docs", "cp")

	if strings.Contains(out, "|---|") {
		t.Errorf("the table's delimiter row reached the terminal:\n%s", out)
	}
	if strings.Contains(out, "| Argument | Description |") {
		t.Errorf("the table printed as raw Markdown:\n%s", out)
	}
	// The control for both: the table's content is still there, so neither
	// check is passing because the block was dropped.
	for _, want := range []string{"ARGUMENT", "DESCRIPTION", "<src>", "<dest>"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from the drawn table:\n%s", want, out)
		}
	}
}

// The site generator escapes '<' and '>' for Hugo. Nothing unescaped them for
// a terminal, so kx docs cp described a source path as
// "&lt;index&gt;:&lt;path&gt;".
func TestDocsUnescapesWhatTheSiteGeneratorEscaped(t *testing.T) {
	out := docsPrinted(t, "docs", "cp")

	if strings.Contains(out, "&lt;") || strings.Contains(out, "&gt;") {
		t.Errorf("an HTML entity reached the terminal:\n%s", out)
	}
	if !strings.Contains(out, "<index>:<path>") {
		t.Errorf("the unescaped text is not in the output:\n%s", out)
	}
}

// A cell may hold a pipe, which the site generator writes as \|. Split on
// every pipe and that cell becomes two columns, silently shifting every cell
// after it.
func TestSplitDocsRowKeepsAnEscapedPipe(t *testing.T) {
	cells := splitDocsRow(`| ` + "`-o`" + ` | json\|yaml\|name | the output shape |`)
	want := []string{"`-o`", "json|yaml|name", "the output shape"}
	if len(cells) != len(want) {
		t.Fatalf("split into %d cells %q, want %d", len(cells), cells, len(want))
	}
	for i := range want {
		if cells[i] != want[i] {
			t.Errorf("cell %d = %q, want %q", i, cells[i], want[i])
		}
	}
}

// A line that begins with a pipe is not always a table. Half-rendering one is
// worse than leaving it as the text it is.
func TestATableNeedsAHeaderADelimiterAndARow(t *testing.T) {
	full := []string{"| A | B |", "|---|---|", "| 1 | 2 |", "after"}
	if block := tableBlock(full); len(block) != 3 {
		t.Errorf("tableBlock took %d lines, want the 3 of the table", len(block))
	}
	if _, _, ok := parseDocsTable(full[:3]); !ok {
		t.Error("a well-formed table did not parse")
	}

	for _, lines := range [][]string{
		{"| A | B |", "| 1 | 2 |", "| 3 | 4 |"}, // no delimiter row
		{"| A | B |", "|---|---|"},              // no rows
		{"not a table", "| A |"},                // does not start one
	} {
		if block := tableBlock(lines); len(block) >= 3 {
			if _, _, ok := parseDocsTable(block); ok {
				t.Errorf("parsed %q as a table", lines)
			}
		}
	}
}
